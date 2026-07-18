package router_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/auth"
	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/router"
	"github.com/tproxy/tproxy/internal/security"
	"github.com/tproxy/tproxy/internal/store"
)

func TestFallbackAndPublicModelRewrite(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"quota exhausted"}}`))
	}))
	defer failing.Close()

	success := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "upstream-success" {
			t.Errorf("upstream model = %v", body["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","model":"upstream-success","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`))
	}))
	defer success.Close()

	dataStore := newStore(t, fallbackConfig(failing.URL, success.URL))
	requestRouter := router.New(dataStore, providers.NewRegistry())
	model, err := requestRouter.Resolve(context.Background(), "coder", nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := requestRouter.Execute(context.Background(), *model, canonical.Request{
		RequestID: "req-test",
		Messages:  []canonical.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Response.Model != "td-coder-pro" {
		t.Fatalf("public response model = %q", result.Response.Model)
	}
	if result.Selection.Provider.ID != "success" {
		t.Fatalf("provider = %q", result.Selection.Provider.ID)
	}
	snapshot, err := dataStore.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Usage.Requests != 2 || snapshot.Usage.Errors != 1 {
		t.Fatalf("usage = %+v", snapshot.Usage)
	}
}

func TestDisableFallbackStopsAfterCredentialPreparationFailure(t *testing.T) {
	var secondaryCalls atomic.Int32
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondaryCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"secondary","model":"upstream-success","choices":[{"message":{"role":"assistant","content":"unexpected fallback"},"finish_reason":"stop"}]}`))
	}))
	defer secondary.Close()
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("first provider should not receive a request when credential preparation fails")
	}))
	defer failing.Close()
	cfg := fallbackConfig(failing.URL, secondary.URL)
	cfg.Providers[0].Credentials[0].AuthType = "oauth"
	dataStore := newStore(t, cfg)
	requestRouter := router.New(dataStore, providers.NewRegistry())
	requestRouter.SetCredentialRefresher(failingCredentialRefresher{})
	model, err := requestRouter.Resolve(context.Background(), "coder", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = requestRouter.Execute(context.Background(), *model, canonical.Request{
		RequestID: "no-fallback-prepare",
		Metadata:  map[string]any{"disable_fallback": true},
		Messages:  []canonical.Message{{Role: "user", Content: "hello"}},
	})
	if err == nil || !strings.Contains(err.Error(), "credential preparation failed") {
		t.Fatalf("expected preparation error without fallback, got %v", err)
	}
	if secondaryCalls.Load() != 0 {
		t.Fatalf("disable_fallback dispatched to secondary provider %d times", secondaryCalls.Load())
	}
}

func TestDisableFallbackStopsStreamingCredentialPreparationFailure(t *testing.T) {
	var secondaryCalls atomic.Int32
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondaryCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"secondary\",\"model\":\"upstream-success\",\"choices\":[{\"delta\":{\"content\":\"unexpected fallback\"},\"finish_reason\":null}]}\n\ndata: [DONE]\n\n"))
	}))
	defer secondary.Close()
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("first provider should not receive a request when credential preparation fails")
	}))
	defer failing.Close()
	cfg := fallbackConfig(failing.URL, secondary.URL)
	cfg.Providers[0].Credentials[0].AuthType = "oauth"
	dataStore := newStore(t, cfg)
	requestRouter := router.New(dataStore, providers.NewRegistry())
	requestRouter.SetCredentialRefresher(failingCredentialRefresher{})
	model, err := requestRouter.Resolve(context.Background(), "coder", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = requestRouter.ExecuteStream(context.Background(), *model, canonical.Request{
		RequestID: "no-fallback-stream-prepare",
		Stream:    true,
		Metadata:  map[string]any{"disable_fallback": true},
		Messages:  []canonical.Message{{Role: "user", Content: "hello"}},
	})
	if err == nil || !strings.Contains(err.Error(), "credential preparation failed") {
		t.Fatalf("expected streaming preparation error without fallback, got %v", err)
	}
	if secondaryCalls.Load() != 0 {
		t.Fatalf("disable_fallback dispatched streaming request to secondary provider %d times", secondaryCalls.Load())
	}
}

func TestDisableFallbackStopsRawProxyAfterFirstFailure(t *testing.T) {
	var secondaryCalls atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"first failed"}}`))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondaryCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"upstream-success"}`))
	}))
	defer second.Close()
	cfg := fallbackConfig(first.URL, second.URL)
	dataStore := newStore(t, cfg)
	requestRouter := router.New(dataStore, providers.NewRegistry())
	model, err := requestRouter.Resolve(context.Background(), "coder", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = requestRouter.ProxyWithOptions(context.Background(), *model, "no-fallback-raw", "/v1/images/generations", []byte(`{"model":"coder","prompt":"hello"}`), "application/json", router.RawProxyOptions{
		Method:             http.MethodPost,
		Headers:            make(http.Header),
		RetryNetworkErrors: true,
		DisableFallback:    true,
	})
	if err == nil {
		t.Fatal("expected first raw proxy failure")
	}
	if secondaryCalls.Load() != 0 {
		t.Fatalf("disable_fallback dispatched raw request to secondary provider %d times", secondaryCalls.Load())
	}
}

func TestDisableFallbackStopsRawProxyWhenAdapterLacksRawSupport(t *testing.T) {
	var secondaryCalls atomic.Int32
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondaryCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"upstream-success"}`))
	}))
	defer secondary.Close()
	cfg := fallbackConfig("http://127.0.0.1:1", secondary.URL)
	cfg.Providers[0].Type = "anthropic-compatible"
	dataStore := newStore(t, cfg)
	requestRouter := router.New(dataStore, providers.NewRegistry())
	model, err := requestRouter.Resolve(context.Background(), "coder", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = requestRouter.ProxyWithOptions(context.Background(), *model, "no-fallback-unsupported-raw", "/v1/chat/completions", []byte(`{"model":"coder"}`), "application/json", router.RawProxyOptions{
		Method:          http.MethodPost,
		Headers:         make(http.Header),
		DisableFallback: true,
	})
	if err == nil || !strings.Contains(err.Error(), "does not support raw endpoint") {
		t.Fatalf("expected raw adapter capability error, got %v", err)
	}
	if secondaryCalls.Load() != 0 {
		t.Fatalf("disable_fallback dispatched unsupported raw request to secondary provider %d times", secondaryCalls.Load())
	}
}

