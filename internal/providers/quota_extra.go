package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/store"
)

func (r *Registry) credentialQuotaByPreset(ctx context.Context, provider store.Provider, credential store.Credential) (CredentialQuota, bool) {
	switch strings.ToLower(strings.TrimSpace(provider.ID)) {
	case "deepseek":
		return r.deepseekQuota(ctx, provider, credential), true
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
	case "kimi", "kimi-coding":
		return r.kimiQuota(ctx, provider, credential), true
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
	quotaURL := glmQuotaURL(provider)
	body, status, err := r.glmQuotaGET(ctx, quotaURL, credential.Secret)
	if err != nil {
		result.Message = err.Error()
		return result
	}
	if status == http.StatusUnauthorized {
		result.Message = "GLM API key invalid or expired."
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
	if data == nil {
		result.Message = "GLM connected. No quota windows returned."
		return result
	}
	plan, quotas := parseGLMQuotaData(data)
	result.Plan = plan
	result.Quotas = quotas
	if len(result.Quotas) == 0 {
		result.Message = "GLM connected. No quota windows returned."
	}
	return result
}

func glmQuotaURL(provider store.Provider) string {
	if provider.ID == "glm-cn" {
		return "https://open.bigmodel.cn/api/monitor/usage/quota/limit"
	}
	return "https://api.z.ai/api/monitor/usage/quota/limit"
}

func (r *Registry) glmQuotaGET(ctx context.Context, quotaURL, secret string) ([]byte, int, error) {
	authVariants := []string{strings.TrimSpace(secret), "Bearer " + strings.TrimSpace(secret)}
	seen := map[string]bool{}
	for _, auth := range authVariants {
		if auth == "" || seen[auth] {
			continue
		}
		seen[auth] = true
		headers := http.Header{
			"Authorization":   {auth},
			"Accept":          {"application/json"},
			"Accept-Language": {"en-US,en"},
		}
		body, status, err := r.quotaGET(ctx, quotaURL, headers)
		if err != nil {
			return nil, 0, err
		}
		if status == http.StatusUnauthorized && len(seen) < len(authVariants) {
			continue
		}
		return body, status, nil
	}
	return nil, http.StatusUnauthorized, nil
}

func parseGLMQuotaData(data map[string]any) (string, map[string]QuotaEntry) {
	quotas := map[string]QuotaEntry{}
	level := stringValue(data["level"])
	plan := level
	if level != "" {
		plan = strings.ToUpper(level[:1]) + strings.ToLower(level[1:])
	}
	limits, _ := data["limits"].([]any)
	tokenLimits := 0
	for _, raw := range limits {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		switch stringValue(item["type"]) {
		case "TOKENS_LIMIT":
			tokenLimits++
			key, name := glmTokenLimitKey(numberValue(item["unit"]), numberValue(item["number"]))
			if tokenLimits == 1 && key == "token_0_0" {
				key, name = "session", "5h Token"
			}
			quotas[key] = glmPercentQuotaEntry(name, item)
		case "TIME_LIMIT":
			quotas["mcp"] = glmMCPQuotaEntry(item)
		}
	}
	return plan, quotas
}

func glmTokenLimitKey(unit, number int) (string, string) {
	if unit == 3 && number == 5 {
		return "session", "5h Token"
	}
	if unit == 6 && number == 1 {
		return "weekly", "Weekly"
	}
	if unit == 0 && number == 0 {
		return "token_0_0", "Token usage"
	}
	return fmt.Sprintf("token_%d_%d", unit, number), "Token usage"
}

func glmPercentQuotaEntry(name string, item map[string]any) QuotaEntry {
	usedPercent := glmQuotaFloat(firstValue(item, "percentage", "used_percent"))
	if usedPercent < 0 {
		usedPercent = 0
	}
	if usedPercent > 100 {
		usedPercent = 100
	}
	resetAt := ""
	if resetMS := int64(glmQuotaFloat(item["nextResetTime"])); resetMS > 0 {
		resetAt = millisToRFC3339(resetMS)
	}
	return QuotaEntry{
		Name:      name,
		Used:      usedPercent,
		Total:     100,
		Remaining: float64(max(0, 100-int(usedPercent))),
		ResetAt:   resetAt,
	}
}

func glmMCPQuotaEntry(item map[string]any) QuotaEntry {
	used := glmQuotaFloat(firstValue(item, "currentValue", "current_value"))
	total := glmQuotaFloat(firstValue(item, "usage", "total"))
	if total <= 0 {
		usedPercent := glmQuotaFloat(item["percentage"])
		return QuotaEntry{
			Name:      "MCP (1 Month)",
			Used:      usedPercent,
			Total:     100,
			Remaining: float64(max(0, 100-int(usedPercent))),
		}
	}
	return QuotaEntry{
		Name:      "MCP (1 Month)",
		Used:      used,
		Total:     total,
		Remaining: quotaPercentRemaining(used, total),
	}
}

func glmQuotaFloat(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case string:
		if strings.TrimSpace(v) == "" {
			return 0
		}
		parsed, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0
		}
		return parsed
	default:
		return float64(numberValue(value))
	}
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

func (r *Registry) kimiQuota(ctx context.Context, provider store.Provider, credential store.Credential) CredentialQuota {
	result := CredentialQuota{
		CredentialID: credential.ID,
		ProviderID:   provider.ID,
		ProviderType: provider.Type,
		Quotas:       map[string]QuotaEntry{},
	}
	baseURL := kimiQuotaBaseURL(provider)
	headers := authHeaders(provider, credential)
	headers.Set("User-Agent", "KimiCLI/1.6")

	body, status, err := r.quotaGET(ctx, baseURL+"/usages", headers)
	if err != nil {
		result.Message = err.Error()
		return result
	}
	if status == http.StatusNotFound {
		body, status, err = r.quotaGET(ctx, baseURL+"/usage", headers)
		if err != nil {
			result.Message = err.Error()
			return result
		}
	}
	if status == http.StatusUnauthorized {
		result.Message = "Kimi authentication failed. Use a Kimi Code key (sk-kimi-...) or refresh the OAuth token."
		return result
	}
	if status < 200 || status >= 300 {
		result.Message = fmt.Sprintf("Kimi quota API unavailable (HTTP %d)", status)
		return result
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		result.Message = "Invalid Kimi quota response"
		return result
	}
	plan, quotas := parseKimiQuotaPayload(payload)
	result.Plan = plan
	result.Quotas = quotas
	if len(result.Quotas) == 0 {
		result.Message = "Kimi connected. No quota windows returned."
	}
	return result
}

func kimiQuotaBaseURL(provider store.Provider) string {
	base := strings.TrimSpace(provider.BaseURL)
	if base == "" {
		return "https://api.kimi.com/coding/v1"
	}
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/v1") {
		return base
	}
	return base + "/v1"
}

