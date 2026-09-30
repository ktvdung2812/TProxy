package providers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
			"monthlyLimit":         map[string]any{"val": float64(150)},
			"includedUsed":         map[string]any{"val": float64(23.98)},
			"onDemandCap":          map[string]any{"val": float64(50)},
			"onDemandUsed":         map[string]any{"val": float64(10)},
			"prepaidBalance":       map[string]any{"val": float64(12)},
			"isUnifiedBillingUser": true,
			"billingPeriodEnd":     "2026-07-01T00:00:00+00:00",
		},
	}
	user := map[string]any{
		"subscriptionTier":  "super_grok",
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

	plan, quotas, message := parseGrokCLIBillingMerged(credits, plain, user, nil)
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
	// A subscribed account that is not on unified billing and exposes no
	// numeric fields still gets an explanatory message rather than windows.
	billing := map[string]any{
		"config": map[string]any{
			"billingPeriodEnd": "2026-07-01T00:00:00+00:00",
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

// rewriteTransport points the hardcoded cli-chat-proxy URLs at a test server.
type grokRewriteTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (t *grokRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = t.target.Scheme
	clone.URL.Host = t.target.Host
	return t.base.RoundTrip(clone)
}

// End-to-end: all four endpoints are fetched, the settings display name wins,
// and the renewal date comes out of the weekly pool reset.
func TestGrokCLIQuotaEndToEnd(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/billing", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("format") == "credits" {
			fmt.Fprint(w, `{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","end":"2026-09-25T17:06:29.663084+00:00"},"onDemandCap":{"val":0},"onDemandUsed":{"val":0},"isUnifiedBillingUser":true,"prepaidBalance":{"val":0},"topUpMethod":"TOP_UP_METHOD_SAVED_PAYMENT_METHOD","billingPeriodEnd":"2026-09-25T17:06:29.663084+00:00"}}`)
			return
		}
		fmt.Fprint(w, `{"config":{"monthlyLimit":{"val":0},"used":{"val":205},"onDemandCap":{"val":0},"billingPeriodEnd":"2026-10-01T00:00:00+00:00"}}`)
	})
	mux.HandleFunc("/v1/user", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"userId":"u-1","principalType":"User","subscriptionTier":"GrokPro","hasGrokCodeAccess":true}`)
	})
	mux.HandleFunc("/v1/settings", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"subscription_tier_display":"SuperGrok","default_model":"grok-4.6"}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	target, _ := url.Parse(server.URL)

	reg := &Registry{client: &http.Client{Transport: &grokRewriteTransport{target: target, base: http.DefaultTransport}}}
	quota := reg.grokCLIQuota(t.Context(),
		store.Provider{ID: "grok-cli", Type: "xai"},
		store.Credential{ID: "c1", Secret: "tok"})

	if quota.Plan != "SuperGrok" {
		t.Fatalf("plan = %q", quota.Plan)
	}
	if quota.Message != "" {
		t.Fatalf("message = %q", quota.Message)
	}
	weekly, ok := quota.Quotas["weekly"]
	if !ok || weekly.Used != 0 || weekly.Remaining != 100 {
		t.Fatalf("weekly = %+v (present=%v)", weekly, ok)
	}
	if quota.RenewsAt == "" {
		t.Fatal("renews_at empty")
	}
}

// Grok's productUsage entries are slices of one weekly allowance, not separate
// allowances. The x.ai usage page for this account showed a single weekly bar
// at 98% used, split Build 95% / Voice 2% / Chat 1%.
func TestParseGrokCLIBillingMergedAggregatesProductsIntoWeekly(t *testing.T) {
	credits := map[string]any{
		"config": map[string]any{
			"productUsage": []any{
				map[string]any{"product": "GrokBuild", "usagePercent": float64(95)},
				map[string]any{"product": "GrokVoice", "usagePercent": float64(2)},
				map[string]any{"product": "GrokChat", "usagePercent": float64(1)},
			},
			"currentPeriod": map[string]any{
				"type": "USAGE_PERIOD_TYPE_WEEKLY",
				"end":  "2026-08-15T00:06:00+00:00",
			},
		},
	}

	_, quotas, _ := parseGrokCLIBillingMerged(credits, nil, map[string]any{"subscriptionTier": "GrokPro"}, nil)

	weekly, ok := quotas["weekly"]
	if !ok {
		t.Fatalf("no aggregate weekly window: %+v", quotas)
	}
	if weekly.Used != 98 || weekly.Total != 100 || weekly.Remaining != 2 {
		t.Fatalf("weekly = %+v, want 98 used / 2 remaining", weekly)
	}

	// The breakdown stays visible…
	if build := quotas["product_grokbuild"]; build.Used != 95 {
		t.Fatalf("build breakdown = %+v", build)
	}
	// …but must not gate routing, or a 1%-used Chat bar would report this
	// nearly exhausted account as free.
	for key := range quotas {
		if strings.HasPrefix(key, grokProductBreakdownPrefix) && quotaKeyAffectsRouting("grok-cli", key) {
			t.Fatalf("breakdown key %q gates routing", key)
		}
	}
	if !quotaKeyAffectsRouting("grok-cli", "weekly") {
		t.Fatal("grok weekly window must gate routing: it is the account's real limit")
	}
}

// The generic rule treats weekly as auxiliary because Codex and Claude gate on
// their session window; Grok has none, so it must not inherit that.
func TestGrokWeeklyGatesRoutingUnlikeOtherProviders(t *testing.T) {
	if quotaKeyAffectsRouting("codex", "weekly") || quotaKeyAffectsRouting("claude", "weekly") {
		t.Fatal("weekly should stay auxiliary for session-based providers")
	}
	for _, providerType := range []string{"xai", "grok-cli"} {
		if !quotaKeyAffectsRouting(providerType, "weekly") {
			t.Fatalf("%s weekly must gate routing", providerType)
		}
	}
}

// Live payload observed 2026-09-19 for a SuperGrok account at 0% weekly usage:
// the credits response drops productUsage and creditUsagePercent entirely, so
// the old parser reported "no numeric quota" for a perfectly healthy account.
func TestParseGrokCLIBillingMergedZeroUsageUnifiedWeek(t *testing.T) {
	credits := map[string]any{
		"config": map[string]any{
			"currentPeriod": map[string]any{
				"type":  "USAGE_PERIOD_TYPE_WEEKLY",
				"start": "2026-09-18T17:06:29.663084+00:00",
				"end":   "2026-09-25T17:06:29.663084+00:00",
			},
			"onDemandCap":          map[string]any{"val": float64(0)},
			"onDemandUsed":         map[string]any{"val": float64(0)},
			"isUnifiedBillingUser": true,
			"prepaidBalance":       map[string]any{"val": float64(0)},
			"topUpMethod":          "TOP_UP_METHOD_SAVED_PAYMENT_METHOD",
			"billingPeriodStart":   "2026-09-18T17:06:29.663084+00:00",
			"billingPeriodEnd":     "2026-09-25T17:06:29.663084+00:00",
		},
	}
	plain := map[string]any{
		"config": map[string]any{
			"monthlyLimit":       map[string]any{"val": float64(0)},
			"used":               map[string]any{"val": float64(205)},
			"onDemandCap":        map[string]any{"val": float64(0)},
			"billingPeriodStart": "2026-09-01T00:00:00+00:00",
			"billingPeriodEnd":   "2026-10-01T00:00:00+00:00",
		},
	}
	user := map[string]any{
		"subscriptionTier":  "GrokPro",
		"hasGrokCodeAccess": true,
		"principalType":     "User",
	}
	settings := map[string]any{"subscription_tier_display": "SuperGrok"}

	plan, quotas, message := parseGrokCLIBillingMerged(credits, plain, user, settings)

	// The settings display name beats the internal tier code.
	if plan != "SuperGrok" {
		t.Fatalf("plan = %q", plan)
	}
	if message != "" {
		t.Fatalf("message = %q", message)
	}
	// Zero usage still means a full weekly pool, not a missing meter.
	weekly, ok := quotas["weekly"]
	if !ok {
		t.Fatalf("expected weekly pool window: %+v", quotas)
	}
	if weekly.Used != 0 || weekly.Total != 100 || weekly.Remaining != 100 {
		t.Fatalf("weekly = %+v", weekly)
	}
	if weekly.ResetAt == "" {
		t.Fatal("weekly reset_at empty")
	}
}

// creditUsagePercent is the combined pool meter (API + Build + Chat); the
// productUsage sum can miss unlisted products, so the combined field wins.
func TestParseGrokCLIBillingMergedCombinedPercentPreferred(t *testing.T) {
	credits := map[string]any{
		"config": map[string]any{
			"creditUsagePercent": float64(75),
			"productUsage": []any{
				map[string]any{"product": "GrokBuild", "usagePercent": float64(70)},
			},
			"currentPeriod": map[string]any{
				"type": "USAGE_PERIOD_TYPE_WEEKLY",
				"end":  "2026-08-15T00:00:00+00:00",
			},
		},
	}
	_, quotas, _ := parseGrokCLIBillingMerged(credits, nil, nil, nil)
	weekly := quotas["weekly"]
	if weekly.Used != 75 {
		t.Fatalf("weekly used = %v, want combined 75 not product sum 70", weekly.Used)
	}
	if build := quotas["product_grokbuild"]; build.Used != 70 {
		t.Fatalf("build breakdown = %+v", build)
	}
}

// The pool label follows currentPeriod.type rather than assuming weekly.
func TestParseGrokCLIBillingMergedPeriodLabels(t *testing.T) {
	cases := []struct {
		periodType string
		wantName   string
	}{
		{"USAGE_PERIOD_TYPE_DAILY", "Daily limit"},
		{"USAGE_PERIOD_TYPE_MONTHLY", "Monthly limit"},
		{"USAGE_PERIOD_TYPE_YEARLY", "Usage limit"},
	}
	for _, tc := range cases {
		credits := map[string]any{
			"config": map[string]any{
				"creditUsagePercent": float64(10),
				"currentPeriod": map[string]any{
					"type": tc.periodType,
					"end":  "2026-08-15T00:00:00+00:00",
				},
			},
		}
		_, quotas, _ := parseGrokCLIBillingMerged(credits, nil, nil, nil)
		if got := quotas["weekly"].Name; got != tc.wantName {
			t.Fatalf("%s: pool name = %q, want %q", tc.periodType, got, tc.wantName)
		}
	}
}

// Team principals have no personal usage surface; the message should say so
// instead of blaming a missing quota.
func TestParseGrokCLIBillingMergedTeamPrincipal(t *testing.T) {
	user := map[string]any{
		"principalType": "Team",
		"teamId":        "team-1",
	}
	_, quotas, message := parseGrokCLIBillingMerged(
		map[string]any{"config": map[string]any{}}, nil, user, nil)
	if len(quotas) != 0 {
		t.Fatalf("quotas = %+v", quotas)
	}
	if !strings.Contains(message, "team") {
		t.Fatalf("message = %q", message)
	}
}

// A depleted pool with auto top-up armed should say so — the account may keep
// serving paid usage even though the included window reads 0%.
func TestParseGrokCLIBillingMergedTopUpDepletedMessage(t *testing.T) {
	credits := map[string]any{
		"config": map[string]any{
			"creditUsagePercent": float64(100),
			"topUpMethod":        "TOP_UP_METHOD_SAVED_PAYMENT_METHOD",
			"currentPeriod": map[string]any{
				"type": "USAGE_PERIOD_TYPE_WEEKLY",
				"end":  "2026-08-15T00:00:00+00:00",
			},
		},
	}
	_, quotas, message := parseGrokCLIBillingMerged(credits, nil, nil, nil)
	if weekly := quotas["weekly"]; weekly.Remaining != 0 {
		t.Fatalf("weekly = %+v", weekly)
	}
	if !strings.Contains(message, "top-up") {
		t.Fatalf("message = %q", message)
	}
}

// Tier display is preferred over raw tier codes; snake_case codes are still
// humanized when settings is unavailable.
func TestResolveGrokCLIPlanSources(t *testing.T) {
	user := map[string]any{"subscriptionTier": "super_grok"}
	settings := map[string]any{"subscription_tier_display": "SuperGrok Heavy"}
	if plan := resolveGrokCLIPlan(user, nil, settings); plan != "SuperGrok Heavy" {
		t.Fatalf("settings display should win, got %q", plan)
	}
	if plan := resolveGrokCLIPlan(user, nil, nil); plan != "Super Grok" {
		t.Fatalf("tier fallback = %q", plan)
	}
	settings = map[string]any{"subscription_tier_display": "null"}
	if plan := resolveGrokCLIPlan(user, nil, settings); plan != "Super Grok" {
		t.Fatalf("null display should fall back to tier, got %q", plan)
	}
}
