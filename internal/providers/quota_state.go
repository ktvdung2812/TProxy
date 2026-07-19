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
	if strings.Contains(normalizedKey, "weekly") || strings.Contains(normalizedKey, "review") {
		return false
	}
	if normalizedType == "codex" {
		return normalizedKey == "session"
	}
	return true
}

// QuotaAtZero reports whether routing quota is fully depleted (0% left).
func QuotaAtZero(quota CredentialQuota) bool {
	if len(quota.Quotas) == 0 {
		return false
	}
	for key, entry := range quota.Quotas {
		if !quotaKeyAffectsRouting(quota.ProviderType, key) {
			continue
		}
		if entry.Unlimited || entry.Total <= 0 {
			continue
		}
		if QuotaEntryRemainingPercent(entry) <= QuotaDepletedAutoDisableThreshold {
			return true
		}
	}
	return false
}
