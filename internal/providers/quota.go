package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/ninerouter"
	"github.com/tproxy/tproxy/internal/store"
)

// QuotaEntry is a normalized quota window for dashboard display.
type QuotaEntry struct {
	Name      string  `json:"name"`
	Used      float64 `json:"used"`
	Total     float64 `json:"total"`
	Remaining float64 `json:"remaining,omitempty"`
	ResetAt   string  `json:"reset_at,omitempty"`
	Unlimited bool    `json:"unlimited,omitempty"`
}

// CredentialQuota is upstream quota data for one credential.
type CredentialQuota struct {
	CredentialID string                `json:"credential_id"`
	ProviderID   string                `json:"provider_id"`
	ProviderType string                `json:"provider_type"`
	Plan         string                `json:"plan,omitempty"`
	Message      string                `json:"message,omitempty"`
	ResetCredits *CodexResetCredits    `json:"reset_credits,omitempty"`
	Quotas       map[string]QuotaEntry `json:"quotas"`
}

var quotaSupportedTypes = map[string]bool{
	"codex":              true,
	"claude":             true,
	"copilot":            true,
	"antigravity":        true,
	"glm":                true,
	"glm-cn":             true,
	"minimax":            true,
	"minimax-cn":         true,
	"vercel-ai-gateway":  true,
	"grok-cli":           true,
	"xai":                true,
	"kiro":               true,
	"qoder":              true,
	"codebuddy-cn":       true,
	"github":             true,
	"gemini-cli":         true,
	"kimi-coding":        true,
	"ollama":             true,
}

// SupportsQuota reports whether a provider type or preset ID has an upstream quota probe.
func SupportsQuota(providerType string) bool {
	key := strings.ToLower(strings.TrimSpace(providerType))
	if quotaSupportedTypes[key] {
		return true
	}
	if preset, ok := ninerouter.Lookup(key); ok && preset.SupportsQuota {
		return true
	}
	return false
}

// CredentialQuota fetches provider-specific quota limits for a credential.
func (r *Registry) CredentialQuota(ctx context.Context, provider store.Provider, credential store.Credential) (CredentialQuota, error) {
	result := CredentialQuota{
		CredentialID: credential.ID,
		ProviderID:   provider.ID,
		ProviderType: provider.Type,
		Quotas:       map[string]QuotaEntry{},
	}
	if credential.ID == "" || credential.Secret == "" {
		return result, &ProviderError{Code: "authorization_required", Message: "credential has no access token", Status: http.StatusUnauthorized}
	}
	if quota, ok := r.credentialQuotaByPreset(ctx, provider, credential); ok {
		return quota, nil
	}
	if !SupportsQuota(provider.Type) {
		result.Message = fmt.Sprintf("Upstream quota API is not implemented for %s", provider.Type)
		return result, nil
	}
	ctx = withCredentialProxy(ctx, credential)
	switch provider.Type {
	case "codex":
		return r.codexQuota(ctx, provider, credential)
	case "claude":
		return r.claudeQuota(ctx, credential)
	case "copilot":
		return r.copilotQuota(ctx, credential)
	case "antigravity":
		return r.antigravityQuota(ctx, credential)
	default:
		result.Message = fmt.Sprintf("Upstream quota API is not implemented for %s", provider.Type)
		return result, nil
	}
}