func parseKimiQuotaPayload(payload map[string]any) (string, map[string]QuotaEntry) {
	quotas := map[string]QuotaEntry{}
	plan := ""
	if user, ok := payload["user"].(map[string]any); ok {
		if membership, ok := user["membership"].(map[string]any); ok {
			plan = strings.TrimPrefix(stringValue(membership["level"]), "LEVEL_")
		}
	}
	if dataList, ok := payload["data"].([]any); ok {
		for _, raw := range dataList {
			item, _ := raw.(map[string]any)
			if item == nil {
				continue
			}
			modelName := stringValue(item["model_name"])
			label := "Limit"
			key := "limit"
			if modelName == "all" {
				label = "Weekly"
				key = "weekly"
			} else if modelName != "" {
				label = modelName
				key = "limit_" + modelName
			}
			quotas[key] = kimiQuotaEntryFromDetail(item, label)
		}
		return plan, quotas
	}
	if usage, ok := payload["usage"].(map[string]any); ok {
		quotas["weekly"] = kimiQuotaEntryFromDetail(usage, "Weekly")
	}
	for idx, raw := range toAnySlice(payload["limits"]) {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		detail, _ := item["detail"].(map[string]any)
		if detail == nil {
			detail = item
		}
		window, _ := item["window"].(map[string]any)
		key, label := kimiQuotaWindowKey(window, idx)
		quotas[key] = kimiQuotaEntryFromDetail(detail, label)
	}
	return plan, quotas
}

func kimiQuotaEntryFromDetail(detail map[string]any, label string) QuotaEntry {
	limit := kimiQuotaNumber(firstValue(detail, "limit", "limit_amount"))
	used := kimiQuotaNumber(firstValue(detail, "used", "used_amount"))
	if used == 0 {
		remaining := kimiQuotaNumber(detail["remaining"])
		if remaining > 0 && limit > 0 {
			used = limit - remaining
		}
	}
	name := label
	if name == "" {
		name = stringValue(firstValue(detail, "name", "title", "model_name"))
	}
	return QuotaEntry{
		Name:      name,
		Used:      used,
		Total:     limit,
		Remaining: quotaPercentRemaining(used, limit),
		ResetAt:   parseResetAt(firstValue(detail, "resetTime", "reset_at", "reset_time")),
	}
}

