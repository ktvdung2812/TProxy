package providers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/store"
)

// Anthropic reports each window as the percentage already consumed, not as a
// token count, so a parser that looks for used/total silently reports every
// Claude account as having full quota.
func TestClaudeUsageWindowsParseUtilization(t *testing.T) {
	var payload map[string]any
	raw := `{
		"five_hour": {"utilization": 87, "resets_at": "2026-08-14T10:00:00Z"},
		"seven_day": {"utilization": 40.5, "resets_at": "2026-08-20T10:00:00Z"},
		"seven_day_opus": {"utilization": 12, "resets_at": "2026-08-20T10:00:00Z"},
		"extra_usage": {"enabled": true},
		"account_uuid": "abc"
	}`
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}

	quotas := map[string]QuotaEntry{}
	appendClaudeUsageWindows(quotas, payload)

	if len(quotas) != 3 {
		t.Fatalf("windows = %+v", quotas)
	}
	session := quotas["session"]
	if session.Used != 87 || session.Total != 100 || session.Remaining != 13 {
		t.Fatalf("session = %+v", session)
	}
	if session.ResetAt != "2026-08-14T10:00:00Z" {
		t.Fatalf("session reset = %q", session.ResetAt)
	}
	if weekly := quotas["weekly"]; weekly.Used != 40.5 || weekly.Remaining != 59.5 {
		t.Fatalf("weekly = %+v", weekly)
	}
	if opus := quotas["weekly_opus"]; opus.Used != 12 || opus.Remaining != 88 {
		t.Fatalf("weekly_opus = %+v", opus)
	}
	// extra_usage and account metadata are not rolling windows.
	if _, ok := quotas["extra_usage"]; ok {
		t.Fatalf("extra_usage reported as a window: %+v", quotas)
	}
}

// A window with no numeric utilization must be dropped rather than reported as
// 0% used, which would look like a fully available window.
func TestClaudeUsageWindowsSkipUnusableEntries(t *testing.T) {
	quotas := map[string]QuotaEntry{}
	appendClaudeUsageWindows(quotas, map[string]any{
		"five_hour": map[string]any{"resets_at": "2026-08-14T10:00:00Z"},
		"seven_day": map[string]any{"utilization": "unknown"},
	})
	if len(quotas) != 0 {
		t.Fatalf("windows = %+v", quotas)
	}
}

func TestClaudeUsageWindowsClampUtilization(t *testing.T) {
	quotas := map[string]QuotaEntry{}
	appendClaudeUsageWindows(quotas, map[string]any{
		"five_hour": map[string]any{"utilization": 140.0},
		"seven_day": map[string]any{"utilization": -5.0},
	})
	if session := quotas["session"]; session.Used != 100 || session.Remaining != 0 {
		t.Fatalf("session = %+v", session)
	}
	if weekly := quotas["weekly"]; weekly.Used != 0 || weekly.Remaining != 100 {
		t.Fatalf("weekly = %+v", weekly)
	}
}

// Only the 5h window gates routing; weekly limits are display-only, matching
// how the Codex windows are treated.
func TestClaudeSessionWindowGatesRouting(t *testing.T) {
	if !quotaKeyAffectsRouting("claude", "session") {
		t.Fatal("session window should gate routing")
	}
	if quotaKeyAffectsRouting("claude", "weekly") || quotaKeyAffectsRouting("claude", "weekly_opus") {
		t.Fatal("weekly windows should not gate routing")
	}
}

// The extra_usage and spend meters only become windows when enabled — a
// disabled block carries null meters that must not read as usable quota.
func TestClaudePaidWindowsRequireEnablement(t *testing.T) {
	quotas := map[string]QuotaEntry{}
	appendClaudePaidWindows(quotas, map[string]any{
		"extra_usage": map[string]any{"is_enabled": false, "utilization": nil},
		"spend":       map[string]any{"enabled": false, "percent": 0},
	})
	if len(quotas) != 0 {
		t.Fatalf("disabled meters = %+v", quotas)
	}

	appendClaudePaidWindows(quotas, map[string]any{
		"extra_usage": map[string]any{"is_enabled": true, "utilization": 25.0},
		"spend":       map[string]any{"enabled": true, "percent": 100.0, "spend_limit_reached": true},
	})
	if extra := quotas["extra_usage"]; extra.Used != 25 || extra.Remaining != 75 {
		t.Fatalf("extra_usage = %+v", extra)
	}
	if spend := quotas["spend"]; spend.Used != 100 || spend.Remaining != 0 {
		t.Fatalf("spend = %+v", spend)
	}
}

