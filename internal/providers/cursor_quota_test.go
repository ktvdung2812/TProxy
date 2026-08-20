package providers

import (
	"encoding/json"
	"testing"
)

func TestCursorPeriodUsageEntry(t *testing.T) {
	var payload map[string]any
	raw := []byte(`{
		"billingCycleStart": "1784662162000",
		"billingCycleEnd": "1787340562000",
		"planUsage": {"totalSpend": 34512, "includedSpend": 2000, "limit": 2000, "totalPercentUsed": 100},
		"enabled": true
	}`)
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	entry, renewsAt := cursorPeriodUsageEntry(payload)
	if renewsAt == "" {
		t.Fatal("billingCycleEnd was not parsed into a renewal date")
	}
	if entry == nil || entry.Name != "monthly" || entry.Used != 100 || entry.Total != 100 || entry.Remaining != 0 {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.ResetAt != renewsAt {
		t.Fatalf("reset_at = %q, want renewal %q", entry.ResetAt, renewsAt)
	}
}

func TestCursorPeriodUsageEntrySoftFails(t *testing.T) {
	entry, renewsAt := cursorPeriodUsageEntry(map[string]any{})
	if entry != nil || renewsAt != "" {
		t.Fatalf("empty payload = %+v %q", entry, renewsAt)
	}
	entry, _ = cursorPeriodUsageEntry(map[string]any{
		"billingCycleEnd": "junk",
		"planUsage":       map[string]any{"totalPercentUsed": 42},
	})
	if entry == nil || entry.Used != 42 || entry.ResetAt != "" {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestCursorEpochMillis(t *testing.T) {
	if got := cursorEpochMillis("1787340562000"); got != float64(1787340562000) {
		t.Fatalf("millis = %v", got)
	}
	if got := cursorEpochMillis("nope"); got != "" {
		t.Fatalf("invalid = %v", got)
	}
	if got := cursorEpochMillis(nil); got != nil {
		t.Fatalf("nil = %v", got)
	}
}
