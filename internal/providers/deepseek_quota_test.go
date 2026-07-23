package providers

import "testing"

func TestParseDeepSeekBalancePayload(t *testing.T) {
	available, quotas := parseDeepSeekBalancePayload(map[string]any{
		"is_available": true,
		"balance_infos": []any{
			map[string]any{
				"currency":          "CNY",
				"total_balance":     "110.00",
				"granted_balance":   "10.00",
				"topped_up_balance": "100.00",
			},
			map[string]any{
				"currency":      "USD",
				"total_balance": "0.00",
			},
		},
	})
	if !available {
		t.Fatal("expected available=true")
	}
	cny, ok := quotas["balance_cny"]
	if !ok {
		t.Fatalf("missing CNY balance: %#v", quotas)
	}
	if cny.Name != "CNY 110.00" || cny.Total != 110 || cny.Remaining != 100 {
		t.Fatalf("unexpected CNY entry: %#v", cny)
	}
	if _, ok := quotas["balance_usd"]; ok {
		t.Fatalf("empty USD should be skipped when account is available: %#v", quotas)
	}
}

func TestParseDeepSeekBalancePayloadUnavailable(t *testing.T) {
	available, quotas := parseDeepSeekBalancePayload(map[string]any{
		"is_available":  false,
		"balance_infos": []any{},
	})
	if available {
		t.Fatal("expected available=false")
	}
	entry, ok := quotas["balance"]
	if !ok || entry.Remaining != 0 {
		t.Fatalf("expected depleted balance entry, got %#v", quotas)
	}
}
