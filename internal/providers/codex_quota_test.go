package providers

import (
	"math"
	"testing"
)

func TestCodexWeeklyPrimaryAndMissingQuota(t *testing.T) {
	quotas := map[string]QuotaEntry{}
	appendCodexWindows(quotas, "", map[string]any{
		"primary_window":   map[string]any{"used_percent": 9, "limit_window_seconds": 604800, "reset_at": 1792651704},
		"secondary_window": map[string]any{"used_percent": 0, "limit_window_seconds": 0},
	})
	if _, invented := quotas["session"]; invented {
		t.Fatal("weekly primary mislabeled as a session", quotas)
	}
	if weekly := quotas["weekly"]; weekly.WindowMinutes != 10080 || weekly.Remaining != 91 || weekly.Total != 100 {
		t.Fatal("weekly quota", weekly)
	}
	if unknown := quotas["secondary"]; unknown.Total != 0 || unknown.Remaining != 0 || unknown.Unlimited {
		t.Fatal("missing duration invented available quota", unknown)
	}
	for _, used := range []any{nil, "invalid", -1, 101, math.NaN(), math.Inf(1)} {
		entry := codexWindowEntry("session", map[string]any{"used_percent": used, "limit_window_seconds": 18000})
		if entry.Total != 0 || entry.Remaining != 0 || entry.Unlimited || entry.WindowMinutes != 300 {
			t.Fatal("invalid usage invented available quota", used, entry)
		}
	}
	for _, duration := range []any{nil, 0, -1, "invalid", math.NaN(), math.Inf(1)} {
		entry := codexWindowEntry("primary", map[string]any{"used_percent": 0, "limit_window_seconds": duration})
		if entry.Total != 0 || entry.Remaining != 0 || entry.WindowMinutes != 0 {
			t.Fatal("invalid duration invented a window", duration, entry)
		}
	}
	if entry := codexWindowEntry("session", map[string]any{"used_percent": 9.25, "limit_window_seconds": 18000}); entry.Remaining != 90.75 {
		t.Fatal("percentage precision lost", entry)
	}
}

func TestCodexPlanName(t *testing.T) {
	cases := map[string]string{
		"free":         "Free",
		"plus":         "Plus",
		"pro":          "Pro",
		"team":         "Team",
		"business":     "Business",
		"edu_account":  "Edu Account",
		"":             "",
		" enterprise ": "Enterprise",
	}
	for raw, want := range cases {
		if got := codexPlanName(raw); got != want {
			t.Fatalf("codexPlanName(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestParseResetAtDateOnlyLayouts(t *testing.T) {
	if got := parseResetAt("10/01/2026"); got != "2026-10-01T00:00:00Z" {
		t.Fatalf("us date = %q", got)
	}
	if got := parseResetAt("2026-10-01"); got != "2026-10-01T00:00:00Z" {
		t.Fatalf("iso date = %q", got)
	}
	if got := parseResetAt("not a date"); got != "not a date" {
		t.Fatalf("unparseable = %q", got)
	}
}