func TestDisableFallbackStopsAfterNotFoundAndRecordsCooldown(t *testing.T) {
	var secondaryCalls atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"model unavailable"}}`))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondaryCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"secondary","model":"upstream-success","choices":[{"message":{"role":"assistant","content":"unexpected fallback"},"finish_reason":"stop"}]}`))
	}))
	defer second.Close()
	dataStore := newStore(t, fallbackConfig(first.URL, second.URL))
	requestRouter := router.New(dataStore, providers.NewRegistry())
	model, err := requestRouter.Resolve(context.Background(), "coder", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = requestRouter.Execute(context.Background(), *model, canonical.Request{
		RequestID: "no-fallback-not-found",
		Metadata:  map[string]any{"disable_fallback": true},
		Messages:  []canonical.Message{{Role: "user", Content: "hello"}},
	})
	if providers.Status(err) != http.StatusNotFound {
		t.Fatalf("expected first 404 without fallback, got %v", err)
	}
	if secondaryCalls.Load() != 0 {
		t.Fatalf("disable_fallback dispatched 404 request to secondary provider %d times", secondaryCalls.Load())
	}
	until, err := dataStore.ModelCooldownUntil(context.Background(), "cred-failing", "upstream-fail", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if until.IsZero() {
		t.Fatal("pinned 404 did not record an upstream-model cooldown")
	}
}

type failingCredentialRefresher struct{}

func (failingCredentialRefresher) EnsureValid(context.Context, store.Provider, store.Credential, bool) (store.Credential, error) {
	return store.Credential{}, fmt.Errorf("credential preparation failed")
}

func TestStreamingRewritesEveryModelEvent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chunk-1\",\"model\":\"upstream-stream\",\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chunk-1\",\"model\":\"upstream-stream\",\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chunk-1\",\"model\":\"upstream-stream\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	cfg := fallbackConfig(upstream.URL, upstream.URL)
	cfg.Providers = cfg.Providers[:1]
	cfg.Providers[0].ID = "stream"
	cfg.Models[0].Routes = cfg.Models[0].Routes[:1]
	cfg.Models[0].Routes[0].Provider = "stream"
	cfg.Models[0].Routes[0].UpstreamModel = "upstream-stream"
	dataStore := newStore(t, cfg)
	requestRouter := router.New(dataStore, providers.NewRegistry())
	model, err := requestRouter.Resolve(context.Background(), "coder", nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := requestRouter.ExecuteStream(context.Background(), *model, canonical.Request{RequestID: "req-stream", Messages: []canonical.Message{{Role: "user", Content: "hi"}}, Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for event := range result.Events {
		if event.Model != "" && event.Model != "td-coder-pro" {
			t.Fatalf("event model leaked upstream name: %q", event.Model)
		}
		text.WriteString(event.Text)
	}
	if text.String() != "hello" {
		t.Fatalf("stream text = %q", text.String())
	}
}

func TestSessionAffinityKeepsCredentialUntilFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"chatcmpl-affinity","model":"upstream","choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}]}`, token)
	}))
	defer upstream.Close()
	t.Setenv("TPROXY_CRED_ONE", "account-one")
	t.Setenv("TPROXY_CRED_TWO", "account-two")
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{ID: "provider", Type: "openai-compatible", Name: "Provider", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "one", AuthType: "api_key", SecretEnv: "TPROXY_CRED_ONE", Priority: 10}, {ID: "two", AuthType: "api_key", SecretEnv: "TPROXY_CRED_TWO", Priority: 10}}}},
		Models:    []config.PublicModelConfig{{ID: "td-session", Enabled: true, RewriteResponseModel: true, Routes: []config.RouteTargetConfig{{ID: "route", Provider: "provider", UpstreamModel: "upstream", Priority: 10}}}},
	}
	dataStore := newStore(t, cfg)
	requestRouter := router.New(dataStore, providers.NewRegistry())
	requestRouter.ConfigureRouting(config.RoutingConfig{Strategy: "round-robin", SessionAffinity: true, SessionAffinityTTL: "1h"})
	model, err := requestRouter.Resolve(context.Background(), "td-session", nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := requestRouter.Execute(context.Background(), *model, canonical.Request{RequestID: "req-one", SessionID: "conversation-a", Messages: []canonical.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := requestRouter.Execute(context.Background(), *model, canonical.Request{RequestID: "req-two", SessionID: "conversation-a", Messages: []canonical.Message{{Role: "user", Content: "again"}}})
	if err != nil {
		t.Fatal(err)
	}
	if first.Response.Content != second.Response.Content {
		t.Fatalf("session moved accounts: first=%v second=%v", first.Response.Content, second.Response.Content)
	}
}

func TestOAuthCredentialRefreshesOnceAfterUnauthorized(t *testing.T) {
	t.Setenv("TPROXY_ROUTER_OAUTH_CLIENT", "router-client")
	var chatCalls atomic.Int32
	var refreshCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/oauth/token":
			refreshCalls.Add(1)
			_ = r.ParseForm()
			if r.Form.Get("refresh_token") != "router-refresh" {
				t.Errorf("refresh token = %q", r.Form.Get("refresh_token"))
			}
			_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"router-refresh","expires_in":3600}`))
		case "/v1/chat/completions":
			chatCalls.Add(1)
			if r.Header.Get("Authorization") != "Bearer new-access" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"message":"expired access token"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"chatcmpl-oauth","model":"upstream","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{ID: "oauth", Type: "openai-compatible", Name: "OAuth", BaseURL: upstream.URL, Enabled: true, OAuth: &config.OAuthConfig{AuthorizationURL: upstream.URL + "/oauth/authorize", TokenURL: upstream.URL + "/oauth/token", ClientIDEnv: "TPROXY_ROUTER_OAUTH_CLIENT"}}},
		Models:    []config.PublicModelConfig{{ID: "td-oauth", Enabled: true, RewriteResponseModel: true, Routes: []config.RouteTargetConfig{{ID: "route", Provider: "oauth", UpstreamModel: "upstream", Priority: 10}}}},
	}
	dataStore := newStore(t, cfg)
	if err := dataStore.SaveOAuthCredential(context.Background(), "oauth", "oauth-credential", "", "", store.OAuthToken{AccessToken: "old-access", RefreshToken: "router-refresh", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	authManager := auth.NewManager(dataStore, upstream.Client())
	defer authManager.Close()
	requestRouter := router.New(dataStore, providers.NewRegistry())
	requestRouter.SetCredentialRefresher(authManager)
	model, err := requestRouter.Resolve(context.Background(), "td-oauth", nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := requestRouter.Execute(context.Background(), *model, canonical.Request{RequestID: "req-oauth", Messages: []canonical.Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Response.Content != "ok" || chatCalls.Load() != 2 || refreshCalls.Load() != 1 {
		t.Fatalf("result=%+v chat_calls=%d refresh_calls=%d", result.Response, chatCalls.Load(), refreshCalls.Load())
	}
}

func TestProviderProxyPoolRotatesAndCredentialBindingOverrides(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"proxy-test","model":"upstream","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()
	cfg := &config.Config{
		ProxyPools: []config.ProxyPoolConfig{{ID: "direct-a", URL: "direct"}, {ID: "direct-b", URL: "none"}},
		Providers:  []config.ProviderConfig{{ID: "provider", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true, ProxyPools: []string{"direct-a", "direct-b"}, Credentials: []config.CredentialConfig{{ID: "credential", AuthType: "none"}}}},
		Models:     []config.PublicModelConfig{{ID: "model", Enabled: true, Routes: []config.RouteTargetConfig{{ID: "route", Provider: "provider", UpstreamModel: "upstream", Priority: 10}}}},
	}
	dataStore := newStore(t, cfg)
	requestRouter := router.New(dataStore, providers.NewRegistry())
	model, err := requestRouter.Resolve(context.Background(), "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := requestRouter.Execute(context.Background(), *model, canonical.Request{RequestID: "proxy-1", Messages: []canonical.Message{{Role: "user", Content: "one"}}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := requestRouter.Execute(context.Background(), *model, canonical.Request{RequestID: "proxy-2", Messages: []canonical.Message{{Role: "user", Content: "two"}}})
	if err != nil {
		t.Fatal(err)
	}
	if first.Selection.Credential.ProxyURL != "direct" || second.Selection.Credential.ProxyURL != "none" {
		t.Fatalf("proxy rotation first=%q second=%q", first.Selection.Credential.ProxyURL, second.Selection.Credential.ProxyURL)
	}
	providerCfg := cfg.Providers[0]
	providerCfg.Credentials[0].ProxyPools = []string{"direct-b"}
	if err = dataStore.SaveProvider(context.Background(), providerCfg); err != nil {
		t.Fatal(err)
	}
	overridden, err := requestRouter.Execute(context.Background(), *model, canonical.Request{RequestID: "proxy-3", Messages: []canonical.Message{{Role: "user", Content: "three"}}})
	if err != nil || overridden.Selection.Credential.ProxyURL != "none" {
		t.Fatalf("credential proxy override=%q err=%v", overridden.Selection.Credential.ProxyURL, err)
	}
}

func TestRoutePricingIsRecordedInUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"priced","model":"upstream","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1000,"completion_tokens":3000,"completion_tokens_details":{"reasoning_tokens":1000}}}`))
	}))
	defer upstream.Close()
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "provider", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "credential", AuthType: "none"}}}}, Models: []config.PublicModelConfig{{ID: "priced-model", Enabled: true, Routes: []config.RouteTargetConfig{{ID: "route", Provider: "provider", UpstreamModel: "upstream", Priority: 10, Pricing: &config.PricingConfig{InputPerMillion: 2, OutputPerMillion: 4, ReasoningPerMillion: 8, Request: 0.1}}}}}}
	dataStore := newStore(t, cfg)
	requestRouter := router.New(dataStore, providers.NewRegistry())
	model, err := requestRouter.Resolve(context.Background(), "priced-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = requestRouter.Execute(context.Background(), *model, canonical.Request{RequestID: "priced-request", Messages: []canonical.Message{{Role: "user", Content: "price me"}}}); err != nil {
		t.Fatal(err)
	}
	usage, err := dataStore.RecentUsage(context.Background(), 1)
	if err != nil || len(usage) != 1 {
		t.Fatalf("usage=%+v err=%v", usage, err)
	}
	// 0.1 request + 1000 input at $2/M + 2000 output at $4/M + 1000 reasoning at $8/M.
	want := 0.1 + 1000*2/1_000_000.0 + 2000*4/1_000_000.0 + 1000*8/1_000_000.0
	if math.Abs(usage[0].EstimatedCostUSD-want) > 1e-12 {
		t.Fatalf("estimated cost=%v want=%v", usage[0].EstimatedCostUSD, want)
	}
}

