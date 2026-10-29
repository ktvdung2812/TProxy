package router

import (
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/providers"
)

func TestQuotaWarmupResetAtMaximum(t *testing.T) {
	now := time.Now().UTC()
	quota := providers.CredentialQuota{Quotas: map[string]providers.QuotaEntry{
		"session": {Used: 0, Total: 100, Remaining: 100, WindowMinutes: 300, ResetAt: now.Add(5*time.Hour - time.Minute).Format(time.RFC3339Nano)},
	}}
	if _, ok := quotaWarmupResetAt("codex", quota, now); !ok {
		t.Fatal("full five-hour Codex window should be warmed")
	}
	quota.Quotas["session"] = providers.QuotaEntry{Used: 1, Total: 100, Remaining: 99, WindowMinutes: 300, ResetAt: now.Add(5 * time.Hour).Format(time.RFC3339Nano)}
	if _, ok := quotaWarmupResetAt("codex", quota, now); ok {
		t.Fatal("used window should not be warmed")
	}
}

func TestAntigravityQuotaWarmupUsesFiveHourFallback(t *testing.T) {
	now := time.Now().UTC()
	quota := providers.CredentialQuota{Quotas: map[string]providers.QuotaEntry{
		"gemini-flash": {Used: 0, Total: 1000, Remaining: 100, ResetAt: now.Add(5 * time.Hour).Format(time.RFC3339Nano)},
	}}
	if _, ok := quotaWarmupResetAt("antigravity", quota, now); !ok {
		t.Fatal("full Antigravity window should use the five-hour fallback")
	}
}
