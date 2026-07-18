package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/tproxy/tproxy/internal/store"
)

const antigravityQuotaUserAgent = "antigravity/hub/2.2.1 darwin/arm64"

var antigravityImportantModels = []string{
	"gemini-3-flash-agent",
	"gemini-3.5-flash-low",
	"gemini-3.5-flash-extra-low",
	"gemini-pro-agent",
	"gemini-3.1-pro-low",
	"claude-sonnet-4-6",
	"claude-opus-4-6-thinking",
	"gpt-oss-120b-medium",
	"gemini-3-flash",
	"gemini-3.1-flash-image",
	"gemini-3-pro-image",
}

func (r *Registry) antigravityQuota(ctx context.Context, credential store.Credential) (CredentialQuota, error) {
	result := CredentialQuota{
		CredentialID: credential.ID,
		ProviderID:   credential.ProviderID,
		ProviderType: "antigravity",
		Quotas:       map[string]QuotaEntry{},
	}
	token := credentialAccessToken(credential)
	if token == "" {
		result.Message = "Antigravity access token not available."
		return result, nil
	}
	subscription, _ := r.antigravitySubscription(ctx, token)
	projectID := ""
	if subscription != nil {
		projectID = stringValue(firstValue(subscription, "cloudaicompanionProject", "cloudaicompanion_project"))
		result.Plan = stringValue(firstValue(subscription, "currentTier", "plan"))
		if tier, ok := subscription["currentTier"].(map[string]any); ok {
			if name := stringValue(tier["name"]); name != "" {
				result.Plan = name
			}
		}
	}
	body := map[string]any{}
	if projectID != "" {
		body["project"] = projectID
	}
	payload, status, err := r.quotaPOST(ctx, "https://cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels", antigravityQuotaHeaders(token), body)
	if err != nil {
		result.Message = err.Error()
		return result, nil
	}
	if status == http.StatusForbidden {
		result.Message = "Antigravity quota API access forbidden. Chat may still work."
		return result, nil
	}
	if status == http.StatusUnauthorized {
		result.Message = "Antigravity quota API authentication expired. Chat may still work."
		return result, nil
	}
	if status < 200 || status >= 300 {
		result.Message = fmt.Sprintf("Antigravity quota API unavailable (HTTP %d)", status)
		return result, nil
	}
	var data map[string]any
	if err := json.Unmarshal(payload, &data); err != nil {
		result.Message = "Invalid Antigravity quota response"
		return result, nil
	}
	models, _ := data["models"].(map[string]any)
	for _, modelKey := range antigravityImportantModels {
		raw, ok := models[modelKey].(map[string]any)
		if !ok {
			continue
		}
		if isInternal, _ := raw["isInternal"].(bool); isInternal {
			continue
		}
		info, _ := raw["quotaInfo"].(map[string]any)
		if info == nil {
			continue
		}
		remainingFraction := float64(numberValue(info["remainingFraction"]))
		total := 1000.0
		remaining := remainingFraction * total
		used := total - remaining
		name := stringValue(firstValue(raw, "displayName", "display_name"))
		if name == "" {
			name = modelKey
		}
		result.Quotas[modelKey] = QuotaEntry{
			Name:      name,
			Used:      used,
			Total:     total,
			Remaining: remaining,
			ResetAt:   parseResetAt(firstValue(info, "resetTime", "reset_at")),
		}
	}
	if len(result.Quotas) == 0 {
		result.Message = "Antigravity connected. No quota windows returned."
	}
	return result, nil
}

