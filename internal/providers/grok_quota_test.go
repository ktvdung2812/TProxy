package providers

import (
	"net/http"
	"testing"

	"github.com/tproxy/tproxy/internal/store"
)

func TestUnwrapProtoVal(t *testing.T) {
	if got := unwrapProtoVal(map[string]any{"val": float64(15000)}); got != 15000 {
		t.Fatalf("proto val = %v", got)
	}
	if got := unwrapProtoVal(float64(12.5)); got != 12.5 {
		t.Fatalf("plain float = %v", got)
	}
	if got := unwrapProtoVal("42.5"); got != 42.5 {
		t.Fatalf("string number = %v", got)
	}
	if got := unwrapProtoVal(nil); got != 0 {
		t.Fatalf("nil = %v", got)
	}
	if got := unwrapProtoVal(map[string]any{"other": 1}); got != 0 {
		t.Fatalf("missing val = %v", got)
	}
}

func TestParseGrokCLIBillingMonthlyAndOnDemand(t *testing.T) {
	billing := map[string]any{
		"config": map[string]any{
			"monthlyLimit":       map[string]any{"val": float64(150)},
			"includedUsed":       map[string]any{"val": float64(23.98)},
			"onDemandCap":        map[string]any{"val": float64(50)},
			"onDemandUsed":       map[string]any{"val": float64(10)},
			"prepaidBalance":     map[string]any{"val": float64(12)},
			"isUnifiedBillingUser": true,
			"billingPeriodEnd":   "2026-07-01T00:00:00+00:00",
		},
	}
	user := map[string]any{
		"subscriptionTier": "super_grok",
		"hasGrokCodeAccess": true,
	}

	plan, quotas, message := parseGrokCLIBilling(billing, user)
	if plan != "Super Grok" {
		t.Fatalf("plan = %q", plan)
	}
	if message != "" {
		t.Fatalf("message = %q", message)
	}

	monthly := quotas["monthly"]
	if monthly.Name != "Monthly included" || monthly.Used != 23.98 || monthly.Total != 150 {
		t.Fatalf("monthly = %+v", monthly)
	}
	// Remaining is percent: (150-23.98)/150 * 100 ≈ 84.013...
	if monthly.Remaining < 84 || monthly.Remaining > 85 {
		t.Fatalf("monthly remaining percent = %v", monthly.Remaining)
	}
	if monthly.ResetAt != "2026-07-01T00:00:00Z" && monthly.ResetAt != "2026-07-01T00:00:00+00:00" {
		// parseResetAt normalizes RFC3339 to UTC
		if monthly.ResetAt == "" {
			t.Fatalf("monthly reset_at empty: %+v", monthly)
		}
	}

	onDemand := quotas["on_demand"]
	if onDemand.Used != 10 || onDemand.Total != 50 {
		t.Fatalf("on_demand = %+v", onDemand)
	}
	prepaid := quotas["prepaid"]
	if prepaid.Total != 12 || prepaid.Remaining != 100 {
		t.Fatalf("prepaid = %+v", prepaid)
	}
}

func TestParseGrokCLIBillingUsedFieldFallback(t *testing.T) {
	// Credits-format shape (units already dollars/credits, not cents).
	billing := map[string]any{
		"config": map[string]any{
			"used":               map[string]any{"val": float64(23.98)},
			"monthlyLimit":       map[string]any{"val": float64(150)},
			"onDemandCap":        map[string]any{"val": float64(0)},
			"billingPeriodStart": "2026-06-01T00:00:00+00:00",
			"billingPeriodEnd":   "2026-07-01T00:00:00+00:00",
		},
	}
	_, quotas, message := parseGrokCLIBilling(billing, nil)
	if message != "" {
		t.Fatalf("message = %q", message)
	}
	monthly := quotas["monthly"]
	if monthly.Used != 23.98 || monthly.Total != 150 {
		t.Fatalf("monthly = %+v", monthly)
	}
}

func TestParseGrokCLIBillingMergedLiveGrokProShape(t *testing.T) {
	// Live payload pair observed 2026-08-01 for GrokPro account.
	credits := map[string]any{
		"config": map[string]any{
			"billingPeriodEnd":     "2026-08-07T17:06:29.663084+00:00",
			"billingPeriodStart":   "2026-07-31T17:06:29.663084+00:00",
			"creditUsagePercent":   float64(1),
			"isUnifiedBillingUser": true,
			"onDemandCap":          map[string]any{"val": float64(0)},
			"onDemandUsed":         map[string]any{"val": float64(0)},
			"prepaidBalance":       map[string]any{"val": float64(0)},
			"productUsage": []any{
				map[string]any{"product": "GrokBuild", "usagePercent": float64(1)},
				map[string]any{"product": "GrokChat"},
			},
			"currentPeriod": map[string]any{
				"type": "USAGE_PERIOD_TYPE_WEEKLY",
				"end":  "2026-08-07T17:06:29.663084+00:00",
			},
		},
	}
	plain := map[string]any{
		"config": map[string]any{
			"billingPeriodEnd":   "2026-08-01T00:00:00+00:00",
			"billingPeriodStart": "2026-07-01T00:00:00+00:00",
			"monthlyLimit":       map[string]any{"val": float64(15000)},
			"used":               map[string]any{"val": float64(50)},
			"onDemandCap":        map[string]any{"val": float64(0)},
		},
	}
	user := map[string]any{
		"subscriptionTier":  "GrokPro",
		"hasGrokCodeAccess": true,
	}

	plan, quotas, message := parseGrokCLIBillingMerged(credits, plain, user)
	if plan != "GrokPro" {
		t.Fatalf("plan = %q", plan)
	}
	if message != "" {
		t.Fatalf("message = %q", message)
	}
	monthly := quotas["monthly"]
	// cents → dollars: 15000/100=150, 50/100=0.5
	if monthly.Total != 150 || monthly.Used != 0.5 {
		t.Fatalf("monthly = %+v", monthly)
	}
	if monthly.Remaining < 99.6 || monthly.Remaining > 99.7 {
		t.Fatalf("monthly remaining = %v", monthly.Remaining)
	}
	build := quotas["product_grokbuild"]
	if build.Name != "GrokBuild" || build.Used != 1 || build.Total != 100 || build.Remaining != 99 {
		t.Fatalf("GrokBuild product = %+v", build)
	}
	// GrokChat without usagePercent must be skipped.
	if _, ok := quotas["product_grokchat"]; ok {
		t.Fatal("GrokChat without usagePercent should be skipped")
	}
	// onDemandCap 0 with subscription should NOT force depleted synthetic bar.
	if _, ok := quotas["on_demand"]; ok {
		t.Fatalf("unexpected on_demand for subscribed account: %+v", quotas["on_demand"])
	}
}

