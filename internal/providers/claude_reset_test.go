package providers

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/store"
)

func claudeResetTestCredential() (store.Provider, store.Credential) {
	return store.Provider{ID: "claude", Type: "claude"},
		store.Credential{ID: "cred-reset", ProviderID: "claude", AuthType: "oauth", Secret: "sk-ant-oat01-test"}
}

// Shape captured from /api/oauth/usage?at_wall=1 for a Pro account holding a
// launch grant; the dashboard must list it as claimable.
func TestParseClaudeLimitResets(t *testing.T) {
	var payload map[string]any
	raw := `{
		"cedar_ember": {
			"eligible": true, "ineligible_reason": null, "at_limit": false, "exhausted": [],
			"grants": [{
				"id": "opus55-launch-promax-20260921",
				"label": "Claude Opus 5.5 launch: one usage-limit reset for Pro and Max",
				"resets_total": 1, "resets_left": 1,
				"starts_at": "2026-09-22T16:00:00+00:00", "ends_at": "2026-10-22T16:00:00+00:00",
				"clears": ["five_hour", "seven_day", "seven_day_overage_included"],
				"paused": false, "usable_now": true, "use_requires_limit": false
			}, {"label": "no id"}],
			"next_grant_id": "opus55-launch-promax-20260921",
			"weekly_resets_at": "2026-10-08T05:00:00+00:00", "cooldown_until": null
		},
		"juniper_tide": {
			"eligible": false, "ineligible_reason": "not_at_wall", "available": false,
			"next_available_at": null, "resets_per_week": 1
		}
	}`
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	got := parseClaudeLimitResets(payload)
	if !got.Eligible || got.NextGrantID != "opus55-launch-promax-20260921" || len(got.Grants) != 1 {
		t.Fatalf("resets = %+v", got)
	}
	grant := got.Grants[0]
	if !grant.UsableNow || grant.ResetsLeft != 1 || grant.EndsAt != "2026-10-22T16:00:00Z" || len(grant.Clears) != 3 {
		t.Fatalf("grant = %+v", grant)
	}
	if got.Session == nil || got.Session.Eligible || got.Session.IneligibleReason != "not_at_wall" || got.Session.ResetsPerWeek != 1 {
		t.Fatalf("session = %+v", got.Session)
	}
}

// An ineligible account carries a null session block and a next_grant_id that
// is not in the list; neither may surface as claimable.
func TestParseClaudeLimitResetsIneligible(t *testing.T) {
	payload := map[string]any{
		"cedar_ember":  map[string]any{"eligible": false, "ineligible_reason": "cli_version", "grants": []any{}, "next_grant_id": "ghost"},
		"juniper_tide": nil,
	}
	got := parseClaudeLimitResets(payload)
	if got.Eligible || got.IneligibleReason != "cli_version" || got.NextGrantID != "" || got.Session != nil || got.Grants == nil {
		t.Fatalf("resets = %+v", got)
	}
}

func TestClaimClaudeLimitResetSendsGrantToOrganization(t *testing.T) {
	provider, credential := claudeResetTestCredential()
	var claimBody map[string]string
	var userAgent string
	registry := NewRegistry()
	registry.client = &http.Client{Transport: antigravityQuotaRoundTripper(func(request *http.Request) (*http.Response, error) {
		switch request.URL.String() {
		case claudeProfileEndpoint:
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{"organization": {"uuid": "org-123"}}`))}, nil
		case "https://api.anthropic.com/api/organizations/org-123/reset_rate_limits":
			userAgent = request.Header.Get("User-Agent")
			raw, _ := io.ReadAll(request.Body)
			_ = json.Unmarshal(raw, &claimBody)
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{"result": "reset", "resets_left": 0, "cleared": ["five_hour", "seven_day"]}`))}, nil
		}
		t.Fatalf("unexpected request %s", request.URL)
		return nil, nil
	})}

	result, err := registry.ClaimClaudeLimitReset(t.Context(), provider, credential, ClaudeResetProgramGrant, "grant-1", "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.ResetsLeft == nil || *result.ResetsLeft != 0 || len(result.Cleared) != 2 {
		t.Fatalf("result = %+v", result)
	}
	if claimBody["program"] != "cedar_ember" || claimBody["grant_id"] != "grant-1" || claimBody["request_id"] != "req-1" {
		t.Fatalf("claim body = %+v", claimBody)
	}
	if !strings.Contains(userAgent, claudeResetClientVersion) {
		t.Fatalf("user agent = %q", userAgent)
	}
}

// already_used is an answered claim, not a transport failure: it reports
// OK=false without an error so the dashboard can show why nothing happened.
func TestClaimClaudeLimitResetNotReset(t *testing.T) {
	provider, credential := claudeResetTestCredential()
	registry := NewRegistry()
	registry.client = &http.Client{Transport: antigravityQuotaRoundTripper(func(request *http.Request) (*http.Response, error) {
		body := `{"result": "already_used", "reason": "already_used"}`
		if request.URL.String() == claudeProfileEndpoint {
			body = `{"organization": {"uuid": "org-123"}}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	result, err := registry.ClaimClaudeLimitReset(t.Context(), provider, credential, ClaudeResetProgramSession, "", "")
	if err != nil || result.OK || result.Result != "already_used" {
		t.Fatalf("result = %+v err = %v", result, err)
	}
}

func TestClaimClaudeLimitResetRejectsBadInput(t *testing.T) {
	provider, credential := claudeResetTestCredential()
	registry := NewRegistry()
	if _, err := registry.ClaimClaudeLimitReset(t.Context(), provider, credential, ClaudeResetProgramGrant, "", ""); Status(err) != http.StatusBadRequest {
		t.Fatalf("missing grant err = %v", err)
	}
	if _, err := registry.ClaimClaudeLimitReset(t.Context(), provider, credential, "bogus", "", ""); Status(err) != http.StatusBadRequest {
		t.Fatalf("bad program err = %v", err)
	}
	if _, err := registry.ClaimClaudeLimitReset(t.Context(), store.Provider{Type: "codex"}, credential, ClaudeResetProgramSession, "", ""); Status(err) != http.StatusBadRequest {
		t.Fatalf("codex provider err = %v", err)
	}
}
