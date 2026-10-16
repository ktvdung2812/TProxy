package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/store"
)

func TestCapacityUsesDistinctEligibleSourcesWithoutScheduling(t *testing.T) {
	off := false
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "p", Type: "openai-compatible", Enabled: true, Limits: config.LimitPolicy{RequestsPerMinute: 60, ConcurrentStreams: 6, MaxInputBytes: 1024, MaxOutputTokens: 100, BudgetUSDPerDay: 10, MediaJobs: 2}, Credentials: []config.CredentialConfig{
				{ID: "a", AuthType: "none", Metadata: map[string]any{"max_concurrent_requests": 3}},
				{ID: "b", AuthType: "none", Metadata: map[string]any{"max_concurrent_requests": 5}},
				{ID: "off", AuthType: "none", Enabled: &off},
				{ID: "expired", AuthType: "none", Metadata: map[string]any{"account_expires_at": "2000-01-01T00:00:00Z"}},
				{ID: "unauthorized", AuthType: "api_key"},
			}},
			{ID: "q", Type: "openai-compatible", Enabled: true, Limits: config.LimitPolicy{RequestsPerMinute: 30, MaxInputBytes: 2048, MaxOutputTokens: 200, BudgetUSDPerDay: 5, MediaJobs: 1}, Credentials: []config.CredentialConfig{{ID: "c", AuthType: "none", Metadata: map[string]any{"max_concurrent_requests": 2}}}},
		},
		Models: []config.PublicModelConfig{{ID: "m", Enabled: true, Capabilities: []string{"text", "video-output"}, Routes: []config.RouteTargetConfig{
			{ID: "p1", Provider: "p", UpstreamModel: "up"}, {ID: "p2", Provider: "p", UpstreamModel: "up"}, {ID: "q1", Provider: "q", UpstreamModel: "up"},
		}}},
	}
	data := openTestStore(t, cfg)
	r := New(data, providers.NewRegistry())
	key := &store.APIKey{ID: "key", Models: []string{"m"}}
	before := len(r.rotation)
	snapshot, err := r.Capacity(context.Background(), key, "m")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.EligibleAccountCount != 3 || len(r.rotation) != before || len(r.AccountRuntime()) != 0 {
		t.Fatalf("capacity changed scheduling or counted unavailable accounts: %+v", snapshot)
	}
	for name, want := range map[string]float64{"requests_per_minute": 90, "concurrent_streams": 8, "concurrent_requests": 10, "max_input_bytes": 2048, "max_output_tokens": 200, "budget_usd_per_day": 15, "media_jobs": 3} {
		limit := snapshot.EffectiveLimits[name]
		if limit.State != "finite" || limit.Value == nil || *limit.Value != want {
			t.Errorf("%s = %+v, want %v", name, limit, want)
		}
	}
	if err := data.SetCooldown(context.Background(), "c", "upstream_rate_limited", "busy", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	key.Policy.CredentialIDs = []string{"b", "c"}
	snapshot, err = r.Capacity(context.Background(), key, "m")
	if err != nil || snapshot.EligibleAccountCount != 1 || *snapshot.EffectiveLimits["concurrent_streams"].Value != 5 {
		t.Fatalf("restricted pool = %+v, %v", snapshot, err)
	}
	key.Policy.CredentialIDs = []string{"gone"}
	snapshot, err = r.Capacity(context.Background(), key, "m")
	if err != nil || snapshot.EffectiveLimits["requests_per_minute"].State != "unavailable" {
		t.Fatalf("empty pool = %+v, %v", snapshot, err)
	}
	key.Models = []string{"p:direct"}
	key.Policy.CredentialIDs = []string{"a"}
	snapshot, err = r.Capacity(context.Background(), key, "p:direct")
	if err != nil || snapshot.EligibleAccountCount != 1 {
		t.Fatalf("direct route = %+v, %v", snapshot, err)
	}
}