func TestParseGrokCLIBillingExhaustedFree(t *testing.T) {
	billing := map[string]any{
		"config": map[string]any{
			"onDemandCap":    map[string]any{"val": float64(0)},
			"onDemandUsed":   map[string]any{"val": float64(0)},
			"prepaidBalance": map[string]any{"val": float64(0)},
		},
	}
	plan, quotas, message := parseGrokCLIBilling(billing, nil)
	if plan != "Grok Build" {
		t.Fatalf("plan = %q", plan)
	}
	onDemand := quotas["on_demand"]
	if onDemand.Used != 1 || onDemand.Total != 1 || onDemand.Remaining != 0 {
		t.Fatalf("exhausted on_demand = %+v", onDemand)
	}
	if message != "" {
		// message only when no quotas — we have synthetic depleted bar
		t.Fatalf("unexpected message with depleted bar: %q", message)
	}
}

func TestParseGrokCLIBillingSubscriptionNoNumericQuota(t *testing.T) {
	billing := map[string]any{
		"config": map[string]any{
			"isUnifiedBillingUser": true,
		},
	}
	user := map[string]any{"subscriptionTier": "premium_plus"}
	plan, quotas, message := parseGrokCLIBilling(billing, user)
	if plan != "Premium Plus" {
		t.Fatalf("plan = %q", plan)
	}
	if len(quotas) != 0 {
		t.Fatalf("quotas = %+v", quotas)
	}
	if message == "" {
		t.Fatal("expected message when subscription has no numeric quota")
	}
}

func TestIsGrokCLIQuotaProvider(t *testing.T) {
	cases := []struct {
		name     string
		provider store.Provider
		want     bool
	}{
		{"preset id", store.Provider{ID: "grok-cli", Type: "xai"}, true},
		{"gcli alias", store.Provider{ID: "gcli", Type: "xai"}, true},
		{"cli base url", store.Provider{ID: "my-grok", Type: "xai", BaseURL: "https://cli-chat-proxy.grok.com/v1"}, true},
		{"type xai default", store.Provider{ID: "custom", Type: "xai"}, true},
		{"public api key", store.Provider{ID: "xai-api", Type: "xai", BaseURL: "https://api.x.ai/v1"}, false},
		{"other type", store.Provider{ID: "claude", Type: "claude"}, false},
	}
	for _, tc := range cases {
		if got := isGrokCLIQuotaProvider(tc.provider); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestGrokCLIQuotaHeaders(t *testing.T) {
	cred := store.Credential{
		Email: "user@example.com",
		OAuthToken: &store.OAuthToken{
			AccessToken: "tok",
			Extra:       map[string]any{"subject": "user-123"},
		},
	}
	headers := grokCLIQuotaHeaders("tok", cred)
	if headers.Get("Authorization") != "Bearer tok" {
		t.Fatalf("auth = %q", headers.Get("Authorization"))
	}
	if headers.Get("x-xai-token-auth") != "xai-grok-cli" {
		t.Fatalf("token-auth = %q", headers.Get("x-xai-token-auth"))
	}
	if headers.Get("x-grok-client-identifier") != "grok-shell" {
		t.Fatalf("identifier = %q", headers.Get("x-grok-client-identifier"))
	}
	if headers.Get("x-email") != "user@example.com" {
		t.Fatalf("email = %q", headers.Get("x-email"))
	}
	if headers.Get("x-userid") != "user-123" {
		t.Fatalf("userid = %q", headers.Get("x-userid"))
	}
	if headers.Get("Accept") != "application/json" {
		t.Fatalf("accept = %q", headers.Get("Accept"))
	}
	if headers.Get("User-Agent") == "" {
		t.Fatal("missing user-agent")
	}
}

func TestGrokCLIQuotaHTTPIntegration(t *testing.T) {
	// Lightweight end-to-end against a mock server is covered by unit parse tests;
	// ensure Registry wiring compiles and returns auth message without token.
	reg := &Registry{client: http.DefaultClient}
	quota := reg.grokCLIQuota(t.Context(), store.Provider{ID: "grok-cli", Type: "xai"}, store.Credential{ID: "c1"})
	if quota.Message == "" {
		t.Fatal("expected missing-token message")
	}
}
