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
		"session": {Name: "Session", Used: 98, Total: 100, Remaining: 2},
		"weekly":  {Name: "Weekly", Used: 100, Total: 100, Remaining: 0},
	}
	quota.ProviderType = "codex"
	if QuotaAtZero(quota) {
		t.Fatal("codex session with remaining quota should stay routable when weekly is empty")
	}

	quota.Quotas = map[string]QuotaEntry{
		"unlimited": {Name: "Unlimited", Unlimited: true},
	}
	if QuotaAtZero(quota) {
		t.Fatal("unlimited windows should not trigger auto-disable")
	}

	// Multi-window: depleted monthly + remaining prepaid stays routable.
	quota = CredentialQuota{
		ProviderType: "xai",
		Quotas: map[string]QuotaEntry{
			"monthly": {Name: "Monthly included", Used: 150, Total: 150, Remaining: 0},
			"prepaid": {Name: "Prepaid", Used: 0, Total: 25, Remaining: 100},
		},
	}
	if QuotaAtZero(quota) {
		t.Fatal("prepaid remaining should keep Grok credential routable")
	}

	// All finite windows empty → depleted.
	quota.Quotas["prepaid"] = QuotaEntry{Name: "Prepaid", Used: 25, Total: 25, Remaining: 0}
	if !QuotaAtZero(quota) {
		t.Fatal("expected fully depleted multi-window quota")
	}
}