func TestSuccessfulRequestClearsOnlyCurrentUpstreamModelCooldown(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cooldown","model":"upstream-a","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{ID: "provider", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "credential", AuthType: "none"}}}},
		Models: []config.PublicModelConfig{{ID: "model-alias", Enabled: true, Routes: []config.RouteTargetConfig{
			{ID: "route-a", Provider: "provider", UpstreamModel: "upstream-a", Priority: 100},
			{ID: "route-b", Provider: "provider", UpstreamModel: "upstream-b", Priority: 50},
		}}},
	}
	dataStore := newStore(t, cfg)
	now := time.Now().UTC()
	if err := dataStore.SetCooldown(context.Background(), "credential", "expired-account", "expired account cooldown", now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.SetModelCooldown(context.Background(), "credential", "upstream-a", "expired", "expired cooldown", now.Add(-time.Minute), http.StatusTooManyRequests, 1); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.SetModelCooldown(context.Background(), "credential", "upstream-b", "active", "active cooldown", now.Add(time.Hour), http.StatusTooManyRequests, 2); err != nil {
		t.Fatal(err)
	}
	requestRouter := router.New(dataStore, providers.NewRegistry())
	model, err := requestRouter.Resolve(context.Background(), "model-alias", nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := requestRouter.Execute(context.Background(), *model, canonical.Request{RequestID: "upstream-a-success", Messages: []canonical.Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Selection.Route.UpstreamModel != "upstream-a" {
		t.Fatalf("selected upstream=%q want upstream-a", result.Selection.Route.UpstreamModel)
	}
	if count := dataStore.ModelCooldownCount(context.Background(), "credential", "upstream-a"); count != 0 {
		t.Fatalf("successful model cooldown count=%d want 0", count)
	}
	credential, err := dataStore.CredentialByID(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	if credential.Status != "healthy" || !credential.CooldownUntil.IsZero() || credential.LastErrorCode != "" || credential.LastError != "" {
		t.Fatalf("account cooldown was not cleared after success: %+v", credential)
	}
	modelBUntil, err := dataStore.ModelCooldownUntil(context.Background(), "credential", "upstream-b", now)
	if err != nil {
		t.Fatal(err)
	}
	if modelBUntil.IsZero() {
		t.Fatal("success on upstream-a cleared upstream-b cooldown")
	}
}

func TestModelCooldownFiltersOnlyMatchingUpstreamRoute(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"isolated","model":%q,"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`, payload["model"])
	}))
	defer upstream.Close()
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{ID: "provider", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "credential", AuthType: "none"}}}},
		Models: []config.PublicModelConfig{{ID: "model-alias", Enabled: true, Routes: []config.RouteTargetConfig{
			{ID: "route-a", Provider: "provider", UpstreamModel: "upstream-a", Priority: 100},
			{ID: "route-b", Provider: "provider", UpstreamModel: "upstream-b", Priority: 50},
		}}},
	}
	dataStore := newStore(t, cfg)
	if err := dataStore.SetModelCooldown(context.Background(), "credential", "upstream-a", "active", "route-a unavailable", time.Now().Add(time.Hour), http.StatusNotFound, 1); err != nil {
		t.Fatal(err)
	}
	requestRouter := router.New(dataStore, providers.NewRegistry())
	model, err := requestRouter.Resolve(context.Background(), "model-alias", nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := requestRouter.Execute(context.Background(), *model, canonical.Request{RequestID: "isolated-cooldown", Messages: []canonical.Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Selection.Route.UpstreamModel != "upstream-b" {
		t.Fatalf("selected upstream=%q want upstream-b", result.Selection.Route.UpstreamModel)
	}
}

func TestNotFoundFallsBackAndSkipsCooledUpstreamModel(t *testing.T) {
	var firstCalls atomic.Int32
	var secondCalls atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"model unavailable"}}`))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"secondary","model":"upstream-success","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer second.Close()
	dataStore := newStore(t, fallbackConfig(first.URL, second.URL))
	requestRouter := router.New(dataStore, providers.NewRegistry())
	model, err := requestRouter.Resolve(context.Background(), "coder", nil)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		result, executeErr := requestRouter.Execute(context.Background(), *model, canonical.Request{RequestID: fmt.Sprintf("not-found-%d", index), Messages: []canonical.Message{{Role: "user", Content: "hello"}}})
		if executeErr != nil {
			t.Fatal(executeErr)
		}
		if result.Selection.Provider.ID != "success" {
			t.Fatalf("selected provider=%q want success", result.Selection.Provider.ID)
		}
	}
	if firstCalls.Load() != 1 || secondCalls.Load() != 2 {
		t.Fatalf("upstream calls first=%d second=%d", firstCalls.Load(), secondCalls.Load())
	}
	until, err := dataStore.ModelCooldownUntil(context.Background(), "cred-failing", "upstream-fail", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if until.IsZero() {
		t.Fatal("404 fallback did not record upstream-model cooldown")
	}
}

func TestStreamingNotFoundFallsBack(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"model unavailable"}}`))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"secondary\",\"model\":\"upstream-success\",\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\ndata: [DONE]\n\n"))
	}))
	defer second.Close()
	dataStore := newStore(t, fallbackConfig(first.URL, second.URL))
	requestRouter := router.New(dataStore, providers.NewRegistry())
	model, err := requestRouter.Resolve(context.Background(), "coder", nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := requestRouter.ExecuteStream(context.Background(), *model, canonical.Request{RequestID: "stream-not-found", Stream: true, Messages: []canonical.Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Selection.Provider.ID != "success" {
		t.Fatalf("selected provider=%q want success", result.Selection.Provider.ID)
	}
	for range result.Events {
	}
	until, err := dataStore.ModelCooldownUntil(context.Background(), "cred-failing", "upstream-fail", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if until.IsZero() {
		t.Fatal("streaming 404 did not record upstream-model cooldown")
	}
}

func TestRawNotFoundFallsBack(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"model unavailable"}}`))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"upstream-success","data":[]}`))
	}))
	defer second.Close()
	dataStore := newStore(t, fallbackConfig(first.URL, second.URL))
	requestRouter := router.New(dataStore, providers.NewRegistry())
	model, err := requestRouter.Resolve(context.Background(), "coder", nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := requestRouter.ProxyWithOptions(context.Background(), *model, "raw-not-found", "/v1/images/generations", []byte(`{"model":"coder","prompt":"hello"}`), "application/json", router.RawProxyOptions{Method: http.MethodPost, Headers: make(http.Header)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Selection.Provider.ID != "success" {
		t.Fatalf("selected provider=%q want success", result.Selection.Provider.ID)
	}
	until, err := dataStore.ModelCooldownUntil(context.Background(), "cred-failing", "upstream-fail", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if until.IsZero() {
		t.Fatal("raw 404 did not record upstream-model cooldown")
	}
}

func TestUsageRecordsNormalizedTokensSavedMetadata(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"tokens-saved","model":"upstream","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{ID: "provider", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "credential", AuthType: "none"}}}},
		Models:    []config.PublicModelConfig{{ID: "model", Enabled: true, Routes: []config.RouteTargetConfig{{ID: "route", Provider: "provider", UpstreamModel: "upstream"}}}},
	}
	dataStore := newStore(t, cfg)
	requestRouter := router.New(dataStore, providers.NewRegistry())
	model, err := requestRouter.Resolve(context.Background(), "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		requestID string
		value     any
		want      int
	}{
		{requestID: "tokens-int64", value: int64(7), want: 7},
		{requestID: "tokens-json-number", value: json.Number("8"), want: 8},
		{requestID: "tokens-negative", value: -9, want: 0},
	}
	for _, test := range tests {
		if _, err = requestRouter.Execute(context.Background(), *model, canonical.Request{RequestID: test.requestID, Metadata: map[string]any{"tokens_saved": test.value}, Messages: []canonical.Message{{Role: "user", Content: "hello"}}}); err != nil {
			t.Fatalf("execute %s: %v", test.requestID, err)
		}
	}
	usage, err := dataStore.RecentUsage(context.Background(), len(tests))
	if err != nil {
		t.Fatal(err)
	}
	byRequest := make(map[string]int, len(usage))
	for _, event := range usage {
		byRequest[event.RequestID] = event.TokensSaved
	}
	for _, test := range tests {
		if got := byRequest[test.requestID]; got != test.want {
			t.Fatalf("tokens_saved for %s=%d want %d", test.requestID, got, test.want)
		}
	}
}

func TestExactModelPrecedesScopedAliasAndScopedAliasPrecedesGlobal(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{ID: "provider", Type: "openai-compatible", BaseURL: "http://127.0.0.1", Enabled: true, Credentials: []config.CredentialConfig{{ID: "credential", AuthType: "none"}}}},
		Models: []config.PublicModelConfig{
			{ID: "exact-model", Aliases: []string{"coder"}, Enabled: true, Routes: []config.RouteTargetConfig{{Provider: "provider", UpstreamModel: "one"}}},
			{ID: "scoped-model", Enabled: true, Routes: []config.RouteTargetConfig{{Provider: "provider", UpstreamModel: "two"}}},
			{ID: "team-model", Enabled: true, Routes: []config.RouteTargetConfig{{Provider: "provider", UpstreamModel: "three"}}},
		},
	}
	dataStore := newStore(t, cfg)
	keyID, plaintext, err := dataStore.CreateAPIKey(context.Background(), "scoped-key", "Scoped", []string{"*"}, config.ClientKeyPolicy{Team: "engineering"})
	if err != nil {
		t.Fatal(err)
	}
	if err = dataStore.SaveModelAlias(context.Background(), store.ModelAlias{Alias: "coder", PublicModelID: "team-model", TeamID: "engineering", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err = dataStore.SaveModelAlias(context.Background(), store.ModelAlias{Alias: "coder", PublicModelID: "scoped-model", APIKeyID: keyID, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err = dataStore.SaveModelAlias(context.Background(), store.ModelAlias{Alias: "exact-model", PublicModelID: "scoped-model", APIKeyID: keyID, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	key, err := dataStore.AuthenticateAPIKey(context.Background(), plaintext)
	if err != nil {
		t.Fatal(err)
	}
	requestRouter := router.New(dataStore, providers.NewRegistry())
	exact, err := requestRouter.Resolve(context.Background(), "exact-model", key)
	if err != nil || exact.ID != "exact-model" {
		t.Fatalf("exact resolution=%+v err=%v", exact, err)
	}
	scoped, err := requestRouter.Resolve(context.Background(), "coder", key)
	if err != nil || scoped.ID != "scoped-model" {
		t.Fatalf("scoped resolution=%+v err=%v", scoped, err)
	}
	_, teamPlaintext, err := dataStore.CreateAPIKey(context.Background(), "team-key", "Team", []string{"*"}, config.ClientKeyPolicy{Team: "engineering"})
	if err != nil {
		t.Fatal(err)
	}
	teamKey, err := dataStore.AuthenticateAPIKey(context.Background(), teamPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	teamScoped, err := requestRouter.Resolve(context.Background(), "coder", teamKey)
	if err != nil || teamScoped.ID != "team-model" {
		t.Fatalf("team scoped resolution=%+v err=%v", teamScoped, err)
	}
	global, err := requestRouter.Resolve(context.Background(), "coder", nil)
	if err != nil || global.ID != "exact-model" {
		t.Fatalf("global resolution=%+v err=%v", global, err)
	}
}

func TestRouteConditionsFilterBeforeDispatch(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"condition","model":%q,"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`, payload["model"])
	}))
	defer upstream.Close()
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "provider", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "credential", AuthType: "none"}}}}, Models: []config.PublicModelConfig{{ID: "condition-model", Enabled: true, Routes: []config.RouteTargetConfig{
		{ID: "openai-only", Provider: "provider", UpstreamModel: "openai-upstream", Priority: 100, Conditions: map[string]any{"protocol": "openai"}},
		{ID: "claude-only", Provider: "provider", UpstreamModel: "claude-upstream", Priority: 10, Conditions: map[string]any{"protocol": "claude"}},
	}}}}
	dataStore := newStore(t, cfg)
	requestRouter := router.New(dataStore, providers.NewRegistry())
	model, err := requestRouter.Resolve(context.Background(), "condition-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := requestRouter.Execute(context.Background(), *model, canonical.Request{RequestID: "condition-request", Source: canonical.ProtocolClaude, Messages: []canonical.Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Selection.Route.ID != "claude-only" || result.Selection.Route.UpstreamModel != "claude-upstream" {
		t.Fatalf("selected route=%+v", result.Selection.Route)
	}
}

