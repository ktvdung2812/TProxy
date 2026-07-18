package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tproxy/tproxy/internal/store"
)

// Google service-account access tokens normally live for one hour. Refresh
// five minutes before expiry while reusing the cached token for the rest of
// its lifetime; a multi-hour buffer would force a mint on every request.
const vertexTokenBuffer = 5 * time.Minute

type vertexServiceAccount struct {
	Type        string `json:"type"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	ProjectID   string `json:"project_id"`
}

type vertexTokenCacheEntry struct {
	token     string
	expiresAt time.Time
}

var vertexTokenCache sync.Map

func (m *Manager) ensureVertexServiceAccount(ctx context.Context, credential store.Credential, force bool) (store.Credential, error) {
	plaintext := credential.Secret
	if credential.AuthType == "oauth" && credential.OAuthToken != nil && credential.OAuthToken.AccessToken != "" {
		plaintext = credential.OAuthToken.AccessToken
	}
	if plaintext == "" {
		return credential, &Error{code: "authorization_required", permanent: true}
	}
	sa, err := parseVertexServiceAccount(plaintext)
	if err != nil {
		return credential, &Error{code: "oauth_configuration_invalid", permanent: true, err: err}
	}
	if !force {
		if cached, ok := vertexTokenCache.Load(sa.ClientEmail); ok {
			entry := cached.(vertexTokenCacheEntry)
			if entry.expiresAt.Sub(m.now()) > vertexTokenBuffer {
				credential.Secret = entry.token
				credential.TokenType = "Bearer"
				return credential, nil
			}
		}
	}
	token, expiresAt, err := m.mintVertexAccessToken(ctx, sa)
	if err != nil {
		return credential, err
	}
	vertexTokenCache.Store(sa.ClientEmail, vertexTokenCacheEntry{token: token, expiresAt: expiresAt})
	credential.Secret = token
	credential.TokenType = "Bearer"
	if credential.AuthType == "service_account" {
		metadata := credential.Metadata
		if metadata == nil {
			metadata = map[string]any{}
		}
		metadata["vertex_access_expires_at"] = expiresAt.UTC().Format(time.RFC3339Nano)
		credential.Metadata = metadata
		if err = m.store.UpdateCredentialMetadata(ctx, credential.ID, credentialMetadataForPersistence(credential)); err != nil {
			return credential, err
		}
	}
	return credential, nil
}

func parseVertexServiceAccount(raw string) (vertexServiceAccount, error) {
	var sa vertexServiceAccount
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &sa); err != nil {
		return vertexServiceAccount{}, fmt.Errorf("parse service account json: %w", err)
	}
	if sa.Type != "service_account" || sa.ClientEmail == "" || sa.PrivateKey == "" || sa.ProjectID == "" {
		return vertexServiceAccount{}, errors.New("service account json is incomplete")
	}
	return sa, nil
}

func (m *Manager) mintVertexAccessToken(ctx context.Context, sa vertexServiceAccount) (string, time.Time, error) {
	assertion, err := signVertexJWT(sa, m.now())
	if err != nil {
		return "", time.Time{}, &Error{code: "oauth_provider_unavailable", err: err}
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := m.client.Do(request)
	if err != nil {
		return "", time.Time{}, &Error{code: "oauth_provider_unavailable", err: err}
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", time.Time{}, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("vertex token exchange failed (%d)", response.StatusCode)}
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err = json.Unmarshal(data, &payload); err != nil || payload.AccessToken == "" {
		return "", time.Time{}, &Error{code: "oauth_provider_unavailable", err: errors.New("invalid vertex token response")}
	}
	expiresIn := payload.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	return payload.AccessToken, m.now().Add(time.Duration(expiresIn) * time.Second), nil
}

func signVertexJWT(sa vertexServiceAccount, now time.Time) (string, error) {
	privateKey, err := parseRSAPrivateKey(sa.PrivateKey)
	if err != nil {
		return "", err
	}
	issued := now.Unix()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims := map[string]any{
		"iss":   sa.ClientEmail,
		"sub":   sa.ClientEmail,
		"aud":   "https://oauth2.googleapis.com/token",
		"iat":   issued,
		"exp":   issued + 3600,
		"scope": "https://www.googleapis.com/auth/cloud-platform",
	}
	claimJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := header + "." + base64.RawURLEncoding.EncodeToString(claimJSON)
	sum := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func parseRSAPrivateKey(pemData string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(strings.ReplaceAll(pemData, `\n`, "\n")))
	if block == nil {
		return nil, errors.New("invalid private key pem")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PrivateKey); ok {
			return rsaKey, nil
		}
		return nil, errors.New("private key is not rsa")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}
