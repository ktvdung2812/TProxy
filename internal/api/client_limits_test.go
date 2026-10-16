package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/router"
	"github.com/tproxy/tproxy/internal/store"
)

func TestClientLimitsKeyScopeAndAdmission(t *testing.T) {
	cfg := &config.Config{
		Limits: config.LimitPolicy{RequestsPerMinute: 90},
		Teams:  []config.TeamPolicyConfig{{ID: "team", Limits: config.LimitPolicy{RequestsPerMinute: 80}}},
		Providers: []config.ProviderConfig{{ID: "codex", Type: "codex", Enabled: true,
			Limits: config.LimitPolicy{RequestsPerMinute: 100, MaxOutputTokens: 2048},
			Credentials: []config.CredentialConfig{
				{ID: "allowed", AuthType: "api_key", Secret: "upstream-secret", Metadata: map[string]any{"max_concurrent_requests": 3}},
				{ID: "private-account", AuthType: "api_key", Secret: "other-secret", Metadata: map[string]any{"max_concurrent_requests": 5}},
			}}},
		Models: []config.PublicModelConfig{{ID: "gpt-test", Enabled: true, Routes: []config.RouteTargetConfig{{ID: "r", Provider: "codex", UpstreamModel: "gpt-test"}}}},
	}
	data := apiTestStore(t, cfg)
	if err := data.SaveModelMappingSettings(context.Background(), store.ModelMappingSettings{Models: map[string]string{"mapped-gpt": "gpt-test"}}); err != nil {
		t.Fatal(err)
	}
	policy := config.ClientKeyPolicy{Team: "team", CredentialIDs: []string{"allowed"}, Endpoints: []string{"/v1/responses"}, Limits: config.LimitPolicy{RequestsPerMinute: 1}}
	_, token, err := data.CreateAPIKey(context.Background(), "client", "Client", []string{"gpt-test"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	_, other, err := data.CreateAPIKey(context.Background(), "other", "Other", []string{"gpt-test"}, config.ClientKeyPolicy{CredentialIDs: []string{"allowed"}})
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(cfg, data, router.New(data, providers.NewRegistry()))
	defer s.Close()
	handler := s.Handler()
	read := func(token, path string) router.CapacitySnapshot {
		t.Helper()
		res := limitsRequest(handler, http.MethodGet, path, token, "", "")
		if res.Code != http.StatusOK || res.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("limits: %d %s", res.Code, res.Body.String())
		}
		var result struct {
			Object   string `json:"object"`
			APIKeyID string `json:"api_key_id"`
			router.CapacitySnapshot
		}
		if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Object != "tproxy.limits" || result.APIKeyID == "" || result.ComputedAt.IsZero() {
			t.Fatal("missing response identity or timestamp", res.Body.String())
		}
		for _, private := range []string{"private-account", "upstream-secret", "other-secret", token} {
			if strings.Contains(res.Body.String(), private) {
				t.Fatal("limits leaked unrelated account or credentials")
			}
		}
		return result.CapacitySnapshot
	}
	for range 2 {
		result := read(token, "/v1/limits?model=gpt-test&api_key_id=other")
		rpm, shared := result.EffectiveLimits["requests_per_minute"], result.SharedLimits["requests_per_minute"]
		if rpm.Value == nil || *rpm.Value != 1 || rpm.Remaining == nil || *rpm.Remaining != 1 || shared.Value == nil || *shared.Value != 100 {
			t.Fatal("reading limits spent RPM or ignored the key cap", result)
		}
		if result.EligibleAccountCount != 1 || *result.EffectiveLimits["concurrent_requests"].Value != 3 || result.EffectiveLimits["max_output_tokens"].Remaining != nil {
			t.Fatal("incorrect account scope or per-request limit", result)
		}
		if result.EffectiveLimits["budget_usd_per_day"].State != "unbounded" || len(result.QuotaWindows) != 1 || result.QuotaWindows[0].State != "unknown" || result.QuotaWindows[0].Quota != nil {
			t.Fatal("missing quota was reported as known", result)
		}
	}
	if result := read(token, "/v1/limits?model=mapped-gpt"); result.EligibleAccountCount != 1 {
		t.Fatal("model mapping did not match inference", result)
	}
	if err := data.UpdateAPIKey(context.Background(), "client", "Client", []string{"gpt-test"}, true, config.ClientKeyPolicy{CredentialIDs: policy.CredentialIDs, DisableModelMapping: true, Team: policy.Team, Endpoints: policy.Endpoints, Limits: policy.Limits}); err != nil {
		t.Fatal(err)
	}
	if result := read(token, "/v1/limits?model=mapped-gpt"); result.EligibleAccountCount != 0 {
		t.Fatal("key's mapping opt-out was ignored", result)
	}
	if err := data.UpdateAPIKey(context.Background(), "client", "Client", []string{"gpt-test"}, true, policy); err != nil {
		t.Fatal(err)
	}
	// Invalid generation still consumes ingress RPM, without an upstream call.
	res := limitsRequest(handler, http.MethodPost, "/v1/responses", token, "{", "")
	if res.Code != http.StatusBadRequest {
		t.Fatal(res.Code, res.Body.String())
	}
	res = limitsRequest(handler, http.MethodPost, "/v1/responses", token, "{", "")
	if res.Code != http.StatusTooManyRequests {
		t.Fatal("generation limit not reached", res.Code, res.Body.String())
	}
	if remaining := read(token, "/v1/limits").EffectiveLimits["requests_per_minute"].Remaining; remaining == nil || *remaining != 0 {
		t.Fatal("exhausted key could not inspect its limits", remaining)
	}
	if remaining := read(other, "/v1/limits?model=gpt-test").EffectiveLimits["requests_per_minute"].Remaining; remaining == nil || *remaining != 89 {
		t.Fatal("other key did not share global consumption", remaining)
	}
	for _, model := range []string{"missing", "codex:private-model"} {
		result := read(token, "/v1/limits?model="+model)
		if result.EligibleAccountCount != 0 || len(result.QuotaWindows) != 0 || result.EffectiveLimits["requests_per_minute"].State != "unavailable" {
			t.Fatal("forbidden/missing model exposed capacity", result)
		}
	}
}

func TestClientLimitsRequiresClientKey(t *testing.T) {
	cfg := &config.Config{Server: config.ServerConfig{AllowLocalWithoutKey: true}}
	data := apiTestStore(t, cfg)
	_, token, err := data.CreateAPIKey(context.Background(), "key", "Key", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(cfg, data, router.New(data, providers.NewRegistry()))
	defer s.Close()
	handler := s.Handler()
	for _, tc := range []struct {
		name, method, path, bearer, apiKey string
		status                             int
	}{
		{"anonymous loopback", "GET", "/v1/limits", "", "", 401},
		{"invalid key", "GET", "/v1/limits", "invalid", "", 401},
		{"management credential", "GET", "/v1/limits", testDashboardPassword, "", 401},
		{"conflicting headers", "GET", "/v1/limits", token, "different", 400},
		{"query credential", "GET", "/v1/limits?api_key=invalid", token, "", 400},
		{"write method", "POST", "/v1/limits", token, "", 405},
		{"api key header", "GET", "/v1/limits", "", token, 200},
		{"normalized base URL", "GET", "/v1/v1/limits", token, "", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.RemoteAddr = "127.0.0.1:1234"
			if tc.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			if tc.apiKey != "" {
				req.Header.Set("X-Api-Key", tc.apiKey)
			}
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tc.status {
				t.Fatal(res.Code, res.Body.String())
			}
		})
	}
	if err := data.UpdateAPIKey(context.Background(), "key", "Key", nil, false); err != nil {
		t.Fatal(err)
	}
	if res := limitsRequest(handler, http.MethodGet, "/v1/limits", token, "", ""); res.Code != http.StatusUnauthorized {
		t.Fatal("disabled key accepted", res.Body.String())
	}
}

func TestClientLimitsCodexCachedWindows(t *testing.T) {
	var calls atomic.Int32
	var fail atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path != "/backend-api/wham/usage" {
			fmt.Fprint(w, `{}`)
			return
		}
		if r.Header.Get("Authorization") == "Bearer test-b" {
			fmt.Fprint(w, `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":9,"limit_window_seconds":604800,"reset_at":1792651704},"secondary_window":{"used_percent":0,"limit_window_seconds":0}}}`)
			return
		}
		fmt.Fprint(w, `{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":0,"limit_window_seconds":18000,"reset_at":1792076920},"secondary_window":{"used_percent":9,"limit_window_seconds":604800,"reset_at":1792681720}}}`)
	}))
	defer upstream.Close()
	// NewRegistry clones the default transport. Redirect this registry's TLS
	// dial to the fixture; no Codex account or public network is used by the test.
	previous := http.DefaultTransport
	http.DefaultTransport = &http.Transport{DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	}}
	registry := providers.NewRegistry()
	http.DefaultTransport = previous
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{ID: "codex", Type: "codex", Enabled: true, Credentials: []config.CredentialConfig{{ID: "a", AuthType: "api_key", Secret: "test-a"}, {ID: "b", AuthType: "api_key", Secret: "test-b"}}}},
		Models:    []config.PublicModelConfig{{ID: "gpt-test", Enabled: true, Routes: []config.RouteTargetConfig{{ID: "r", Provider: "codex", UpstreamModel: "gpt-test"}}}},
	}
	data := apiTestStore(t, cfg)
	_, token, err := data.CreateAPIKey(context.Background(), "key", "Key", []string{"gpt-test"})
	if err != nil {
		t.Fatal(err)
	}
	r := router.New(data, registry)
	s := NewServer(cfg, data, r)
	defer s.Close()
	provider, err := data.Provider(context.Background(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		credential, err := data.CredentialByID(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, err = r.CredentialQuota(ctx, *provider, credential)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, failed := range []bool{false, true} {
		if failed {
			fail.Store(true)
			credential, _ := data.CredentialByID(context.Background(), "a")
			_, _ = r.CredentialQuota(context.Background(), *provider, credential)
		}
		before := calls.Load()
		res := limitsRequest(s.Handler(), http.MethodGet, "/v1/limits?model=gpt-test", token, "", "")
		var result router.CapacitySnapshot
		if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if res.Code != 200 || len(result.QuotaWindows) != 2 || calls.Load() != before {
			t.Fatal("limits must use cached per-account windows", res.Body.String(), calls.Load(), before)
		}
		for _, snapshot := range result.QuotaWindows {
			if snapshot.Quota == nil {
				t.Fatal("missing cached Codex quota", snapshot)
			}
			session, weekly := snapshot.Quota.Quotas["session"], snapshot.Quota.Quotas["weekly"]
			if weekly.Total != 100 || weekly.Remaining != 91 || weekly.WindowMinutes != 10080 {
				t.Fatal("Codex windows changed or account percentages were combined", snapshot)
			}
			if snapshot.CredentialID == "a" && (session.Total != 100 || session.Remaining != 100 || session.WindowMinutes != 300 || session.ResetAt != "2026-10-15T15:08:40Z") {
				t.Fatal("five-hour window changed", snapshot)
			}
			if snapshot.CredentialID == "b" && (session.Total != 0 || snapshot.Quota.Quotas["secondary"].Total != 0 || snapshot.State != "unknown") {
				t.Fatal("weekly primary invented a five-hour quota", snapshot)
			}
			if failed && snapshot.CredentialID == "a" && (snapshot.State != "unknown" || !snapshot.Stale) {
				t.Fatal("failed probe did not preserve stale/unknown state", snapshot)
			}
		}
	}
}