func TestComboFallsBackAcrossOrderedPublicModelsAndRewritesComboID(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"try next"}}`))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"combo","model":"upstream-two","choices":[{"message":{"role":"assistant","content":"combo ok"},"finish_reason":"stop"}]}`))
	}))
	defer second.Close()
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "first-provider", Type: "openai-compatible", BaseURL: first.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "first-credential", AuthType: "none"}}},
			{ID: "second-provider", Type: "openai-compatible", BaseURL: second.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "second-credential", AuthType: "none"}}},
		},
		Models: []config.PublicModelConfig{
			{ID: "first-model", Enabled: true, Routes: []config.RouteTargetConfig{{ID: "first-route", Provider: "first-provider", UpstreamModel: "upstream-one"}}},
			{ID: "second-model", Enabled: true, Routes: []config.RouteTargetConfig{{ID: "second-route", Provider: "second-provider", UpstreamModel: "upstream-two"}}},
		},
		Combos: []config.ComboConfig{{ID: "combo-model", DisplayName: "Combo", Enabled: true, RewriteResponseModel: true, Capabilities: []string{"text"}, Items: []config.ComboItemConfig{{PublicModelID: "first-model"}, {PublicModelID: "second-model"}}}},
	}
	dataStore := newStore(t, cfg)
	requestRouter := router.New(dataStore, providers.NewRegistry())
	model, err := requestRouter.Resolve(context.Background(), "combo-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := requestRouter.Execute(context.Background(), *model, canonical.Request{RequestID: "combo-request", Source: canonical.ProtocolOpenAI, Messages: []canonical.Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Selection.Provider.ID != "second-provider" || result.Response.Model != "combo-model" || result.Response.Content != "combo ok" {
		t.Fatalf("combo result=%+v", result)
	}
}

