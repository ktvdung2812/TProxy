package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/antigravity"
	"github.com/tproxy/tproxy/internal/store"
)

const (
	antigravityDefaultBaseURL = "https://cloudcode-pa.googleapis.com"
	antigravityDailyBaseURL   = "https://daily-cloudcode-pa.googleapis.com"
	antigravityOnboardTimeout = 30 * time.Second
)

func (m *Manager) prepareProviderToken(ctx context.Context, provider store.Provider, token store.OAuthToken, email string) (store.OAuthToken, string, error) {
	if token.Extra == nil {
		token.Extra = make(map[string]any)
	}
	if email == "" {
		email = stringValue(token.Extra["email"])
	}
	if provider.Type == "antigravity" {
		var err error
		token, email, err = m.enrichAntigravityToken(ctx, provider, token, email)
		if err != nil {
			return token, email, err
		}
	}
	if provider.Type == "kiro" {
		var err error
		token, email, err = m.enrichKiroToken(ctx, token, email)
		if err != nil {
			return token, email, err
		}
	}
	if isClineProvider(provider.Type) {
		var err error
		token, email, err = m.enrichClineToken(ctx, token, email)
		if err != nil {
			return token, email, err
		}
	}
	if email != "" {
		token.Extra["email"] = email
	}
	if len(token.Extra) == 0 {
		token.Extra = nil
	}
	return token, email, nil
}

func mergeTokenExtra(token *store.OAuthToken, old store.OAuthToken) {
	if token.Extra == nil {
		token.Extra = make(map[string]any)
	}
	for key, value := range old.Extra {
		if _, exists := token.Extra[key]; !exists {
			token.Extra[key] = value
		}
	}
}

func (m *Manager) enrichAntigravityToken(ctx context.Context, provider store.Provider, token store.OAuthToken, email string) (store.OAuthToken, string, error) {
	if token.AccessToken == "" {
		return token, email, &Error{code: "authorization_required", permanent: true}
	}
	if email == "" && provider.OAuth != nil && provider.OAuth.UserInfoURL != "" {
		if discovered, err := m.fetchAntigravityEmail(ctx, provider.OAuth.UserInfoURL, token.AccessToken); err == nil {
			email = discovered
		}
	}
	projectID := antigravityProjectFromExtra(token.Extra)
	if projectID == "" {
		var err error
		projectID, err = m.fetchAntigravityProjectID(ctx, provider, token.AccessToken)
		if err != nil {
			return token, email, &Error{code: "oauth_provider_unavailable", err: err}
		}
	}
	if projectID == "" {
		return token, email, &Error{code: "oauth_provider_unavailable", err: errors.New("Antigravity project ID is unavailable")}
	}
	token = antigravityTokenWithProjectID(token, projectID)
	return token, email, nil
}

// ensureAntigravityProject recovers metadata missing from older imports even
// when their access token is still fresh. A Cloud Code request cannot proceed
// without a project ID, so recover it before returning a credential to the
// router and persist the canonical project_id for subsequent requests.
func (m *Manager) ensureAntigravityProject(ctx context.Context, provider store.Provider, credential store.Credential) (store.Credential, error) {
	if provider.Type != "antigravity" || credential.AuthType != "oauth" || credential.OAuthToken == nil {
		return credential, nil
	}
	if projectID := antigravityProjectFromExtra(credential.OAuthToken.Extra); projectID != "" {
		if antigravityProjectValue(credential.OAuthToken.Extra["project_id"]) == "" {
			return m.persistAntigravityProject(ctx, credential, antigravityTokenWithProjectID(*credential.OAuthToken, projectID))
		}
		return credential, nil
	}
	if credential.ID == "" {
		return credential, &Error{code: "oauth_provider_unavailable", err: errors.New("Antigravity credential ID is unavailable for project recovery")}
	}

	m.mu.Lock()
	if existing := m.projects[credential.ID]; existing != nil {
		m.mu.Unlock()
		select {
		case <-existing.done:
			return existing.credential, existing.err
		case <-ctx.Done():
			return credential, ctx.Err()
		}
	}
	call := &antigravityProjectCall{done: make(chan struct{})}
	m.projects[credential.ID] = call
	m.mu.Unlock()

	updated, err := m.runAntigravityProjectRecovery(ctx, provider, credential, call)
	return updated, err
}

