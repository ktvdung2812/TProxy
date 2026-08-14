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

// QuotaAtZero reports whether routing quota is fully depleted (0% left).
// A credential is depleted only when every routing-relevant window is empty.
// Multi-window providers (e.g. Grok monthly + prepaid) stay routable while any
// window still has remaining capacity.
func QuotaAtZero(quota CredentialQuota) bool {
	if len(quota.Quotas) == 0 {
		return false
	}
	hasRoutingWindow := false
	for key, entry := range quota.Quotas {
		if !quotaKeyAffectsRouting(quota.ProviderType, key) {
			continue
		}
		if entry.Unlimited || entry.Total <= 0 {
			continue
		}
		hasRoutingWindow = true
		if QuotaEntryRemainingPercent(entry) > QuotaDepletedAutoDisableThreshold {
			return false
		}
	}
	return hasRoutingWindow
}
