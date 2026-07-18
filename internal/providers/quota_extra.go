package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/store"
)

func (r *Registry) credentialQuotaByPreset(ctx context.Context, provider store.Provider, credential store.Credential) (CredentialQuota, bool) {
	switch strings.ToLower(strings.TrimSpace(provider.ID)) {
	case "glm", "glm-cn":
		return r.glmQuota(ctx, provider, credential), true
	case "minimax", "minimax-cn":
		return r.minimaxQuota(ctx, provider, credential), true
	case "vercel-ai-gateway":
		return r.vercelGatewayQuota(ctx, credential), true
	case "grok-cli", "xai":
		return r.grokCLIQuota(ctx, credential), true
	case "kiro":
		return r.kiroQuota(ctx, credential), true
	case "qoder":
		return r.qoderQuota(ctx, credential), true
	case "codebuddy-cn":
		return r.codebuddyCNQuota(ctx, credential), true
	case "gemini-cli":
		return r.geminiCLIQuota(ctx, credential), true
	case "github":
		quota, err := r.copilotQuota(ctx, credential)
		if err != nil && quota.Message == "" {
			quota.Message = err.Error()
		}
		return quota, true
	default:
		return CredentialQuota{}, false
	}
}

func (r *Registry) glmQuota(ctx context.Context, provider store.Provider, credential store.Credential) CredentialQuota {
	result := CredentialQuota{
		CredentialID: credential.ID,
		ProviderID:   provider.ID,
		ProviderType: provider.Type,
		Quotas:       map[string]QuotaEntry{},
	}
	quotaURL := "https://api.z.ai/api/monitor/usage/quota/limit"
	if provider.ID == "glm-cn" {
		quotaURL = "https://open.bigmodel.cn/api/monitor/usage/quota/limit"
	}
	headers := http.Header{
		"Authorization": {"Bearer " + credential.Secret},
		"Accept":          {"application/json"},
	}
	body, status, err := r.quotaGET(ctx, quotaURL, headers)
	if err != nil {
		result.Message = err.Error()
		return result
	}
	if status < 200 || status >= 300 {
		result.Message = fmt.Sprintf("GLM quota API unavailable (HTTP %d)", status)
		return result
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		result.Message = "Invalid GLM quota response"
		return result
	}
	data, _ := payload["data"].(map[string]any)
	limits, _ := data["limits"].([]any)
	for _, raw := range limits {
		item, _ := raw.(map[string]any)
		if item == nil || stringValue(item["type"]) != "TOKENS_LIMIT" {
			continue
		}
		usedPercent := float64(numberValue(item["percentage"]))
		resetAt := ""
		if resetMS := int64(numberValue(item["nextResetTime"])); resetMS > 0 {
			resetAt = millisToRFC3339(resetMS)
		}
		remaining := float64(max(0, 100-int(usedPercent)))
		result.Quotas["session"] = QuotaEntry{
			Name:      "Session",
			Used:      usedPercent,
			Total:     100,
			Remaining: remaining,
			ResetAt:   resetAt,
		}
		break
	}
	if level := stringValue(data["level"]); level != "" {
		result.Plan = level
	}
	if len(result.Quotas) == 0 {
		result.Message = "GLM connected. No quota windows returned."
	}
	return result
}

func (r *Registry) minimaxQuota(ctx context.Context, provider store.Provider, credential store.Credential) CredentialQuota {
	result := CredentialQuota{
		CredentialID: credential.ID,
		ProviderID:   provider.ID,
		ProviderType: provider.Type,
		Quotas:       map[string]QuotaEntry{},
	}
	urls := []string{
		"https://api.minimax.io/v1/coding_plan/remains",
		"https://api.minimax.io/anthropic/v1/coding_plan/remains",
	}
	if provider.ID == "minimax-cn" {
		urls = []string{
			"https://api.minimaxi.com/v1/coding_plan/remains",
			"https://api.minimaxi.com/anthropic/v1/coding_plan/remains",
		}
	}
	headers := http.Header{
		"Authorization": {"Bearer " + credential.Secret},
		"Accept":          {"application/json"},
	}
	var body []byte
	var status int
	var err error
	for _, url := range urls {
		body, status, err = r.quotaGET(ctx, url, headers)
		if err == nil && status >= 200 && status < 300 {
			break
		}
	}
	if err != nil {
		result.Message = err.Error()
		return result
	}
	if status < 200 || status >= 300 {
		result.Message = fmt.Sprintf("MiniMax quota API unavailable (HTTP %d)", status)
		return result
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		result.Message = "Invalid MiniMax quota response"
		return result
	}
	models, _ := payload["models"].([]any)
	for _, raw := range models {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		name := stringValue(firstValue(item, "model_name", "modelName"))
		if name == "" {
			name = "MiniMax"
		}
		if name == "MiniMax-M*" || name == "general" {
			name = "M-series"
		}
		sessionRemaining := float64(numberValue(firstValue(item, "current_interval_remaining_percent", "currentIntervalRemainingPercent")))
		if sessionRemaining > 0 || numberValue(firstValue(item, "current_interval_total_count", "currentIntervalTotalCount")) > 0 {
			result.Quotas[name+" session"] = QuotaEntry{
				Name:      name + " session",
				Used:      float64(max(0, 100-int(sessionRemaining))),
				Total:     100,
				Remaining: sessionRemaining,
			}
		}
	}
	if len(result.Quotas) == 0 {
		result.Message = "MiniMax connected. No quota windows returned."
	}
	return result
}

