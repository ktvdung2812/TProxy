package providers

import "testing"

func TestParseGLMQuotaData(t *testing.T) {
	data := map[string]any{
		"level": "pro",
		"limits": []any{
			map[string]any{
				"type":          "TOKENS_LIMIT",
				"unit":          float64(3),
				"number":        float64(5),
				"percentage":    40.5,
				"nextResetTime": float64(1737000000000),
			},
			map[string]any{
				"type":          "TOKENS_LIMIT",
				"unit":          float64(6),
				"number":        float64(1),
				"percentage":    52.0,
				"nextResetTime": float64(1737500000000),
			},
			map[string]any{
				"type":         "TIME_LIMIT",
				"percentage":   12.3,
				"currentValue": float64(123),
				"usage":        float64(1000),
			},
		},
	}

	plan, quotas := parseGLMQuotaData(data)
	if plan != "Pro" {
		t.Fatalf("plan = %q", plan)
	}
	session := quotas["session"]
	if session.Used != 40.5 || session.Total != 100 || session.Remaining != 60 {
		t.Fatalf("session = %+v", session)
	}
	if session.Name != "5h Token" {
		t.Fatalf("session name = %q", session.Name)
	}
	weekly := quotas["weekly"]
	if weekly.Used != 52.0 || weekly.Remaining != 48.0 {
		t.Fatalf("weekly = %+v", weekly)
	}
	mcp := quotas["mcp"]
	if mcp.Used != 123 || mcp.Total != 1000 || mcp.Remaining != 87.7 {
		t.Fatalf("mcp = %+v", mcp)
	}
}

func TestParseGLMQuotaDataLegacySingleTokenLimit(t *testing.T) {
	data := map[string]any{
		"level": "lite",
		"limits": []any{
			map[string]any{
				"type":       "TOKENS_LIMIT",
				"percentage": float64(0),
			},
		},
	}
	_, quotas := parseGLMQuotaData(data)
	session := quotas["session"]
	if session.Used != 0 || session.Remaining != 100 {
		t.Fatalf("session = %+v", session)
	}
}

func TestGLMQuotaLimitKey(t *testing.T) {
	tests := []struct {
		unit, number int
		wantKey      string
		wantName     string
	}{
		{3, 5, "session", "5h Token"},
		{6, 1, "weekly", "Weekly"},
		{9, 2, "token_9_2", "Token usage"},
	}
	for _, tc := range tests {
		key, name := glmTokenLimitKey(tc.unit, tc.number)
		if key != tc.wantKey || name != tc.wantName {
			t.Fatalf("unit=%d number=%d => key=%q name=%q, want %q %q", tc.unit, tc.number, key, name, tc.wantKey, tc.wantName)
		}
	}
}
