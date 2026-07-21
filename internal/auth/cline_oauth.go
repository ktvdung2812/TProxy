package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/store"
)

const (
	clineAuthorizeURL          = "https://api.cline.bot/api/v1/auth/authorize"
	clineTokenURL              = "https://api.cline.bot/api/v1/auth/token"
	clineRefreshURL            = "https://api.cline.bot/api/v1/auth/refresh"
	clineAuthkitDeviceRedirect = "https://authkit.cline.bot/device"
)

func isClineProvider(providerType string) bool {
	switch providerType {
	case "cline", "clinepass":
		return true
	default:
		return false
	}
}

func clineAuthorizationURL(redirectURL string) string {
	params := url.Values{
		"client_type":  {"extension"},
		"callback_url": {redirectURL},
		"redirect_uri": {redirectURL},
	}
	return clineAuthorizeURL + "?" + params.Encode()
}

func normalizeClineAccessToken(token string) string {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "workos:") {
		return trimmed
	}
	return "workos:" + trimmed
}

func clineTokenFromMap(data map[string]any, now time.Time) (store.OAuthToken, error) {
	access := stringValue(firstValue(data, "accessToken", "access_token"))
	if access == "" {
		return store.OAuthToken{}, &Error{code: "oauth_provider_unavailable", err: errors.New("Cline token response did not contain an access token")}
	}
	token := store.OAuthToken{
		AccessToken:  normalizeClineAccessToken(access),
		RefreshToken: stringValue(firstValue(data, "refreshToken", "refresh_token")),
		TokenType:    "Bearer",
		Extra:        map[string]any{},
	}
	if expiresAt := stringValue(data["expiresAt"]); expiresAt != "" {
		if parsed, err := time.Parse(time.RFC3339, expiresAt); err == nil {
			token.ExpiresAt = parsed.UTC()
		}
	}
	if token.ExpiresAt.IsZero() {
		if expires := durationSeconds(data["expires_in"], 0); expires > 0 {
			token.ExpiresAt = now.Add(expires)
		}
	}
	if email := stringValue(data["email"]); email != "" {
		token.Extra["email"] = email
	}
	if first := stringValue(data["firstName"]); first != "" {
		token.Extra["first_name"] = first
	}
	if last := stringValue(data["lastName"]); last != "" {
		token.Extra["last_name"] = last
	}
	return token, nil
}

func decodeClineCallbackCode(code string) (map[string]any, error) {
	base64Code := strings.TrimSpace(code)
	padding := 4 - (len(base64Code) % 4)
	if padding != 4 {
		base64Code += strings.Repeat("=", padding)
	}
	decoded, err := base64.StdEncoding.DecodeString(base64Code)
	if err != nil {
		return nil, err
	}
	lastBrace := strings.LastIndex(string(decoded), "}")
	if lastBrace == -1 {
		return nil, errors.New("no JSON found in decoded Cline code")
	}
	var payload map[string]any
	if err := json.Unmarshal(decoded[:lastBrace+1], &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (m *Manager) exchangeClineCode(ctx context.Context, code, redirectURL, tokenURL string) (store.OAuthToken, error) {
	if payload, err := decodeClineCallbackCode(code); err == nil {
		return clineTokenFromMap(payload, m.now())
	}
	if tokenURL == "" {
		tokenURL = clineTokenURL
	}
	var lastErr error
	for _, candidateRedirect := range clineTokenExchangeRedirectURIs(redirectURL) {
		token, err := m.exchangeClineCodeAtRedirect(ctx, code, candidateRedirect, tokenURL)
		if err == nil {
			return token, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return store.OAuthToken{}, lastErr
	}
	return store.OAuthToken{}, &Error{code: "oauth_provider_unavailable", err: errors.New("Cline token exchange failed")}
}

func clineTokenExchangeRedirectURIs(redirectURL string) []string {
	seen := map[string]struct{}{}
	ordered := make([]string, 0, 2)
	for _, candidate := range []string{strings.TrimSpace(redirectURL), clineAuthkitDeviceRedirect} {
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		ordered = append(ordered, candidate)
	}
	return ordered
}

func (m *Manager) exchangeClineCodeAtRedirect(ctx context.Context, code, redirectURL, tokenURL string) (store.OAuthToken, error) {
	body, err := json.Marshal(map[string]string{
		"grant_type":   "authorization_code",
		"code":         code,
		"client_type":  "extension",
		"redirect_uri": redirectURL,
	})
	if err != nil {
		return store.OAuthToken{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(body))
	if err != nil {
		return store.OAuthToken{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("HTTP-Referer", "https://cline.bot")
	request.Header.Set("X-Title", "Cline")
	response, err := m.client.Do(request)
	if err != nil {
		return store.OAuthToken{}, &Error{code: "oauth_provider_unavailable", err: err}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return store.OAuthToken{}, &Error{code: "oauth_provider_unavailable"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return store.OAuthToken{}, oauthHTTPError(data, response.StatusCode, false)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return store.OAuthToken{}, &Error{code: "oauth_provider_unavailable", err: errors.New("invalid Cline token response")}
	}
	if nested, ok := raw["data"].(map[string]any); ok {
		raw = nested
	}
	return clineTokenFromMap(raw, m.now())
}

func (m *Manager) refreshClineToken(ctx context.Context, refreshToken, refreshURL string) (store.OAuthToken, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return store.OAuthToken{}, &Error{code: "oauth_configuration_invalid", permanent: true}
	}
	body, err := json.Marshal(map[string]string{
		"refreshToken": refreshToken,
		"grantType":    "refresh_token",
		"clientType":   "extension",
	})
	if err != nil {
		return store.OAuthToken{}, err
	}
	if refreshURL == "" {
		refreshURL = clineRefreshURL
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, refreshURL, bytes.NewReader(body))
	if err != nil {
		return store.OAuthToken{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("HTTP-Referer", "https://cline.bot")
	request.Header.Set("X-Title", "Cline")
	response, err := m.client.Do(request)
	if err != nil {
		return store.OAuthToken{}, &Error{code: "oauth_provider_unavailable", err: err}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return store.OAuthToken{}, &Error{code: "oauth_provider_unavailable"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return store.OAuthToken{}, oauthHTTPError(data, response.StatusCode, true)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return store.OAuthToken{}, &Error{code: "oauth_provider_unavailable", err: errors.New("invalid Cline refresh response")}
	}
	if nested, ok := raw["data"].(map[string]any); ok {
		raw = nested
	}
	token, err := clineTokenFromMap(raw, m.now())
	if err != nil {
		return store.OAuthToken{}, err
	}
	if token.RefreshToken == "" {
		token.RefreshToken = refreshToken
	}
	return token, nil
}

func (m *Manager) enrichClineToken(ctx context.Context, token store.OAuthToken, email string) (store.OAuthToken, string, error) {
	token.AccessToken = normalizeClineAccessToken(token.AccessToken)
	if email == "" {
		email = stringValue(token.Extra["email"])
	}
	if email != "" {
		if token.Extra == nil {
			token.Extra = map[string]any{}
		}
		token.Extra["email"] = email
	}
	return token, email, nil
}