func kimiQuotaNumber(value any) float64 {
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return 0
		}
		parsed, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0
		}
		return parsed
	default:
		return float64(numberValue(value))
	}
}

func kimiQuotaWindowKey(window map[string]any, idx int) (string, string) {
	duration := numberValue(window["duration"])
	unit := strings.ToUpper(stringValue(firstValue(window, "timeUnit", "time_unit")))
	if strings.Contains(unit, "MINUTE") {
		if duration >= 60 && duration%60 == 0 {
			hours := duration / 60
			return "session", fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("limit_%dm", duration), fmt.Sprintf("%dm", duration)
	}
	if strings.Contains(unit, "HOUR") {
		return "session", fmt.Sprintf("%dh", duration)
	}
	if strings.Contains(unit, "DAY") {
		return fmt.Sprintf("limit_%dd", duration), fmt.Sprintf("%dd", duration)
	}
	if strings.Contains(unit, "MONTH") {
		return fmt.Sprintf("limit_%dmo", duration), fmt.Sprintf("%dmo", duration)
	}
	return fmt.Sprintf("limit_%d", idx+1), fmt.Sprintf("Limit #%d", idx+1)
}

func (r *Registry) deepseekQuota(ctx context.Context, provider store.Provider, credential store.Credential) CredentialQuota {
	result := CredentialQuota{
		CredentialID: credential.ID,
		ProviderID:   provider.ID,
		ProviderType: provider.Type,
		Quotas:       map[string]QuotaEntry{},
		Plan:         "Pay-as-you-go",
	}
	base := strings.TrimSpace(provider.BaseURL)
	if base == "" {
		base = "https://api.deepseek.com"
	}
	quotaURL := openAIResourceURL(base, "/user/balance")
	headers := http.Header{
		"Authorization": {"Bearer " + strings.TrimSpace(credential.Secret)},
		"Accept":        {"application/json"},
	}
	body, status, err := r.quotaGET(withCredentialProxy(ctx, credential), quotaURL, headers)
	if err != nil {
		result.Message = err.Error()
		return result
	}
	if status == http.StatusUnauthorized {
		result.Message = "DeepSeek API key invalid or expired."
		return result
	}
	if status < 200 || status >= 300 {
		result.Message = fmt.Sprintf("DeepSeek balance API unavailable (HTTP %d)", status)
		return result
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		result.Message = "Invalid DeepSeek balance response"
		return result
	}
	available, quotas := parseDeepSeekBalancePayload(payload)
	result.Quotas = quotas
	if !available {
		result.Message = "DeepSeek balance insufficient for API calls."
	} else if len(quotas) == 0 {
		result.Message = "DeepSeek connected. No balance info returned."
	}
	return result
}

func parseDeepSeekBalancePayload(payload map[string]any) (bool, map[string]QuotaEntry) {
	quotas := map[string]QuotaEntry{}
	available := true
	if value, ok := payload["is_available"].(bool); ok {
		available = value
	}
	rawInfos, _ := payload["balance_infos"].([]any)
	for _, raw := range rawInfos {
		info, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		currency := strings.ToUpper(strings.TrimSpace(stringValue(info["currency"])))
		if currency == "" {
			currency = "USD"
		}
		totalText := strings.TrimSpace(stringValue(firstValue(info, "total_balance", "totalBalance")))
		total := kimiQuotaNumber(firstValue(info, "total_balance", "totalBalance"))
		if total <= 0 && available {
			continue
		}
		label := currency + " " + totalText
		if totalText == "" {
			label = fmt.Sprintf("%s %.4g", currency, total)
		}
		key := "balance_" + strings.ToLower(currency)
		entry := QuotaEntry{Name: label, Used: 0, Total: total, Remaining: 100}
		if total <= 0 {
			entry.Used = 1
			entry.Total = 1
			entry.Remaining = 0
		}
		quotas[key] = entry
	}
	if len(quotas) == 0 && !available {
		quotas["balance"] = QuotaEntry{Name: "Balance 0", Used: 1, Total: 1, Remaining: 0}
	}
	return available, quotas
}