func (m *Manager) runAntigravityProjectRecovery(ctx context.Context, provider store.Provider, credential store.Credential, call *antigravityProjectCall) (updated store.Credential, err error) {
	updated = credential
	defer func() {
		if recover() != nil {
			err = errors.New("Antigravity project recovery failed")
		}
		m.mu.Lock()
		call.credential, call.err = updated, err
		delete(m.projects, credential.ID)
		close(call.done)
		m.mu.Unlock()
	}()
	return m.recoverAntigravityProject(ctx, provider, credential)
}

// prepareAntigravityCredential repairs imported metadata before the normal
// provider token preparation path. It avoids a control-plane call when the
// project ID already lives in non-secret credential metadata, while keeping
// the canonical value inside the encrypted OAuth token envelope.
func (m *Manager) prepareAntigravityCredential(ctx context.Context, provider store.Provider, credential store.Credential) (store.Credential, error) {
	if provider.Type != "antigravity" || credential.AuthType != "oauth" || credential.OAuthToken == nil || antigravityProjectFromExtra(credential.OAuthToken.Extra) != "" {
		return credential, nil
	}
	projectID := antigravityProjectFromExtra(credential.Metadata)
	if projectID == "" {
		return credential, nil
	}
	token := antigravityTokenWithProjectID(*credential.OAuthToken, projectID)
	credential.OAuthToken = &token
	return m.persistAntigravityProject(ctx, credential, token)
}

func (m *Manager) recoverAntigravityProject(ctx context.Context, provider store.Provider, credential store.Credential) (store.Credential, error) {
	if credential.OAuthToken == nil || strings.TrimSpace(credential.OAuthToken.AccessToken) == "" {
		_ = m.store.MarkCredentialAuthRequired(ctx, credential.ID, "authorization_required")
		return credential, &Error{code: "authorization_required", permanent: true}
	}
	token := *credential.OAuthToken
	if token.Extra == nil {
		token.Extra = make(map[string]any)
	}
	projectID, err := m.fetchAntigravityProjectID(ctx, provider, token.AccessToken)
	if err != nil {
		return credential, &Error{code: "oauth_provider_unavailable", err: err}
	}
	if projectID == "" {
		return credential, &Error{code: "oauth_provider_unavailable", err: errors.New("Antigravity project ID is unavailable")}
	}
	token = antigravityTokenWithProjectID(token, projectID)
	return m.persistAntigravityProject(ctx, credential, token)
}

func antigravityTokenWithProjectID(token store.OAuthToken, projectID string) store.OAuthToken {
	projectID = strings.TrimSpace(projectID)
	extra := make(map[string]any, len(token.Extra)+1)
	for key, value := range token.Extra {
		extra[key] = value
	}
	extra["project_id"] = projectID
	token.Extra = extra
	return token
}

func (m *Manager) persistAntigravityProject(ctx context.Context, credential store.Credential, token store.OAuthToken) (store.Credential, error) {
	if err := m.store.UpdateOAuthToken(ctx, credential.ID, token); err != nil {
		return credential, err
	}
	credential.Secret = token.AccessToken
	credential.TokenType = token.TokenType
	credential.OAuthToken = &token
	credential.Status = "healthy"
	credential.CooldownUntil = time.Time{}
	credential.LastErrorCode = ""
	credential.LastError = ""
	return credential, nil
}

func antigravityProjectFromExtra(extra map[string]any) string {
	if extra == nil {
		return ""
	}
	for _, key := range []string{"project_id", "projectId", "cloudaicompanionProject", "cloudaicompanion_project", "project"} {
		if projectID := antigravityProjectValue(extra[key]); projectID != "" {
			return projectID
		}
	}
	return ""
}

func antigravityProjectValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		for _, key := range []string{"id", "project_id", "projectId", "cloudaicompanionProject", "cloudaicompanion_project"} {
			if projectID := antigravityProjectValue(typed[key]); projectID != "" {
				return projectID
			}
		}
	}
	return ""
}

func (m *Manager) fetchAntigravityEmail(ctx context.Context, target, accessToken string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", antigravity.UserAgent())
	response, err := m.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", errors.New("Antigravity user information request failed")
	}
	var payload map[string]any
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return "", err
	}
	return strings.TrimSpace(stringValue(payload["email"])), nil
}

