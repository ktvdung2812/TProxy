package providers

import (
	"encoding/json"
	"testing"

	"github.com/tproxy/tproxy/internal/store"
)

func TestParseOpenCodeGoStatus(t *testing.T) {
	payload := map[string]any{}
	raw := `{
	  "subscriberUserId": "acc_01",
	  "useBalance": false,
	  "cancelAtPeriodEnd": false,
	  "access": {
	    "startsAt": "2026-09-10T22:10:15.000Z",
	    "endsAt": "2026-10-10T22:10:15.000Z",
	    "cancelAtPeriodEnd": false,
	    "meters": {
	      "fiveHour": {
	        "startsAt": "2026-09-19T03:29:59.588Z",
	        "resetsAt": "2026-09-19T08:29:59.588Z",
	        "limitMicroCents": "1200000000",
	        "usedMicroCents": "136553093"
	      },
	      "week": {
	        "startsAt": "2026-09-14T00:00:00.000Z",
	        "resetsAt": "2026-09-21T00:00:00.000Z",
	        "limitMicroCents": "3000000000",
	        "usedMicroCents": "766273501"
	      },
	      "month": {
	        "limitMicroCents": "6000000000",
	        "usedMicroCents": "1725927122"
	      }
	    }
	  }
	}`
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	renewsAt, quotas := parseOpenCodeGoStatus(payload)
	if renewsAt != "2026-10-10T22:10:15Z" {
		t.Fatalf("renewsAt = %q, want 2026-10-10T22:10:15Z", renewsAt)
	}
	if len(quotas) != 3 {
		t.Fatalf("expected 3 quota windows, got %d", len(quotas))
	}
	session := quotas["session"]
	if session.Total != 12 {
		t.Fatalf("session total = %v, want 12 USD", session.Total)
	}
	if diff := session.Used - 1.36553093; diff > 0.001 || diff < -0.001 {
		t.Fatalf("session used = %v, want ~1.3655 USD", session.Used)
	}
	if session.ResetAt != "2026-09-19T08:29:59Z" {
		t.Fatalf("session reset = %q, want 2026-09-19T08:29:59Z", session.ResetAt)
	}
	weekly := quotas["weekly"]
	if weekly.Total != 30 || weekly.ResetAt != "2026-09-21T00:00:00Z" {
		t.Fatalf("weekly = %+v, want 30 USD reset 2026-09-21", weekly)
	}
	monthly := quotas["monthly"]
	if monthly.Total != 60 {
		t.Fatalf("monthly total = %v, want 60 USD", monthly.Total)
	}
	if monthly.ResetAt != renewsAt {
		t.Fatalf("monthly reset = %q, want renewal %q", monthly.ResetAt, renewsAt)
	}
}

func TestParseOpenCodeGoUsage(t *testing.T) {
	payload := map[string]any{}
	raw := `{"usage":{
	  "rolling":{"status":"ok","percent":11,"resetsAt":"2026-09-19T08:29:59.588Z"},
	  "weekly":{"status":"ok","percent":25,"resetsAt":"2026-09-21T00:00:00.287Z"},
	  "monthly":{"status":"ok","percent":28,"resetsAt":"2026-10-10T22:10:15.000Z"}
	}}`
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	renewsAt, quotas := parseOpenCodeGoUsage(payload)
	if renewsAt != "2026-10-10T22:10:15Z" {
		t.Fatalf("renewsAt = %q, want monthly.resetsAt", renewsAt)
	}
	if len(quotas) != 3 {
		t.Fatalf("expected 3 quota windows, got %d", len(quotas))
	}
	session := quotas["session"]
	if session.Used != 11 || session.Total != 100 || session.Remaining != 89 {
		t.Fatalf("session = %+v, want 11/100 used, 89 remaining", session)
	}
	if quotas["weekly"].Used != 25 || quotas["monthly"].Used != 28 {
		t.Fatalf("unexpected usage percents: %+v", quotas)
	}
}

func TestOpenCodeGoQuotaKeysAffectRouting(t *testing.T) {
	for _, key := range []string{"session", "weekly", "monthly"} {
		if !quotaKeyAffectsRouting("opencode-go", key) {
			t.Fatalf("opencode-go key %q should affect routing", key)
		}
		if !quotaKeyAutoDisablesAtZero("opencode-go", key) {
			t.Fatalf("opencode-go key %q should auto-disable at zero", key)
		}
	}
	// Other providers must not inherit monthly auto-disable.
	if quotaKeyAutoDisablesAtZero("xai", "monthly") {
		t.Fatal("xai monthly window must not auto-disable at zero")
	}
	if quotaKeyAffectsRouting("openai-compatible", "weekly") {
		t.Fatal("generic openai-compatible weekly window should stay display-only")
	}
}

func TestIsOpenCodeGoQuotaProvider(t *testing.T) {
	cases := []struct {
		id, baseURL string
		want        bool
	}{
		{"opencode-go", "https://opencode.ai/zen/go/v1", true},
		{"custom", "https://opencode.ai/zen/go/v1", true},
		{"custom", "https://opencode.ai/zen/go", true},
		{"opencode", "https://opencode.ai", false},
		{"openai", "https://api.openai.com/v1", false},
	}
	for _, tc := range cases {
		got := isOpenCodeGoQuotaProvider(store.Provider{ID: tc.id, BaseURL: tc.baseURL})
		if got != tc.want {
			t.Errorf("isOpenCodeGoQuotaProvider(%q, %q) = %v, want %v", tc.id, tc.baseURL, got, tc.want)
		}
	}
}