func TestProviderConcurrentStreamLimitFallsBackAndReleases(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"primary\",\"model\":\"primary-upstream\",\"choices\":[{\"delta\":{\"content\":\"primary\"},\"finish_reason\":null}]}\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		_, _ = w.Write([]byte("data: {\"id\":\"primary\",\"model\":\"primary-upstream\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer primary.Close()
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"secondary\",\"model\":\"secondary-upstream\",\"choices\":[{\"delta\":{\"content\":\"secondary\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"secondary\",\"model\":\"secondary-upstream\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer secondary.Close()
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "primary", Type: "openai-compatible", BaseURL: primary.URL, Enabled: true, Limits: config.LimitPolicy{ConcurrentStreams: 1}, Credentials: []config.CredentialConfig{{ID: "primary-credential", AuthType: "none"}}},
			{ID: "secondary", Type: "openai-compatible", BaseURL: secondary.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "secondary-credential", AuthType: "none"}}},
		},
		Models: []config.PublicModelConfig{{ID: "stream-model", Enabled: true, Routes: []config.RouteTargetConfig{
			{ID: "primary-route", Provider: "primary", UpstreamModel: "primary-upstream", Priority: 100},
			{ID: "secondary-route", Provider: "secondary", UpstreamModel: "secondary-upstream", Priority: 10},
		}}},
	}
	dataStore := newStore(t, cfg)
	requestRouter := router.New(dataStore, providers.NewRegistry())
	model, err := requestRouter.Resolve(context.Background(), "stream-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := requestRouter.ExecuteStream(context.Background(), *model, canonical.Request{RequestID: "stream-one", Source: canonical.ProtocolOpenAI, Stream: true, Messages: []canonical.Message{{Role: "user", Content: "first"}}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("primary stream did not start")
	}
	second, err := requestRouter.ExecuteStream(context.Background(), *model, canonical.Request{RequestID: "stream-two", Source: canonical.ProtocolOpenAI, Stream: true, Messages: []canonical.Message{{Role: "user", Content: "second"}}})
	if err != nil {
		t.Fatal(err)
	}
	if second.Selection.Provider.ID != "secondary" {
		t.Fatalf("second stream selected provider %s", second.Selection.Provider.ID)
	}
	for range second.Events {
	}
	close(release)
	for range first.Events {
	}
	third, err := requestRouter.ExecuteStream(context.Background(), *model, canonical.Request{RequestID: "stream-three", Source: canonical.ProtocolOpenAI, Stream: true, Messages: []canonical.Message{{Role: "user", Content: "third"}}})
	if err != nil {
		t.Fatal(err)
	}
	if third.Selection.Provider.ID != "primary" {
		t.Fatalf("provider slot was not released; selected %s", third.Selection.Provider.ID)
	}
	for range third.Events {
	}
}