func (r *Registry) codexQuota(ctx context.Context, provider store.Provider, credential store.Credential) (CredentialQuota, error) {
	result := CredentialQuota{
		CredentialID: credential.ID,
		ProviderID:   provider.ID,
		ProviderType: provider.Type,
		Quotas:       map[string]QuotaEntry{},
	}
	body, status, err := r.quotaGET(ctx, "https://chatgpt.com/backend-api/wham/usage", codexHeaders(provider, credential, false, canonical.Request{}))
	if err != nil {
		return result, err
	}
	if status < 200 || status >= 300 {
		result.Message = fmt.Sprintf("Codex usage API unavailable (HTTP %d)", status)
		return result, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return result, &ProviderError{Code: "quota_parse_failed", Message: "invalid Codex usage response", Err: err}
	}
	result.Plan = stringValue(firstValue(payload, "plan_type", "plan"))
	if summary, ok := payload["summary"].(map[string]any); ok && result.Plan == "" {
		result.Plan = stringValue(summary["plan"])
	}
	normal := firstValue(payload, "rate_limit", "rate_limits")
	if snapshot, ok := normal.(map[string]any); ok {
		appendCodexWindows(result.Quotas, "", snapshot)
	}
	if byID, ok := payload["rate_limits_by_limit_id"].(map[string]any); ok {
		if codexLimit, ok := byID["codex"].(map[string]any); ok {
			appendCodexWindows(result.Quotas, "", codexLimit)
		}
	}
	if review := codexReviewRateLimit(payload); review != nil {
		appendCodexWindows(result.Quotas, "review", review)
	}
	result.ResetCredits = codexResetCreditsFromUsage(payload)
	if len(result.Quotas) == 0 {
		result.Message = "Codex connected. No quota windows returned."
	}
	return result, nil
}

func (r *Registry) claudeQuota(ctx context.Context, credential store.Credential) (CredentialQuota, error) {
	result := CredentialQuota{
		CredentialID: credential.ID,
		ProviderID:   credential.ProviderID,
		ProviderType: "claude",
		Quotas:       map[string]QuotaEntry{},
	}
	headers := authHeaders(store.Provider{Type: "claude"}, credential)
	headers.Set("anthropic-version", "2023-06-01")
	body, status, err := r.quotaGET(ctx, "https://api.anthropic.com/api/oauth/usage", headers)
	if err != nil {
		return result, err
	}
	if status == http.StatusOK {
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err == nil {
			for key, raw := range payload {
				if item, ok := raw.(map[string]any); ok {
					result.Quotas[key] = quotaFromUsedTotal(item)
				}
			}
			if len(result.Quotas) > 0 {
				return result, nil
			}
		}
	}
	result.Message = "Claude connected. Usage is tracked per request."
	return result, nil
}

func (r *Registry) copilotQuota(ctx context.Context, credential store.Credential) (CredentialQuota, error) {
	result := CredentialQuota{
		CredentialID: credential.ID,
		ProviderID:   credential.ProviderID,
		ProviderType: "copilot",
		Quotas:       map[string]QuotaEntry{},
	}
	headers := http.Header{
		"Authorization":           {"token " + credential.Secret},
		"Accept":                  {"application/json"},
		"X-GitHub-Api-Version":    {"2023-09-01"},
		"User-Agent":              {"tproxy-quota/1.0"},
		"Editor-Version":          {"vscode/1.100.0"},
		"Editor-Plugin-Version":   {"copilot-chat/0.26.7"},
	}
	body, status, err := r.quotaGET(ctx, "https://api.github.com/copilot_internal/user", headers)
	if err != nil {
		return result, err
	}
	if status < 200 || status >= 300 {
		result.Message = fmt.Sprintf("Copilot usage API unavailable (HTTP %d)", status)
		return result, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return result, &ProviderError{Code: "quota_parse_failed", Message: "invalid Copilot usage response", Err: err}
	}
	result.Plan = stringValue(firstValue(payload, "copilot_plan", "access_type_sku"))
	if snapshots, ok := payload["quota_snapshots"].(map[string]any); ok {
		resetAt := parseResetAt(payload["quota_reset_date"])
		for name, raw := range snapshots {
			item, _ := raw.(map[string]any)
			result.Quotas[name] = githubQuotaEntry(item, resetAt)
		}
	} else if monthly, ok := payload["monthly_quotas"].(map[string]any); ok {
		used := map[string]any{}
		if limited, ok := payload["limited_user_quotas"].(map[string]any); ok {
			used = limited
		}
		resetAt := parseResetAt(firstValue(payload, "limited_user_reset_date", "quota_reset_date"))
		for name, raw := range monthly {
			total := float64(numberValue(raw))
			usedCount := float64(numberValue(used[name]))
			result.Quotas[name] = QuotaEntry{
				Name:      name,
				Used:      usedCount,
				Total:     total,
				Remaining: total - usedCount,
				ResetAt:   resetAt,
			}
		}
	}
	if len(result.Quotas) == 0 {
		result.Message = "Copilot connected. Unable to parse quota data."
	}
	return result, nil
}

