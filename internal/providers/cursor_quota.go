package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	cursorpkg "github.com/tproxy/tproxy/internal/providers/cursor"
	"github.com/tproxy/tproxy/internal/store"
)

const (
	cursorPeriodUsagePath  = "/aiserver.v1.DashboardService/GetCurrentPeriodUsage"
	cursorStripeProfileURL = "https://api2.cursor.sh/auth/full_stripe_profile"
)

// cursorQuota reads the monthly billing-cycle usage from the Cursor IDE API.
// The web dashboard endpoints need a browser session cookie, but the Connect
// API the IDE itself uses accepts the stored access token.
func (r *Registry) cursorQuota(ctx context.Context, provider store.Provider, credential store.Credential) CredentialQuota {
	result := CredentialQuota{
		CredentialID: credential.ID,
		ProviderID:   provider.ID,
		ProviderType: provider.Type,
		Quotas:       map[string]QuotaEntry{},
	}
	token, machineID, err := cursorCredentials(credential)
	if err != nil {
		result.Message = "Cursor credential is missing access token or machine ID; re-import from Cursor IDE."
		return result
	}
	baseURL := strings.TrimRight(provider.BaseURL, "/")
	if baseURL == "" {
		baseURL = cursorpkg.BaseURL
	}
	headers := cursorpkg.BuildCursorHeaders(token, &machineID, true)
	quotaHeaders := make(http.Header, len(headers))
	for key, value := range headers {
		quotaHeaders.Set(key, value)
	}
	// The IDE talks connect+proto, but Connect unary endpoints also accept
	// JSON, which is what these probes send.
	quotaHeaders.Set("Content-Type", "application/json")
	quotaHeaders.Set("Accept", "application/json")

	body, status, err := r.quotaPOST(ctx, baseURL+cursorPeriodUsagePath, quotaHeaders, map[string]any{})
	if err != nil {
		result.Message = err.Error()
		return result
	}
	if status < 200 || status >= 300 {
		result.Message = fmt.Sprintf("Cursor usage API unavailable (HTTP %d)", status)
		return result
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		result.Message = "Invalid Cursor usage response"
		return result
	}
	entry, renewsAt := cursorPeriodUsageEntry(payload)
	if renewsAt != "" {
		result.RenewsAt = renewsAt
	}
	if entry != nil {
		result.Quotas[entry.Name] = *entry
	}
	// Plan name lives on a separate endpoint; soft-fail, it only costs the label.
	result.Plan = r.cursorMembershipPlan(ctx, quotaHeaders)
	if len(result.Quotas) == 0 {
		result.Message = "Cursor connected. No quota windows returned."
	}
	return result
}

// cursorPeriodUsageEntry normalizes the GetCurrentPeriodUsage payload into one
// monthly entry. billingCycleStart/End are epoch milliseconds encoded as
// strings; spend figures are cents.
func cursorPeriodUsageEntry(payload map[string]any) (*QuotaEntry, string) {
	renewsAt := parseResetAt(cursorEpochMillis(payload["billingCycleEnd"]))
	planUsage, _ := payload["planUsage"].(map[string]any)
	if planUsage == nil {
		return nil, renewsAt
	}
	used := float64(numberValue(planUsage["totalPercentUsed"]))
	if used < 0 {
		used = 0
	}
	if used > 100 {
		used = 100
	}
	return &QuotaEntry{
		Name:      "monthly",
		Used:      used,
		Total:     100,
		Remaining: 100 - used,
		ResetAt:   renewsAt,
	}, renewsAt
}

func cursorEpochMillis(value any) any {
	raw, ok := value.(string)
	if !ok {
		return value
	}
	millis, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || millis <= 0 {
		return ""
	}
	return float64(millis)
}

// cursorMembershipPlan reads the membership type (pro/business/...) from the
// stripe profile endpoint. Soft-fail: an empty plan only costs the label.
func (r *Registry) cursorMembershipPlan(ctx context.Context, headers http.Header) string {
	body, status, err := r.quotaGET(ctx, cursorStripeProfileURL, headers)
	if err != nil || status < 200 || status >= 300 {
		return ""
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	return stringValue(firstValue(payload, "membershipType", "individualMembershipType"))
}