func (m *Manager) fetchAntigravityProjectID(ctx context.Context, provider store.Provider, accessToken string) (string, error) {
	baseURL := antigravityBaseURL(provider.BaseURL)
	if baseURL == "" {
		baseURL = antigravityDefaultBaseURL
	}
	payload, err := m.antigravityJSON(ctx, baseURL+"/v1internal:loadCodeAssist", accessToken, antigravity.UserAgent(), map[string]any{
		"metadata": map[string]any{"ideType": "ANTIGRAVITY"},
	}, false)
	if err != nil {
		return "", err
	}
	if projectID := antigravityProjectID(payload); projectID != "" {
		return projectID, nil
	}
	tierID := antigravityDefaultTier(payload)
	onboardBaseURL := baseURL
	if baseURL == antigravityDefaultBaseURL {
		onboardBaseURL = antigravityDailyBaseURL
	}
	requestBody := map[string]any{
		"tier_id": tierID,
		"metadata": map[string]any{
			"ide_type":    "ANTIGRAVITY",
			"ide_version": antigravity.Version(),
			"ide_name":    "antigravity",
		},
	}
	for attempt := 0; attempt < 5; attempt++ {
		// An injected HTTP client is allowed to have no timeout. Bound each
		// onboarding poll nevertheless so an unavailable control plane cannot
		// permanently pin an OAuth completion or credential-recovery request.
		attemptCtx, cancel := context.WithTimeout(ctx, antigravityOnboardTimeout)
		payload, err = m.antigravityJSON(attemptCtx, onboardBaseURL+"/v1internal:onboardUser", accessToken, antigravity.OnboardUserAgent(), requestBody, true)
		cancel()
		if err != nil {
			return "", err
		}
		if projectID := antigravityProjectID(payload); projectID != "" {
			return projectID, nil
		}
		if done, _ := payload["done"].(bool); done {
			return "", errors.New("Antigravity onboarding completed without a project ID")
		}
		if err = waitForContext(ctx, 2*time.Second); err != nil {
			return "", err
		}
	}
	return "", errors.New("Antigravity onboarding did not complete")
}

// antigravityBaseURL accepts both the Cloud Code origin and legacy presets
// ending in /v1internal. Cloud Code actions are written as
// /v1internal:<action>, so retaining that suffix would create an invalid
// /v1internal/v1internal:<action> URL.
func antigravityBaseURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(strings.ToLower(baseURL), "/v1internal") {
		baseURL = strings.TrimRight(baseURL[:len(baseURL)-len("/v1internal")], "/")
	}
	return baseURL
}

func (m *Manager) antigravityJSON(ctx context.Context, target, accessToken, userAgent string, body map[string]any, controlPlane bool) (map[string]any, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", userAgent)
	if controlPlane {
		request.Header.Set("X-Goog-Api-Client", "gl-node/22.21.1")
	}
	response, err := m.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, errors.New("Antigravity control-plane request failed")
	}
	var payload map[string]any
	if err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func antigravityProjectID(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	for _, key := range []string{"cloudaicompanionProject", "cloudaicompanion_project", "projectId", "project", "project_id"} {
		if projectID := antigravityProjectValue(payload[key]); projectID != "" {
			return projectID
		}
	}
	for _, key := range []string{"response", "result"} {
		if nested, ok := payload[key].(map[string]any); ok {
			if projectID := antigravityProjectID(nested); projectID != "" {
				return projectID
			}
		}
	}
	return ""
}

func antigravityDefaultTier(payload map[string]any) string {
	if tiers, ok := payload["allowedTiers"].([]any); ok {
		for _, item := range tiers {
			tier, _ := item.(map[string]any)
			isDefault, _ := tier["isDefault"].(bool)
			if isDefault {
				if id := strings.TrimSpace(stringValue(tier["id"])); id != "" {
					return id
				}
			}
		}
	}
	if tier, ok := payload["currentTier"].(map[string]any); ok {
		if id := strings.TrimSpace(stringValue(tier["id"])); id != "" {
			return id
		}
	}
	return "free-tier"
}

func waitForContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
