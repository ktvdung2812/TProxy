package providers

import (
	"testing"

	"github.com/tproxy/tproxy/internal/store"
)

func TestParseKimiQuotaPayloadUsageAndLimits(t *testing.T) {
	payload := map[string]any{
		"user": map[string]any{
			"membership": map[string]any{"level": "LEVEL_INTERMEDIATE"},
		},
		"usage": map[string]any{
			"limit":     "100",
			"used":      "32",
			"remaining": "68",
			"resetTime": "2026-07-24T15:50:30.774858Z",
		},
		"limits": []any{
			map[string]any{
				"window": map[string]any{"duration": 300, "timeUnit": "TIME_UNIT_MINUTE"},
				"detail": map[string]any{
					"limit":     "100",
					"remaining": "100",
					"resetTime": "2026-07-19T12:50:30.774858Z",
				},
			},
		},
	}

	plan, quotas := parseKimiQuotaPayload(payload)
	if plan != "INTERMEDIATE" {
		t.Fatalf("plan = %q", plan)
	}
	weekly, ok := quotas["weekly"]
	if !ok {
		t.Fatal("expected weekly quota")
	}
	if weekly.Used != 32 || weekly.Total != 100 || weekly.Remaining != 68 {
		t.Fatalf("weekly = %+v", weekly)
	}
	session, ok := quotas["session"]
	if !ok {
		t.Fatal("expected session quota")
	}
	if session.Name != "5h" || session.Used != 0 || session.Total != 100 || session.Remaining != 100 {
		t.Fatalf("session = %+v", session)
	}
}

func TestParseKimiQuotaPayloadDataList(t *testing.T) {
	payload := map[string]any{
		"data": []any{
			map[string]any{"model_name": "all", "used": 10, "limit": 50, "remaining": 40},
			map[string]any{"model_name": "k3", "used": 2, "limit": 20, "remaining": 18},
		},
	}

	_, quotas := parseKimiQuotaPayload(payload)
	weekly := quotas["weekly"]
	if weekly.Used != 10 || weekly.Total != 50 {
		t.Fatalf("weekly = %+v", weekly)
	}
	limit := quotas["limit_k3"]
	if limit.Used != 2 || limit.Total != 20 {
		t.Fatalf("limit_k3 = %+v", limit)
	}
}

func TestKimiQuotaBaseURL(t *testing.T) {
	tests := []struct {
		base string
		want string
	}{
		{"", "https://api.kimi.com/coding/v1"},
		{"https://api.kimi.com/coding", "https://api.kimi.com/coding/v1"},
		{"https://api.kimi.com/coding/v1", "https://api.kimi.com/coding/v1"},
	}
	for _, tc := range tests {
		got := kimiQuotaBaseURL(store.Provider{BaseURL: tc.base})
		if got != tc.want {
			t.Fatalf("base %q => %q, want %q", tc.base, got, tc.want)
		}
	}
}