func (r *Registry) vercelGatewayQuota(ctx context.Context, credential store.Credential) CredentialQuota {
	result := CredentialQuota{
		CredentialID: credential.ID,
		ProviderID:   credential.ProviderID,
		ProviderType: credential.ProviderID,
		Quotas:       map[string]QuotaEntry{},
		Plan:         "Pay-as-you-go",
	}
	headers := http.Header{
		"Authorization": {"Bearer " + credential.Secret},
		"Accept":          {"application/json"},
	}
	body, status, err := r.quotaGET(ctx, "https://ai-gateway.vercel.sh/v1/credits", headers)
	if err != nil {
		result.Message = err.Error()
		return result
	}
	if status < 200 || status >= 300 {
		result.Message = fmt.Sprintf("Vercel AI Gateway credits API unavailable (HTTP %d)", status)
		return result
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		result.Message = "Invalid Vercel AI Gateway credits response"
		return result
	}
	balance := float64(numberValue(payload["balance"]))
	totalUsed := float64(numberValue(payload["total_used"]))
	const monthlyCredit = 5.0
	if balance <= 0 && totalUsed <= 0 {
		result.Message = "Vercel AI Gateway connected. No credit allocation found."
		return result
	}
	result.Quotas["Remaining (USD)"] = QuotaEntry{
		Name:      "Remaining (USD)",
		Used:      monthlyCredit - balance,
		Total:     monthlyCredit,
		Remaining: (balance / monthlyCredit) * 100,
	}
	return result
}

func (r *Registry) grokCLIQuota(ctx context.Context, credential store.Credential) CredentialQuota {
	result := CredentialQuota{
		CredentialID: credential.ID,
		ProviderID:   credential.ProviderID,
		ProviderType: "xai",
		Quotas:       map[string]QuotaEntry{},
	}
	headers := http.Header{
		"Authorization":            {"Bearer " + credential.Secret},
		"Accept":                   {"application/json"},
		"User-Agent":               {"tproxy-quota/1.0"},
		"x-xai-token-auth":         {"xai-grok-cli"},
		"x-grok-client-identifier": {"grok-cli"},
		"x-grok-client-version":    {"0.2.99"},
		"x-grok-client-mode":       {"headless"},
	}
	body, status, err := r.quotaGET(ctx, "https://cli-chat-proxy.grok.com/v1/billing?format=credits", headers)
	if err != nil {
		result.Message = err.Error()
		return result
	}
	if status < 200 || status >= 300 {
		result.Message = fmt.Sprintf("Grok CLI billing API unavailable (HTTP %d)", status)
		return result
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		result.Message = "Invalid Grok CLI billing response"
		return result
	}
	config, _ := payload["config"].(map[string]any)
	if config == nil {
		config = payload
	}
	monthlyLimit := float64(numberValue(firstValue(config, "monthlyLimit", "monthly_limit")))
	includedUsed := float64(numberValue(firstValue(config, "includedUsed", "included_used")))
	if monthlyLimit > 0 {
		result.Quotas["Monthly included"] = QuotaEntry{
			Name:      "Monthly included",
			Used:      includedUsed,
			Total:     monthlyLimit,
			Remaining: quotaPercentRemaining(includedUsed, monthlyLimit),
		}
	}
	onDemandCap := float64(numberValue(config["onDemandCap"]))
	onDemandUsed := float64(numberValue(config["onDemandUsed"]))
	if onDemandCap > 0 {
		result.Quotas["On-demand"] = QuotaEntry{
			Name:      "On-demand",
			Used:      onDemandUsed,
			Total:     onDemandCap,
			Remaining: quotaPercentRemaining(onDemandUsed, onDemandCap),
		}
	}
	if len(result.Quotas) == 0 {
		result.Message = "Grok connected. No credit allotment returned."
	}
	return result
}

