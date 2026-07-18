package auth

import (
	"context"
	"encoding/json"
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

const (
	copilotTokenURL         = "https://api.github.com/copilot_internal/v2/token"
	copilotDefaultAPI       = "https://api.githubcopilot.com"
	copilotTokenBuffer      = 5 * time.Minute
	copilotUserAgent        = "GitHubCopilotChat/0.38.0"
	copilotEditorVersion    = "vscode/1.110.0"
	copilotPluginVersion    = "copilot-chat/0.38.0"
	copilotGitHubAPIVersion = "2025-04-01"
)

type copilotAPIToken struct {
	JWT         string
	APIEndpoint string
	ExpiresAt   time.Time
}

type copilotExchangeCall struct {
	done   chan struct{}
	result copilotAPIToken
	err    error
}

var (
	copilotTokenCache  sync.Map
	copilotExchangeMu  sync.Mutex
	copilotExchange    = make(map[string]*copilotExchangeCall)
	copilotUnsupported sync.Map
)

func (m *Manager) ensureCopilotCredential(ctx context.Context, provider store.Provider, credential store.Credential, force bool) (store.Credential, error) {
	if credential.AuthType != "oauth" {
		return credential, &Error{code: "authorization_required", permanent: true}
	}
	updated, err := m.ensureOAuthCredential(ctx, provider, credential, force)
	if err != nil {
		return credential, err
	}
	githubToken := updated.Secret
	if updated.OAuthToken != nil && updated.OAuthToken.AccessToken != "" {
		githubToken = updated.OAuthToken.AccessToken
	}
	if githubToken == "" {
		return credential, &Error{code: "authorization_required", permanent: true}
	}
	apiToken, err := m.ensureCopilotAPIToken(ctx, credential.ID, githubToken, credential.Metadata, force)
	if err != nil {
		return updated, err
	}
	endpoint, endpointErr := validateCopilotAPIEndpoint(apiToken.APIEndpoint)
	if endpointErr != nil {
		return updated, &Error{code: "oauth_configuration_invalid", permanent: true, err: endpointErr}
	}
	apiToken.APIEndpoint = endpoint
	updated.Secret = apiToken.JWT
	updated.TokenType = "Bearer"
	if updated.Metadata == nil {
		updated.Metadata = map[string]any{}
	}
	// The derived Copilot token is short-lived credential material. Keep it in
	// the process cache only; metadata_json is plaintext and must never contain
	// access tokens. Endpoint and expiry metadata remain safe to persist so the
	// adapter can route requests after a restart (the token is exchanged again).
	delete(updated.Metadata, "copilot_api_token")
	updated.Metadata["copilot_api_endpoint"] = apiToken.APIEndpoint
	updated.Metadata["copilot_token_expires_at"] = apiToken.ExpiresAt.UTC().Format(time.RFC3339Nano)
	updated.Metadata["copilot_exchange_unsupported"] = apiToken.JWT == githubToken
	if err = m.store.UpdateCredentialMetadata(ctx, credential.ID, credentialMetadataForPersistence(updated)); err != nil {
		return updated, err
	}
	return updated, nil
}

func (m *Manager) ensureCopilotAPIToken(ctx context.Context, credentialID, githubToken string, metadata map[string]any, force bool) (copilotAPIToken, error) {
	if !force {
		if cached, ok := copilotTokenCache.Load(credentialID); ok {
			entry := cached.(copilotAPIToken)
			if entry.ExpiresAt.Sub(m.now()) > copilotTokenBuffer {
				return entry, nil
			}
		}
	}
	if _, unsupported := copilotUnsupported.Load(credentialID); unsupported || metadataBool(metadata, "copilot_exchange_unsupported") {
		return copilotAPIToken{JWT: githubToken, APIEndpoint: copilotDefaultAPI, ExpiresAt: m.now().Add(time.Hour)}, nil
	}

	copilotExchangeMu.Lock()
	if existing := copilotExchange[credentialID]; existing != nil {
		copilotExchangeMu.Unlock()
		select {
		case <-existing.done:
			return existing.result, existing.err
		case <-ctx.Done():
			return copilotAPIToken{}, ctx.Err()
		}
	}
	call := &copilotExchangeCall{done: make(chan struct{})}
	copilotExchange[credentialID] = call
	copilotExchangeMu.Unlock()

	entry, err := m.exchangeCopilotToken(ctx, credentialID, githubToken)
	copilotExchangeMu.Lock()
	call.result, call.err = entry, err
	delete(copilotExchange, credentialID)
	close(call.done)
	copilotExchangeMu.Unlock()
	if err == nil {
		copilotTokenCache.Store(credentialID, entry)
	}
	return entry, err
}

func (m *Manager) exchangeCopilotToken(ctx context.Context, credentialID, githubToken string) (copilotAPIToken, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, copilotTokenURL, nil)
	if err != nil {
		return copilotAPIToken{}, err
	}
	request.Header.Set("Authorization", "token "+githubToken)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", copilotUserAgent)
	request.Header.Set("Editor-Version", copilotEditorVersion)
	request.Header.Set("Editor-Plugin-Version", copilotPluginVersion)
	request.Header.Set("x-github-api-version", copilotGitHubAPIVersion)
	response, err := m.client.Do(request)
	if err != nil {
		return copilotAPIToken{}, &Error{code: "oauth_provider_unavailable", err: err}
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode == http.StatusNotFound {
		copilotUnsupported.Store(credentialID, true)
		return copilotAPIToken{JWT: githubToken, APIEndpoint: copilotDefaultAPI, ExpiresAt: m.now().Add(time.Hour)}, nil
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return copilotAPIToken{}, &Error{code: "authorization_required", permanent: true, err: fmt.Errorf("copilot token exchange failed (%d)", response.StatusCode)}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return copilotAPIToken{}, &Error{code: "oauth_provider_unavailable", err: fmt.Errorf("copilot token exchange failed (%d)", response.StatusCode)}
	}
	var payload struct {
		Token     string `json:"token"`
		ExpiresAt int64  `json:"expires_at"`
		Endpoints struct {
			API string `json:"api"`
		} `json:"endpoints"`
	}
	if err = json.Unmarshal(body, &payload); err != nil || payload.Token == "" {
		return copilotAPIToken{}, &Error{code: "oauth_provider_unavailable", err: errors.New("invalid copilot token response")}
	}
	endpoint := strings.TrimRight(payload.Endpoints.API, "/")
	if endpoint == "" {
		endpoint = copilotDefaultAPI
	}
	if _, err := validateCopilotAPIEndpoint(endpoint); err != nil {
		return copilotAPIToken{}, &Error{code: "oauth_configuration_invalid", permanent: true, err: err}
	}
	expiresAt := m.now().Add(30 * time.Minute)
	if payload.ExpiresAt > 0 {
		expiresAt = time.Unix(payload.ExpiresAt, 0)
	}
	return copilotAPIToken{JWT: payload.Token, APIEndpoint: endpoint, ExpiresAt: expiresAt}, nil
}

// validateCopilotAPIEndpoint constrains the endpoint returned by GitHub's
// token exchange. It is later used as an HTTP base URL with a bearer token, so
// accepting an arbitrary host would turn a provider response into an SSRF and
// credential-exfiltration primitive.
func validateCopilotAPIEndpoint(raw string) (string, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(raw), "/")
	if endpoint == "" {
		endpoint = copilotDefaultAPI
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("Copilot API endpoint must be an HTTPS GitHub Copilot URL")
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return "", errors.New("Copilot API endpoint must use the HTTPS port")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host != "githubcopilot.com" && !strings.HasSuffix(host, ".githubcopilot.com") && host != "copilot-api.githubusercontent.com" {
		return "", errors.New("Copilot API endpoint is outside the githubcopilot.com domain")
	}
	return endpoint, nil
}

func metadataBool(metadata map[string]any, key string) bool {
	if metadata == nil {
		return false
	}
	switch value := metadata[key].(type) {
	case bool:
		return value
	case string:
		return value == "true" || value == "1"
	default:
		return false
	}
}