func (r *Registry) quotaGET(ctx context.Context, target string, headers http.Header) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, &ProviderError{Code: "quota_request_failed", Err: err}
	}
	req.Header = headers.Clone()
	response, err := r.client.Do(req)
	if err != nil {
		return nil, 0, &ProviderError{Code: "quota_request_failed", Err: err}
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if readErr != nil {
		return nil, response.StatusCode, &ProviderError{Code: "quota_request_failed", Err: readErr}
	}
	return body, response.StatusCode, nil
}

func appendCodexWindows(quotas map[string]QuotaEntry, prefix string, snapshot map[string]any) {
	rateLimit := snapshot
	if nested, ok := snapshot["rate_limit"].(map[string]any); ok {
		rateLimit = nested
	}
	primary := firstMap(rateLimit, "primary_window", "primary")
	secondary := firstMap(rateLimit, "secondary_window", "secondary")
	if primary != nil {
		name := "session"
		if prefix != "" {
			name = prefix + "_session"
		}
		quotas[name] = codexWindowEntry(name, primary)
	}
	if secondary != nil {
		name := "weekly"
		if prefix != "" {
			name = prefix + "_weekly"
		}
		quotas[name] = codexWindowEntry(name, secondary)
	}
}

func codexReviewRateLimit(payload map[string]any) map[string]any {
	if item, ok := payload["code_review_rate_limit"].(map[string]any); ok {
		return item
	}
	if item, ok := payload["review_rate_limit"].(map[string]any); ok {
		return item
	}
	if byID, ok := payload["rate_limits_by_limit_id"].(map[string]any); ok {
		for _, key := range []string{"code_review", "codex_review", "review"} {
			if item, ok := byID[key].(map[string]any); ok {
				return item
			}
		}
	}
	additional, _ := payload["additional_rate_limits"].([]any)
	for _, raw := range additional {
		item, _ := raw.(map[string]any)
		id := strings.ToLower(stringValue(firstValue(item, "limit_name", "metered_feature", "id")))
		if strings.Contains(id, "review") {
			return item
		}
	}
	return nil
}

func codexWindowEntry(name string, window map[string]any) QuotaEntry {
	used := float64(numberValue(firstValue(window, "used_percent", "percent_used")))
	if used < 0 {
		used = 0
	}
	if used > 100 {
		used = 100
	}
	return QuotaEntry{
		Name:      name,
		Used:      used,
		Total:     100,
		Remaining: 100 - used,
		ResetAt:   parseResetAt(firstValue(window, "reset_at", "resets_at", "resetAt")),
	}
}

func githubQuotaEntry(item map[string]any, resetAt string) QuotaEntry {
	entitlement := float64(numberValue(item["entitlement"]))
	remaining := float64(numberValue(item["remaining"]))
	unlimited := item["unlimited"] == true
	used := entitlement - remaining
	if unlimited {
		return QuotaEntry{Name: "", Used: used, Total: 0, Unlimited: true, ResetAt: resetAt}
	}
	return QuotaEntry{Name: "", Used: used, Total: entitlement, Remaining: remaining, ResetAt: resetAt}
}

func quotaFromUsedTotal(item map[string]any) QuotaEntry {
	used := float64(numberValue(firstValue(item, "used", "used_percent")))
	total := float64(numberValue(firstValue(item, "total", "limit", "max")))
	return QuotaEntry{Used: used, Total: total, Remaining: total - used, ResetAt: parseResetAt(item["reset_at"])}
}

func firstMap(values map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if item, ok := values[key].(map[string]any); ok {
			return item
		}
	}
	return nil
}

func parseResetAt(value any) string {
	switch typed := value.(type) {
	case string:
		if typed == "" {
			return ""
		}
		if parsed, err := time.Parse(time.RFC3339, typed); err == nil {
			return parsed.UTC().Format(time.RFC3339)
		}
		return typed
	case float64:
		if typed > 1_000_000_000_000 {
			return time.UnixMilli(int64(typed)).UTC().Format(time.RFC3339)
		}
		return time.Unix(int64(typed), 0).UTC().Format(time.RFC3339)
	default:
		return ""
	}
}