// The flat limits[] array is an alternate shape carrying the same windows; it
// fills keys the object shape left out without overwriting it.
func TestClaudeLimitWindowsFillMissingKeys(t *testing.T) {
	quotas := map[string]QuotaEntry{
		"session": {Name: "session", Used: 87, Total: 100, Remaining: 13},
	}
	appendClaudeLimitWindows(quotas, map[string]any{
		"limits": []any{
			map[string]any{"kind": "session", "percent": 50, "resets_at": "2026-08-14T10:00:00Z"},
			map[string]any{"kind": "weekly_all", "percent": 35, "resets_at": "2026-08-20T10:00:00Z"},
			map[string]any{"kind": "unknown_meter", "percent": 10},
		},
	})
	if session := quotas["session"]; session.Used != 87 {
		t.Fatalf("limits overwrote session: %+v", session)
	}
	weekly, ok := quotas["weekly"]
	if !ok || weekly.Used != 35 || weekly.Remaining != 65 || weekly.ResetAt != "2026-08-20T10:00:00Z" {
		t.Fatalf("weekly = %+v", weekly)
	}
	if _, ok := quotas["unknown_meter"]; ok {
		t.Fatal("unrecognized limit kinds must be skipped")
	}
}

func TestClaudePlanFromProfile(t *testing.T) {
	pro := claudePlanFromProfile(map[string]any{
		"account": map[string]any{"has_claude_pro": true, "has_claude_max": false},
		"organization": map[string]any{
			"organization_type":   "claude_pro",
			"subscription_status": "active",
		},
	})
	if pro != "Claude Pro" {
		t.Fatalf("plan = %q", pro)
	}
	canceled := claudePlanFromProfile(map[string]any{
		"organization": map[string]any{
			"organization_type":   "claude_pro",
			"subscription_status": "canceled",
		},
	})
	if canceled != "Claude Pro (canceled)" {
		t.Fatalf("plan = %q", canceled)
	}
	if got := claudePlanFromProfile(map[string]any{
		"account": map[string]any{"has_claude_max": true},
	}); got != "Claude Max" {
		t.Fatalf("plan = %q", got)
	}
	if got := claudePlanFromProfile(map[string]any{}); got != "" {
		t.Fatalf("plan = %q", got)
	}
}

func TestClaudeUsageThrottleExpires(t *testing.T) {
	now := time.Now()
	setClaudeUsageThrottle("cred-throttle", now.Add(time.Minute))
	if !claudeUsageThrottled("cred-throttle", now) {
		t.Fatal("expected the probe to be cooling down")
	}
	if claudeUsageThrottled("cred-throttle", now.Add(2*time.Minute)) {
		t.Fatal("cooldown should have expired")
	}
	// An expired entry is dropped rather than accumulating per credential.
	claudeUsageThrottle.mu.Lock()
	_, present := claudeUsageThrottle.until["cred-throttle"]
	claudeUsageThrottle.mu.Unlock()
	if present {
		t.Fatal("expired cooldown was not cleared")
	}
}

// Anthropic publishes no model list a Claude Code OAuth token can read, so the
// catalogue is static. Without it the dashboard reports the provider as having
// no models at all.
func TestClaudeStaticModelsAreDiscovered(t *testing.T) {
	provider := store.Provider{ID: "claude", Type: "claude", BaseURL: "https://api.anthropic.com"}
	models := staticDiscoveryModels(provider)
	if len(models) == 0 {
		t.Fatal("claude reported no static models")
	}
	seen := map[string]bool{}
	for _, model := range models {
		if model.ID == "" || model.Name == "" {
			t.Fatalf("incomplete model entry: %+v", model)
		}
		if model.OwnedBy != "anthropic" {
			t.Fatalf("model %q owned by %q", model.ID, model.OwnedBy)
		}
		if seen[model.ID] {
			t.Fatalf("duplicate model id %q", model.ID)
		}
		seen[model.ID] = true
	}
	if !seen["claude-opus-5"] || !seen["claude-sonnet-5"] {
		t.Fatalf("current flagship models missing: %+v", seen)
	}
}
