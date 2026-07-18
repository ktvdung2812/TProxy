package providers

import "testing"

func TestQuotaAtZero(t *testing.T) {
	quota := CredentialQuota{
		Quotas: map[string]QuotaEntry{
			"session": {Name: "Session", Used: 100, Total: 100, Remaining: 0},
			"weekly":  {Name: "Weekly", Used: 10, Total: 100, Remaining: 90},
		},
	}
	if !QuotaAtZero(quota) {
		t.Fatal("expected session at 0% to mark quota depleted")
	}

	quota.Quotas["session"] = QuotaEntry{Name: "Session", Used: 50, Total: 100, Remaining: 50}
	if QuotaAtZero(quota) {
		t.Fatal("expected recovered session quota to be available")
	}

	quota.Quotas = map[string]QuotaEntry{
		"unlimited": {Name: "Unlimited", Unlimited: true},
	}
	if QuotaAtZero(quota) {
		t.Fatal("unlimited windows should not trigger auto-disable")
	}
}