func TestConcurrentAccountSchedulingLoad(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"load","model":"upstream","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "provider", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "account-a", AuthType: "none"}, {ID: "account-b", AuthType: "none"}, {ID: "account-c", AuthType: "none"}}}}, Models: []config.PublicModelConfig{{ID: "load-model", Enabled: true, Routes: []config.RouteTargetConfig{{Provider: "provider", UpstreamModel: "upstream"}}}}}
	dataStore := newStore(t, cfg)
	requestRouter := router.New(dataStore, providers.NewRegistry())
	model, err := requestRouter.Resolve(context.Background(), "load-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 60
	counts := map[string]int{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	errorsCh := make(chan error, workers)
	for index := 0; index < workers; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			result, executeErr := requestRouter.Execute(context.Background(), *model, canonical.Request{RequestID: fmt.Sprintf("load-%d", index), Source: canonical.ProtocolOpenAI, Messages: []canonical.Message{{Role: "user", Content: "hello"}}})
			if executeErr != nil {
				errorsCh <- executeErr
				return
			}
			mu.Lock()
			counts[result.Selection.Credential.ID]++
			mu.Unlock()
		}(index)
	}
	wg.Wait()
	close(errorsCh)
	for executeErr := range errorsCh {
		t.Fatal(executeErr)
	}
	if len(counts) != 3 {
		t.Fatalf("scheduler distribution=%v", counts)
	}
	for credentialID, count := range counts {
		if count < 10 {
			t.Fatalf("credential %s received only %d requests: %v", credentialID, count, counts)
		}
	}
}

func fallbackConfig(firstURL, secondURL string) *config.Config {
	return &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "failing", Type: "openai-compatible", Name: "Failing", BaseURL: firstURL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "cred-failing", AuthType: "none", Priority: 100}}},
			{ID: "success", Type: "openai-compatible", Name: "Success", BaseURL: secondURL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "cred-success", AuthType: "none", Priority: 100}}},
		},
		Models: []config.PublicModelConfig{{
			ID: "td-coder-pro", DisplayName: "TD Coder Pro", Aliases: []string{"coder"}, Enabled: true, RewriteResponseModel: true,
			Routes: []config.RouteTargetConfig{{ID: "route-failing", Provider: "failing", UpstreamModel: "upstream-fail", Priority: 100}, {ID: "route-success", Provider: "success", UpstreamModel: "upstream-success", Priority: 50}},
		}},
	}
}

func newStore(t *testing.T, cfg *config.Config) *store.Store {
	t.Helper()
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := store.OpenSQLite(filepath.Join(t.TempDir(), "test.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	if err = dataStore.Seed(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	return dataStore
}