func TestPoolLimitUnboundedAndPerRequestSemantics(t *testing.T) {
	a := NewEffectiveLimit(10, 3, "a")
	b := NewEffectiveLimit(20, 5, "b")
	sum, perRequest := poolLimit([]EffectiveLimit{a, b}, false), poolLimit([]EffectiveLimit{a, b}, true)
	if *sum.Value != 30 || *sum.Remaining != 22 || *perRequest.Value != 20 || perRequest.Remaining != nil {
		t.Fatal(sum, perRequest)
	}
	unknown := EffectiveLimit{State: "unknown", Sources: []string{"b"}}
	if poolLimit([]EffectiveLimit{a, unknown}, false).State != "unknown" {
		t.Fatal("unknown capacity became a finite sum")
	}
	unbounded := poolLimit([]EffectiveLimit{a, NewEffectiveLimit(0, 0, "b")}, false)
	if unbounded.State != "unbounded" || unbounded.Value != nil {
		t.Fatal(unbounded)
	}
	bounded := TightenLimit(unbounded, a)
	if bounded.State != "finite" || *bounded.Value != 10 {
		t.Fatal(bounded)
	}
	if TightenLimit(poolLimit(nil, false), a).State != "unavailable" {
		t.Fatal("empty pool acquired capacity from a key limit")
	}
}

func TestProviderRPMIsSharedAtomicAndCountsRetries(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		if body["max_tokens"] != float64(7) {
			t.Errorf("default output cap missing: %v", body["max_tokens"])
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"ok","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "p", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true, Limits: config.LimitPolicy{RequestsPerMinute: 3, MaxOutputTokens: 7}, Credentials: []config.CredentialConfig{{ID: "a", AuthType: "none"}, {ID: "b", AuthType: "none"}}}}, Models: []config.PublicModelConfig{{ID: "m", Enabled: true, Routes: []config.RouteTargetConfig{{ID: "r", Provider: "p", UpstreamModel: "up"}}}}}
	data := openTestStore(t, cfg)
	r := New(data, providers.NewRegistry())
	model, err := r.Resolve(context.Background(), "m", nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var admitted atomic.Int32
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			request := canonical.Request{RequestID: fmt.Sprint(i), Source: canonical.ProtocolOpenAI, Raw: map[string]any{"messages": []any{}}, Metadata: map[string]any{"client_api_key_id": fmt.Sprint(i % 2)}}
			_, err := r.Execute(context.Background(), *model, request)
			if err == nil {
				admitted.Add(1)
			} else if providers.Code(err) != "provider_limit_exceeded" {
				t.Errorf("unexpected rejection: %v", err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 3 || admitted.Load() != 3 {
		t.Fatalf("calls=%d admitted=%d", calls.Load(), admitted.Load())
	}
	for _, id := range []string{"a", "b"} {
		credential, _ := data.CredentialByID(context.Background(), id)
		if !credential.CooldownUntil.IsZero() {
			t.Fatalf("local cap applied upstream cooldown to %s", id)
		}
	}
	for _, state := range r.AccountRuntime() {
		if state.InFlight != 0 {
			t.Fatal("leaked account slot")
		}
	}
	// The same limiter reserves again for retries, before a usage row exists.
	p, _ := data.Provider(context.Background(), "p")
	r.limitsMu.Lock()
	r.providerRequests["p"] = []time.Time{time.Now().Add(-2 * time.Minute)}
	r.limitsMu.Unlock()
	for range 3 {
		if err := r.admitProviderRequest(context.Background(), *p, canonical.Request{}); err != nil {
			t.Fatal(err)
		}
	}
	err = r.admitProviderRequest(context.Background(), *p, canonical.Request{})
	if providers.RetryAfter(err) == "" {
		t.Fatal("RPM rejection lacks retry time")
	}
}

func TestDefaultOutputLimitDoesNotMutateFallbackPayload(t *testing.T) {
	raw := map[string]any{"max_tokens": 100, "messages": []any{}}
	request := canonical.Request{Source: canonical.ProtocolOpenAI, Raw: raw, MaxTokens: 100, Metadata: map[string]any{DefaultOutputLimitMetadataKey: true}}
	small := requestForProvider(request, store.Provider{Limits: config.LimitPolicy{MaxOutputTokens: 10}})
	large := requestForProvider(request, store.Provider{Limits: config.LimitPolicy{MaxOutputTokens: 50}})
	if small.MaxTokens != 10 || large.MaxTokens != 50 || !reflect.DeepEqual(raw, request.Raw) || raw["max_tokens"] != 100 {
		t.Fatal("fallback mutated the original cap")
	}
}

func TestLocalProviderLimitPreservesFallbackAndHealth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"ok","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	data := openTestStore(t, &config.Config{Providers: []config.ProviderConfig{
		{ID: "busy", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true, Limits: config.LimitPolicy{RequestsPerMinute: 1}, Credentials: []config.CredentialConfig{{ID: "a", AuthType: "none"}}},
		{ID: "free", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "b", AuthType: "none"}}},
	}, Models: []config.PublicModelConfig{{ID: "m", Enabled: true, Routes: []config.RouteTargetConfig{{ID: "r1", Provider: "busy", UpstreamModel: "up", Priority: 10}, {ID: "r2", Provider: "free", UpstreamModel: "up", Priority: 1}}}}})
	r := New(data, providers.NewRegistry())
	r.ConfigureRouting(config.RoutingConfig{Strategy: "fill-first", Retry: config.RetryConfig{MaxCredentials: 1}})
	p, _ := data.Provider(context.Background(), "busy")
	if err := r.admitProviderRPM(context.Background(), *p); err != nil {
		t.Fatal(err)
	}
	m, _ := r.Resolve(context.Background(), "m", nil)
	result, err := r.Execute(context.Background(), *m, canonical.Request{Source: canonical.ProtocolOpenAI})
	if err != nil || result.Selection.Provider.ID != "free" {
		t.Fatalf("fallback=%+v %v", result, err)
	}
	for _, account := range r.AccountRuntime() {
		if account.InFlight != 0 || (account.CredentialID == "a" && account.Samples != 0) {
			t.Fatalf("local rejection changed health or leaked: %+v", account)
		}
	}
}

