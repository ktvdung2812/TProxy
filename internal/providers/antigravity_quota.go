package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/tproxy/tproxy/internal/antigravity"
	"github.com/tproxy/tproxy/internal/store"
)

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
	// The OAuth token is the canonical source for the project selected during
	// enrollment. loadCodeAssist is useful for the plan name and as a legacy
	// fallback, but it can temporarily omit the project even for a healthy
	// credential.
	projectID := antigravityProject(credential)
	subscription, _ := r.antigravitySubscription(ctx, token)
	if subscription != nil {
		if projectID == "" {
			projectID = antigravityProjectFromValues(subscription)
		}
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
		name := stringValue(firstValue(raw, "displayName", "display_name"))
		if name == "" {
			name = modelKey
		}
		entry, ok := antigravityQuotaEntry(name, info["remainingFraction"], firstValue(info, "resetTime", "reset_at"))
		if !ok {
			continue
		}
		result.Quotas[modelKey] = entry
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
			projectID = antigravityProjectFromValues(sub)
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
		entry, ok := antigravityQuotaEntry(modelID, bucket["remainingFraction"], bucket["resetTime"])
		if !ok {
			continue
		}
		result.Quotas[modelID] = entry
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
		"Authorization":    {"Bearer " + token},
		"Content-Type":     {"application/json"},
		"User-Agent":       {antigravity.UserAgent()},
		"X-Client-Name":    {"antigravity"},
		"X-Client-Version": {antigravity.Version()},
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

// antigravityQuotaEntry preserves Antigravity's normalized 1,000-unit display
// counts while keeping Remaining in the percentage unit used by the dashboard
// and routing state. Cloud Code may encode remainingFraction as either a JSON
// number or a numeric string.
func antigravityQuotaEntry(name string, rawFraction, resetAt any) (QuotaEntry, bool) {
	fraction, ok := antigravityRemainingFraction(rawFraction)
	if !ok {
		return QuotaEntry{}, false
	}
	const total = 1000.0
	remainingUnits := fraction * total
	return QuotaEntry{
		Name:      name,
		Used:      total - remainingUnits,
		Total:     total,
		Remaining: fraction * 100,
		ResetAt:   parseResetAt(resetAt),
	}, true
}

func antigravityRemainingFraction(value any) (float64, bool) {
	var fraction float64
	switch typed := value.(type) {
	case float64:
		fraction = typed
	case float32:
		fraction = float64(typed)
	case int:
		fraction = float64(typed)
	case int64:
		fraction = float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		fraction = parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, false
		}
		fraction = parsed
	default:
		return 0, false
	}
	if math.IsNaN(fraction) || math.IsInf(fraction, 0) || fraction < 0 || fraction > 1 {
		return 0, false
	}
	return fraction, true
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