func (r *Registry) geminiCLIQuota(ctx context.Context, credential store.Credential) CredentialQuota {
	result := CredentialQuota{
		CredentialID: credential.ID,
		ProviderID:   credential.ProviderID,
		ProviderType: "antigravity",
		Quotas:       map[string]QuotaEntry{},
		Plan:         "Free",
	}
	token := credentialAccessToken(credential)
	if token == "" {
		result.Message = "Gemini CLI access token not available."
		return result
	}
	projectID := antigravityProject(credential)
	plan := result.Plan
	if projectID == "" {
		if sub, _ := r.geminiCLISubscription(ctx, token); sub != nil {
			projectID = stringValue(firstValue(sub, "cloudaicompanionProject", "cloudaicompanion_project"))
			if tier, ok := sub["currentTier"].(map[string]any); ok {
				if name := stringValue(tier["name"]); name != "" {
					plan = name
				}
			}
		}
	}
	result.Plan = plan
	if projectID == "" {
		result.Message = "Gemini CLI project ID not available. Reconnect the OAuth account."
		return result
	}
	payload, status, err := r.quotaPOST(ctx, "https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota", geminiQuotaHeaders(token), map[string]any{"project": projectID})
	if err != nil {
		result.Message = err.Error()
		return result
	}
	if status < 200 || status >= 300 {
		result.Message = fmt.Sprintf("Gemini CLI quota API unavailable (HTTP %d)", status)
		return result
	}
	var data map[string]any
	if err := json.Unmarshal(payload, &data); err != nil {
		result.Message = "Invalid Gemini CLI quota response"
		return result
	}
	buckets, _ := data["buckets"].([]any)
	for _, raw := range buckets {
		bucket, _ := raw.(map[string]any)
		if bucket == nil {
			continue
		}
		modelID := stringValue(bucket["modelId"])
		if modelID == "" || bucket["remainingFraction"] == nil {
			continue
		}
		remainingFraction := float64(numberValue(bucket["remainingFraction"]))
		total := 1000.0
		remaining := remainingFraction * total
		used := total - remaining
		result.Quotas[modelID] = QuotaEntry{
			Name:      modelID,
			Used:      used,
			Total:     total,
			Remaining: remaining,
			ResetAt:   parseResetAt(bucket["resetTime"]),
		}
	}
	if len(result.Quotas) == 0 {
		result.Message = "Gemini CLI connected. No quota windows returned."
	}
	return result
}

func (r *Registry) antigravitySubscription(ctx context.Context, token string) (map[string]any, error) {
	payload, status, err := r.quotaPOST(ctx, "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist", antigravityQuotaHeaders(token), map[string]any{
		"metadata": map[string]any{"ideType": "ANTIGRAVITY", "platform": "PLATFORM_UNSPECIFIED", "pluginType": "GEMINI"},
		"mode":     1,
	})
	if err != nil || status < 200 || status >= 300 {
		return nil, err
	}
	var data map[string]any
	if json.Unmarshal(payload, &data) != nil {
		return nil, fmt.Errorf("invalid subscription response")
	}
	return data, nil
}

func (r *Registry) geminiCLISubscription(ctx context.Context, token string) (map[string]any, error) {
	payload, status, err := r.quotaPOST(ctx, "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist", geminiQuotaHeaders(token), map[string]any{
		"metadata": map[string]any{"ideType": "IDE_UNSPECIFIED", "platform": "PLATFORM_UNSPECIFIED", "pluginType": "GEMINI"},
	})
	if err != nil || status < 200 || status >= 300 {
		return nil, err
	}
	var data map[string]any
	if json.Unmarshal(payload, &data) != nil {
		return nil, fmt.Errorf("invalid subscription response")
	}
	return data, nil
}

func antigravityQuotaHeaders(token string) http.Header {
	return http.Header{
		"Authorization":   {"Bearer " + token},
		"Content-Type":    {"application/json"},
		"User-Agent":      {antigravityQuotaUserAgent},
		"X-Client-Name":   {"antigravity"},
		"X-Client-Version": {"2.2.1"},
	}
}

func geminiQuotaHeaders(token string) http.Header {
	return http.Header{
		"Authorization": {"Bearer " + token},
		"Content-Type":  {"application/json"},
	}
}

func credentialAccessToken(credential store.Credential) string {
	if credential.OAuthToken != nil && credential.OAuthToken.AccessToken != "" {
		return credential.OAuthToken.AccessToken
	}
	return credential.Secret
}

func (r *Registry) quotaPOST(ctx context.Context, target string, headers http.Header, body map[string]any) ([]byte, int, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, 0, &ProviderError{Code: "quota_request_failed", Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(string(encoded)))
	if err != nil {
		return nil, 0, &ProviderError{Code: "quota_request_failed", Err: err}
	}
	req.Header = headers.Clone()
	response, err := r.client.Do(req)
	if err != nil {
		return nil, 0, &ProviderError{Code: "quota_request_failed", Err: err}
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if readErr != nil {
		return nil, response.StatusCode, &ProviderError{Code: "quota_request_failed", Err: readErr}
	}
	return data, response.StatusCode, nil
}
