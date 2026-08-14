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
