package providers

import (
	"math"
	"strings"
)

const QuotaDepletedAutoDisableThreshold = 0.0

// QuotaEntryRemainingPercent returns remaining quota percentage for a window.
func QuotaEntryRemainingPercent(entry QuotaEntry) float64 {
	if entry.Unlimited || entry.Total <= 0 {
		return 100
	}
	if entry.Remaining > 0 {
		return math.Max(0, math.Min(100, entry.Remaining))
	}
	used := entry.Used
	if used < 0 {
		used = 0
	}
	if used >= entry.Total {
		return 0
	}
	return math.Max(0, math.Round(((entry.Total-used)/entry.Total)*100))
}

// quotaKeyAffectsRouting reports whether a quota window should gate credential routing.
// Auxiliary windows such as weekly or review limits are tracked for display only.
func quotaKeyAffectsRouting(providerType, key string) bool {
	normalizedKey := strings.ToLower(strings.TrimSpace(key))
	normalizedType := strings.ToLower(strings.TrimSpace(providerType))
	// Grok's per-product bars are slices of its weekly allowance, which is
	// already tracked as its own window. Counting them again would let an
	// untouched product (Chat at 1% used) report the account as free while the
	// shared pool it draws from is spent.
	if strings.HasPrefix(normalizedKey, grokProductBreakdownPrefix) {
		return false
	}
	// Grok is the exception to the rule below: it has no session window, so its
	// weekly allowance is the real limit rather than an auxiliary one.
	if isGrokQuotaProviderType(normalizedType) {
		return normalizedKey != "review"
	}
	// Devin's daily and weekly windows are both hard account limits — a spent
	// weekly window blocks requests until reset, so it is not display-only.
	if normalizedType == "devin" {
		return true
	}
	// OpenCode Go's 5-hour, weekly and monthly windows are all hard dollar caps —
	// a spent window blocks paid models until it resets.
	if normalizedType == "opencode-go" {
		return true
	}
	if strings.Contains(normalizedKey, "weekly") || strings.Contains(normalizedKey, "review") {
		return false
	}
	if normalizedType == "codex" {
		return normalizedKey == "session"
	}
	return true
}

// isGrokQuotaProviderType covers both the generic xAI provider type and the
// grok-cli preset, which report the same billing shape.
func isGrokQuotaProviderType(providerType string) bool {
	return providerType == "xai" || providerType == "grok-cli"
}

// quotaKeyAutoDisablesAtZero identifies account-wide quota windows. A session
// window is typically the 5-hour allowance; the primary weekly window is the
// other account-wide allowance. Either one being exhausted makes the
// credential unavailable.
func quotaKeyAutoDisablesAtZero(providerType, key string) bool {
	normalizedKey := strings.ToLower(strings.TrimSpace(key))
	if normalizedKey == "session" || normalizedKey == "weekly" {
		return true
	}
	normalizedType := strings.ToLower(strings.TrimSpace(providerType))
	// OpenCode Go's monthly cap only resets at subscription renewal, so a spent
	// month dead-stops the credential just like a spent session window.
	if normalizedType == "opencode-go" && normalizedKey == "monthly" {
		return true
	}
	// Devin's daily ACU window blocks requests until reset just like its
	// weekly one, so an empty daily meter depletes the credential too.
	return normalizedType == "devin" && normalizedKey == "daily"
}

// quotaRescuedByCredit reports whether a paid-usage meter with remaining
// capacity keeps the credential serving after the included windows empty:
// Claude extra usage / spend meters and Grok prepaid + on-demand headroom bill
// usage past the plan windows instead of hard-stopping the account.
func quotaRescuedByCredit(quota CredentialQuota) bool {
	var keys []string
	switch strings.ToLower(strings.TrimSpace(quota.ProviderType)) {
	case "claude":
		keys = []string{"extra_usage", "spend"}
	case "xai", "grok-cli":
		keys = []string{"prepaid", "on_demand"}
	default:
		return false
	}
	for _, key := range keys {
		entry, ok := quota.Quotas[key]
		if !ok || entry.Unlimited || entry.Total <= 0 {
			continue
		}
		if QuotaEntryRemainingPercent(entry) > QuotaDepletedAutoDisableThreshold {
			return true
		}
	}
	return false
}

// QuotaAtZero reports whether routing quota is fully depleted (0% left).
// An empty session (typically 5h) or primary weekly window immediately
// depletes the credential. Other independent routing windows (e.g. Grok
// monthly + prepaid) only deplete a credential when every window is empty.
func QuotaAtZero(quota CredentialQuota) bool {
	if len(quota.Quotas) == 0 {
		return false
	}
	rescued := quotaRescuedByCredit(quota)
	hasRoutingWindow := false
	hasAvailableRoutingWindow := false
	for key, entry := range quota.Quotas {
		if entry.Unlimited || entry.Total <= 0 {
			continue
		}
		remaining := QuotaEntryRemainingPercent(entry)
		if quotaKeyAutoDisablesAtZero(quota.ProviderType, key) && remaining <= QuotaDepletedAutoDisableThreshold && !rescued {
			return true
		}
		if !quotaKeyAffectsRouting(quota.ProviderType, key) {
			continue
		}
		hasRoutingWindow = true
		if remaining > QuotaDepletedAutoDisableThreshold {
			hasAvailableRoutingWindow = true
		}
	}
	return hasRoutingWindow && !hasAvailableRoutingWindow
}
