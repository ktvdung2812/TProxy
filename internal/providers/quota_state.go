package providers

import "math"

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

// QuotaAtZero reports whether any finite quota window is fully depleted (0% left).
func QuotaAtZero(quota CredentialQuota) bool {
	if len(quota.Quotas) == 0 {
		return false
	}
	for _, entry := range quota.Quotas {
		if entry.Unlimited || entry.Total <= 0 {
			continue
		}
		if QuotaEntryRemainingPercent(entry) <= QuotaDepletedAutoDisableThreshold {
			return true
		}
	}
	return false
}
