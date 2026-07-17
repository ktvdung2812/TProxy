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

	"github.com/tproxy/tproxy/internal/store"
)

const (
	antigravityDefaultBaseURL   = "https://cloudcode-pa.googleapis.com"
	antigravityDailyBaseURL     = "https://daily-cloudcode-pa.googleapis.com"
	antigravityUserAgent        = "antigravity/hub/2.2.1 darwin/arm64"
	antigravityOnboardUserAgent = antigravityUserAgent + " google-api-nodejs-client/10.3.0"
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
	projectID := stringValue(token.Extra["project_id"])
	if projectID == "" {
		projectID = stringValue(token.Extra["projectId"])
	}
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
	token.Extra["project_id"] = projectID
	return token, email, nil
}

func (m *Manager) fetchAntigravityEmail(ctx context.Context, target, accessToken string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", antigravityUserAgent)
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
	baseURL := strings.TrimRight(provider.BaseURL, "/")
	if baseURL == "" {
		baseURL = antigravityDefaultBaseURL
	}
	payload, err := m.antigravityJSON(ctx, baseURL+"/v1internal:loadCodeAssist", accessToken, antigravityUserAgent, map[string]any{
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
			"ide_version": "2.2.1",
			"ide_name":    "antigravity",
		},
	}
	for attempt := 0; attempt < 5; attempt++ {
		payload, err = m.antigravityJSON(ctx, onboardBaseURL+"/v1internal:onboardUser", accessToken, antigravityOnboardUserAgent, requestBody, true)
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
	for _, key := range []string{"cloudaicompanionProject", "projectId", "project", "project_id"} {
		switch value := payload[key].(type) {
		case string:
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		case map[string]any:
			if id := strings.TrimSpace(stringValue(value["id"])); id != "" {
				return id
			}
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