func (r *Registry) kiroQuota(ctx context.Context, credential store.Credential) CredentialQuota {
	result := CredentialQuota{
		CredentialID: credential.ID,
		ProviderID:   credential.ProviderID,
		ProviderType: "kiro",
		Quotas:       map[string]QuotaEntry{},
	}
	headers := http.Header{
		"Authorization":  {"Bearer " + credential.Secret},
		"Accept":         {"application/json"},
		"x-amz-user-agent": {"aws-sdk-js/1.0.0 KiroIDE"},
		"user-agent":     {"aws-sdk-js/1.0.0 KiroIDE"},
	}
	url := "https://codewhisperer.us-east-1.amazonaws.com/getUsageLimits?isEmailRequired=true&origin=AI_EDITOR&resourceType=AGENTIC_REQUEST"
	body, status, err := r.quotaGET(ctx, url, headers)
	if err != nil {
		result.Message = err.Error()
		return result
	}
	if status < 200 || status >= 300 {
		result.Message = fmt.Sprintf("Kiro usage API unavailable (HTTP %d)", status)
		return result
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		result.Message = "Invalid Kiro usage response"
		return result
	}
	if info, ok := payload["subscriptionInfo"].(map[string]any); ok {
		result.Plan = stringValue(info["subscriptionTitle"])
	}
	resetAt := stringValue(firstValue(payload, "nextDateReset", "resetDate"))
	for _, raw := range toAnySlice(payload["usageBreakdownList"]) {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		name := strings.ToLower(stringValue(item["resourceType"]))
		if name == "" {
			name = "usage"
		}
		used := float64(numberValue(item["currentUsageWithPrecision"]))
		total := float64(numberValue(item["usageLimitWithPrecision"]))
		remaining := quotaPercentRemaining(used, total)
		result.Quotas[name] = QuotaEntry{
			Name:      name,
			Used:      used,
			Total:     total,
			Remaining: remaining,
			ResetAt:   resetAt,
		}
	}
	if len(result.Quotas) == 0 {
		result.Message = "Kiro connected. No quota windows returned."
	}
	return result
}

func (r *Registry) qoderQuota(ctx context.Context, credential store.Credential) CredentialQuota {
	result := CredentialQuota{
		CredentialID: credential.ID,
		ProviderID:   credential.ProviderID,
		ProviderType: "qoder",
		Quotas:       map[string]QuotaEntry{},
	}
	headers := http.Header{
		"Authorization": {"Bearer " + credential.Secret},
		"Accept":        {"application/json"},
	}
	body, status, err := r.quotaGET(ctx, "https://center.qoder.sh/api/v1/quota", headers)
	if err != nil {
		result.Message = err.Error()
		return result
	}
	if status < 200 || status >= 300 {
		result.Message = fmt.Sprintf("Qoder quota API unavailable (HTTP %d)", status)
		return result
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		result.Message = "Invalid Qoder quota response"
		return result
	}
	credits := float64(numberValue(firstValue(payload, "credits", "remaining_credits", "availableCredits")))
	if credits > 0 {
		result.Quotas["Credits"] = QuotaEntry{
			Name:      "Credits",
			Used:      0,
			Total:     credits,
			Remaining: 100,
		}
	}
	if len(result.Quotas) == 0 {
		result.Message = "Qoder connected. No quota data returned."
	}
	return result
}

func (r *Registry) codebuddyCNQuota(ctx context.Context, credential store.Credential) CredentialQuota {
	result := CredentialQuota{
		CredentialID: credential.ID,
		ProviderID:   credential.ProviderID,
		ProviderType: providerTypeOrDefault(credential),
		Quotas:       map[string]QuotaEntry{},
	}
	headers := authHeaders(store.Provider{Type: "openai-compatible"}, credential)
	body, status, err := r.quotaGET(ctx, "https://copilot.tencent.com/v2/billing/usage", headers)
	if err != nil {
		result.Message = err.Error()
		return result
	}
	if status < 200 || status >= 300 {
		result.Message = fmt.Sprintf("CodeBuddy CN usage API unavailable (HTTP %d)", status)
		return result
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		result.Message = "Invalid CodeBuddy CN usage response"
		return result
	}
	for key, raw := range payload {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		used := float64(numberValue(firstValue(item, "used", "usedAmount")))
		total := float64(numberValue(firstValue(item, "total", "totalAmount", "limit")))
		if total <= 0 {
			continue
		}
		result.Quotas[key] = QuotaEntry{
			Name:      key,
			Used:      used,
			Total:     total,
			Remaining: quotaPercentRemaining(used, total),
		}
	}
	if len(result.Quotas) == 0 {
		result.Message = "CodeBuddy CN connected. No quota windows returned."
	}
	return result
}

func providerTypeOrDefault(credential store.Credential) string {
	if credential.ProviderID != "" {
		return credential.ProviderID
	}
	return "openai-compatible"
}

func toAnySlice(value any) []any {
	items, _ := value.([]any)
	return items
}

func millisToRFC3339(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

func quotaPercentRemaining(used, total float64) float64 {
	if total <= 0 {
		return 0
	}
	remaining := ((total - used) / total) * 100
	if remaining < 0 {
		return 0
	}
	return remaining
}