func TestCapacityComboAccessAndMediaCompatibility(t *testing.T) {
	data := openTestStore(t, &config.Config{Providers: []config.ProviderConfig{
		{ID: "p", Type: "openai-compatible", Enabled: true, Limits: config.LimitPolicy{RequestsPerMinute: 10, MediaJobs: 2}, Credentials: []config.CredentialConfig{{ID: "a", AuthType: "none"}}},
		{ID: "other", Type: "openai-compatible", Enabled: true, Limits: config.LimitPolicy{RequestsPerMinute: 100}, Credentials: []config.CredentialConfig{{ID: "b", AuthType: "none"}}},
	}, Models: []config.PublicModelConfig{{ID: "m", Enabled: true, Capabilities: []string{"text"}, Routes: []config.RouteTargetConfig{{ID: "r1", Provider: "p", UpstreamModel: "up"}, {ID: "r2", Provider: "other", UpstreamModel: "up", Conditions: map[string]any{"team": "other"}}}}}, Combos: []config.ComboConfig{{ID: "combo", Enabled: true, Capabilities: []string{"text"}, Items: []config.ComboItemConfig{{PublicModelID: "m"}, {PublicModelID: "m"}, {PublicModelID: "p:up"}}}}})
	r := New(data, providers.NewRegistry())
	key := &store.APIKey{ID: "key", Models: []string{"combo"}, Policy: config.ClientKeyPolicy{Team: "team"}}
	snapshot, err := r.Capacity(context.Background(), key, "combo")
	if err != nil || snapshot.EligibleAccountCount != 1 || *snapshot.EffectiveLimits["requests_per_minute"].Value != 10 || snapshot.EffectiveLimits["media_jobs"].State != "unavailable" {
		t.Fatalf("combo capacity=%+v, %v", snapshot, err)
	}
	if len(r.rotation) != 0 {
		t.Fatal("combo inspection changed rotation")
	}
}

func TestSourceStreamCapacityReleasedOnCancellation(t *testing.T) {
	started := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		w.(http.Flusher).Flush()
		started <- struct{}{}
		<-req.Context().Done()
	}))
	defer upstream.Close()
	data := openTestStore(t, &config.Config{Providers: []config.ProviderConfig{{ID: "p", Type: "openai-compatible", Enabled: true, BaseURL: upstream.URL, Limits: config.LimitPolicy{ConcurrentStreams: 1}, Credentials: []config.CredentialConfig{{ID: "a", AuthType: "none", Metadata: map[string]any{"max_concurrent_requests": 2}}}}}, Models: []config.PublicModelConfig{{ID: "m", Enabled: true, Routes: []config.RouteTargetConfig{{ID: "r", Provider: "p", UpstreamModel: "up"}}}}})
	r := New(data, providers.NewRegistry())
	m, _ := r.Resolve(context.Background(), "m", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := r.ExecuteStream(ctx, *m, canonical.Request{Source: canonical.ProtocolOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := r.ExecuteStream(context.Background(), *m, canonical.Request{Source: canonical.ProtocolOpenAI}); providers.Code(err) != "provider_concurrency_limit" {
		t.Fatalf("stream cap=%v", err)
	}
	cancel()
	for range stream.Events {
	}
	if r.AccountRuntime()[0].InFlight != 0 {
		t.Fatal("cancel leaked an account slot")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.providerStreams["p"] != 0 {
		t.Fatal("cancel leaked a provider stream")
	}
}
