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
	if !QuotaAtZero(quota) {
		t.Fatal("expected empty weekly quota to disable the credential even when session quota remains")
	}

	quota.Quotas["weekly"] = QuotaEntry{Name: "Weekly", Used: 99, Total: 100, Remaining: 1}
	if QuotaAtZero(quota) {
		t.Fatal("expected credential to recover when both session and weekly quota remain")
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

	// Grok weekly pool spent but on-demand headroom left: paid usage keeps
	// serving, so the credential must not auto-disable.
	quota = CredentialQuota{
		ProviderType: "xai",
		Quotas: map[string]QuotaEntry{
			"weekly":    {Name: "Weekly limit", Used: 100, Total: 100, Remaining: 0},
			"on_demand": {Name: "On-demand", Used: 3, Total: 50, Remaining: 94},
		},
	}
	if QuotaAtZero(quota) {
		t.Fatal("on-demand headroom should rescue a spent Grok weekly pool")
	}

	// Claude session/weekly spent while extra usage has room: overage billing
	// keeps requests working.
	quota = CredentialQuota{
		ProviderType: "claude",
		Quotas: map[string]QuotaEntry{
			"session":     {Name: "Session", Used: 100, Total: 100, Remaining: 0},
			"weekly":      {Name: "Weekly", Used: 100, Total: 100, Remaining: 0},
			"extra_usage": {Name: "Extra usage", Used: 40, Total: 100, Remaining: 60},
		},
	}
	if QuotaAtZero(quota) {
		t.Fatal("enabled extra usage should rescue a spent Claude plan window")
	}
	quota.Quotas["extra_usage"] = QuotaEntry{Name: "Extra usage", Used: 100, Total: 100, Remaining: 0}
	if !QuotaAtZero(quota) {
		t.Fatal("exhausted extra usage should not rescue the credential")
	}

	// Devin's daily meter is a hard block like its weekly one.
	quota = CredentialQuota{
		ProviderType: "devin",
		Quotas: map[string]QuotaEntry{
			"daily":  {Name: "daily", Used: 100, Total: 100, Remaining: 0},
			"weekly": {Name: "weekly", Used: 30, Total: 100, Remaining: 70},
		},
	}
	if !QuotaAtZero(quota) {
		t.Fatal("a spent Devin daily window should deplete the credential")
	}
	// A non-devin "daily" window alone does not force auto-disable.
	quota = CredentialQuota{
		ProviderType: "other",
		Quotas: map[string]QuotaEntry{
			"daily":   {Name: "daily", Used: 100, Total: 100, Remaining: 0},
			"session": {Name: "session", Used: 30, Total: 100, Remaining: 70},
		},
	}
	if QuotaAtZero(quota) {
		t.Fatal("non-devin daily windows should not auto-disable")
	}
}
