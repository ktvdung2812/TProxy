package router_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/router"
)

func accounts(prefix string, count int) []config.CredentialConfig {
	result := make([]config.CredentialConfig, 0, count)
	for index := 0; index < count; index++ {
		result = append(result, config.CredentialConfig{ID: fmt.Sprintf("%s-%d", prefix, index), AuthType: "none", Priority: 100})
	}
	return result
}

// failoverConfig mirrors a Provider Priority Manager chain: P1 is the primary
// provider, P2 is the next one down the list. Each provider is given several
// accounts so a single request can exercise the failure threshold; with one
// account the per-account cooldown parks the credential after the first error.
func failoverConfig(primaryURL, secondaryURL string, primaryAccounts int) *config.Config {
	return &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "openrouter", Type: "openai-compatible", Name: "OpenRouter", BaseURL: primaryURL, Enabled: true, Credentials: accounts("cred-openrouter", primaryAccounts)},
			{ID: "anthropic-compat", Type: "openai-compatible", Name: "Anthropic", BaseURL: secondaryURL, Enabled: true, Credentials: accounts("cred-anthropic", 3)},
		},
		Models: []config.PublicModelConfig{{
			ID: "opus-5", DisplayName: "Opus 5", Aliases: []string{"opus"}, Enabled: true,
			Routes: []config.RouteTargetConfig{
				{ID: "route-openrouter", Provider: "openrouter", UpstreamModel: "opus-5", Priority: 100},
				{ID: "route-anthropic", Provider: "anthropic-compat", UpstreamModel: "opus-5", Priority: 90},
			},
		}},
	}
}

func failingUpstream(t *testing.T, calls *atomic.Int32, status int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream is down"}}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func healthyUpstream(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ok","model":"opus-5","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(server.Close)
	return server
}

// This is the behaviour requested for the Provider Priority Manager: the P1
// provider is dropped after three failed attempts and every later request goes
// straight to P2 without paying the cost of another failed attempt.
func TestProviderIsDisabledAfterThreeFailuresAndTrafficMovesDownTheChain(t *testing.T) {
	var primaryCalls, secondaryCalls atomic.Int32
	primary := failingUpstream(t, &primaryCalls, http.StatusServiceUnavailable)
	secondary := healthyUpstream(t, &secondaryCalls)

	dataStore := newStore(t, failoverConfig(primary.URL, secondary.URL, 6))
	requestRouter := router.New(dataStore, providers.NewRegistry())
	model, err := requestRouter.Resolve(context.Background(), "opus", nil)
	if err != nil {
		t.Fatal(err)
	}

	for index := 0; index < 4; index++ {
		result, executeErr := requestRouter.Execute(context.Background(), *model, canonical.Request{
			RequestID: fmt.Sprintf("failover-%d", index),
			Messages:  []canonical.Message{{Role: "user", Content: "hello"}},
		})
		if executeErr != nil {
			t.Fatalf("request %d failed: %v", index, executeErr)
		}
		if result.Selection.Provider.ID != "anthropic-compat" {
			t.Fatalf("request %d served by %q, want anthropic-compat", index, result.Selection.Provider.ID)
		}
	}

	// Three accounts are tried on the first request, the circuit opens, and the
	// remaining three accounts plus every later request skip the provider.
	if got := primaryCalls.Load(); got != 3 {
		t.Fatalf("primary provider was called %d times, want 3 before it is disabled", got)
	}
	if got := secondaryCalls.Load(); got != 4 {
		t.Fatalf("secondary provider was called %d times, want 4", got)
	}

	states := requestRouter.CircuitBreakers().Snapshot("opus-5")
	if len(states) != 1 {
		t.Fatalf("snapshot = %+v, want one disabled provider", states)
	}
	if states[0].ProviderID != "openrouter" || states[0].State != "open" {
		t.Fatalf("snapshot = %+v, want openrouter open", states[0])
	}
	if states[0].RetryAt.IsZero() {
		t.Fatal("a disabled provider must carry the time it will be probed again")
	}
}

func TestFailoverIsScopedToTheModel(t *testing.T) {
	var primaryCalls, secondaryCalls atomic.Int32
	primary := failingUpstream(t, &primaryCalls, http.StatusServiceUnavailable)
	secondary := healthyUpstream(t, &secondaryCalls)

	cfg := failoverConfig(primary.URL, secondary.URL, 3)
	cfg.Models = append(cfg.Models, config.PublicModelConfig{
		ID: "sonnet-5", DisplayName: "Sonnet 5", Aliases: []string{"sonnet"}, Enabled: true,
		Routes: []config.RouteTargetConfig{{ID: "route-openrouter-sonnet", Provider: "openrouter", UpstreamModel: "sonnet-5", Priority: 100}},
	})
	dataStore := newStore(t, cfg)
	requestRouter := router.New(dataStore, providers.NewRegistry())

	opus, err := requestRouter.Resolve(context.Background(), "opus", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, execErr := requestRouter.Execute(context.Background(), *opus, canonical.Request{RequestID: "scoped-opus", Messages: []canonical.Message{{Role: "user", Content: "hi"}}}); execErr != nil {
		t.Fatal(execErr)
	}
	if requestRouter.CircuitBreakers().Status("opus-5", "openrouter") != "open" {
		t.Fatal("openrouter should be disabled for opus-5")
	}
	if requestRouter.CircuitBreakers().Status("sonnet-5", "openrouter") != "closed" {
		t.Fatal("failures on opus-5 must not disable openrouter for sonnet-5")
	}

	before := primaryCalls.Load()
	sonnet, err := requestRouter.Resolve(context.Background(), "sonnet", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, execErr := requestRouter.Execute(context.Background(), *sonnet, canonical.Request{RequestID: "scoped-sonnet", Messages: []canonical.Message{{Role: "user", Content: "hi"}}}); execErr == nil {
		t.Fatal("expected the still-enabled provider to be attempted and fail")
	}
	if primaryCalls.Load() <= before {
		t.Fatal("sonnet-5 should still have attempted openrouter")
	}
}

// Rate limiting is an account-level problem with its own cooldown, so it must not
// take a whole provider out of the priority chain.
func TestRateLimitsDoNotDisableTheProvider(t *testing.T) {
	var primaryCalls, secondaryCalls atomic.Int32
	primary := failingUpstream(t, &primaryCalls, http.StatusTooManyRequests)
	secondary := healthyUpstream(t, &secondaryCalls)

	dataStore := newStore(t, failoverConfig(primary.URL, secondary.URL, 6))
	requestRouter := router.New(dataStore, providers.NewRegistry())
	model, err := requestRouter.Resolve(context.Background(), "opus", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, execErr := requestRouter.Execute(context.Background(), *model, canonical.Request{RequestID: "ratelimit", Messages: []canonical.Message{{Role: "user", Content: "hi"}}}); execErr != nil {
		t.Fatal(execErr)
	}
	if requestRouter.CircuitBreakers().Status("opus-5", "openrouter") != "closed" {
		t.Fatal("429s must not open the provider circuit")
	}
	if got := primaryCalls.Load(); got != 6 {
		t.Fatalf("every account was called %d times, want 6 — rate limits must not cut the chain short", got)
	}
}

func TestConfiguredFailureThresholdIsApplied(t *testing.T) {
	var primaryCalls, secondaryCalls atomic.Int32
	primary := failingUpstream(t, &primaryCalls, http.StatusServiceUnavailable)
	secondary := healthyUpstream(t, &secondaryCalls)

	dataStore := newStore(t, failoverConfig(primary.URL, secondary.URL, 6))
	requestRouter := router.New(dataStore, providers.NewRegistry())
	requestRouter.ConfigureRouting(config.RoutingConfig{
		Failover: config.FailoverConfig{FailureThreshold: 1, ResetTimeout: "1h"},
	})
	model, err := requestRouter.Resolve(context.Background(), "opus", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, execErr := requestRouter.Execute(context.Background(), *model, canonical.Request{RequestID: "threshold", Messages: []canonical.Message{{Role: "user", Content: "hi"}}}); execErr != nil {
		t.Fatal(execErr)
	}
	if got := primaryCalls.Load(); got != 1 {
		t.Fatalf("primary called %d times, want 1 with a threshold of 1", got)
	}
}

func TestFailoverCanBeDisabled(t *testing.T) {
	var primaryCalls, secondaryCalls atomic.Int32
	primary := failingUpstream(t, &primaryCalls, http.StatusServiceUnavailable)
	secondary := healthyUpstream(t, &secondaryCalls)

	dataStore := newStore(t, failoverConfig(primary.URL, secondary.URL, 6))
	requestRouter := router.New(dataStore, providers.NewRegistry())
	off := false
	requestRouter.ConfigureRouting(config.RoutingConfig{Failover: config.FailoverConfig{Enabled: &off}})
	model, err := requestRouter.Resolve(context.Background(), "opus", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, execErr := requestRouter.Execute(context.Background(), *model, canonical.Request{RequestID: "disabled", Messages: []canonical.Message{{Role: "user", Content: "hi"}}}); execErr != nil {
		t.Fatal(execErr)
	}
	if got := primaryCalls.Load(); got != 6 {
		t.Fatalf("primary called %d times, want all 6 accounts when failover is turned off", got)
	}
}

func TestResetPutsTheProviderBackInRotation(t *testing.T) {
	var primaryCalls, secondaryCalls atomic.Int32
	primary := failingUpstream(t, &primaryCalls, http.StatusServiceUnavailable)
	secondary := healthyUpstream(t, &secondaryCalls)

	dataStore := newStore(t, failoverConfig(primary.URL, secondary.URL, 6))
	requestRouter := router.New(dataStore, providers.NewRegistry())
	// Account cooldowns expire immediately so this test isolates the provider
	// circuit from the per-account backoff.
	requestRouter.ConfigureRouting(config.RoutingConfig{Cooldown: config.CooldownConfig{Default: "1ms", Status5xx: "1ms"}})
	model, err := requestRouter.Resolve(context.Background(), "opus", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, execErr := requestRouter.Execute(context.Background(), *model, canonical.Request{RequestID: "reset-trip", Messages: []canonical.Message{{Role: "user", Content: "hi"}}}); execErr != nil {
		t.Fatal(execErr)
	}
	before := primaryCalls.Load()
	if _, execErr := requestRouter.Execute(context.Background(), *model, canonical.Request{RequestID: "reset-blocked", Messages: []canonical.Message{{Role: "user", Content: "hi"}}}); execErr != nil {
		t.Fatal(execErr)
	}
	if primaryCalls.Load() != before {
		t.Fatal("a disabled provider must not be attempted again before its backoff expires")
	}

	requestRouter.CircuitBreakers().Reset("opus-5", "openrouter")
	if _, execErr := requestRouter.Execute(context.Background(), *model, canonical.Request{RequestID: "reset-after", Messages: []canonical.Message{{Role: "user", Content: "hi"}}}); execErr != nil {
		t.Fatal(execErr)
	}
	if primaryCalls.Load() <= before {
		t.Fatal("after a manual reset the provider should be attempted again")
	}
}

// When every provider in the chain has been disabled the caller gets an
// actionable error rather than the generic "no credential" message.
func TestAllProvidersDisabledReportsFailover(t *testing.T) {
	var primaryCalls, secondaryCalls atomic.Int32
	primary := failingUpstream(t, &primaryCalls, http.StatusServiceUnavailable)
	secondary := failingUpstream(t, &secondaryCalls, http.StatusServiceUnavailable)

	dataStore := newStore(t, failoverConfig(primary.URL, secondary.URL, 3))
	requestRouter := router.New(dataStore, providers.NewRegistry())
	model, err := requestRouter.Resolve(context.Background(), "opus", nil)
	if err != nil {
		t.Fatal(err)
	}
	var lastErr error
	for index := 0; index < 4; index++ {
		_, lastErr = requestRouter.Execute(context.Background(), *model, canonical.Request{RequestID: fmt.Sprintf("exhausted-%d", index), Messages: []canonical.Message{{Role: "user", Content: "hi"}}})
	}
	var providerErr *providers.ProviderError
	if !errors.As(lastErr, &providerErr) {
		t.Fatalf("error = %T %v, want *providers.ProviderError", lastErr, lastErr)
	}
	if providerErr.Code != "all_providers_failed_over" {
		t.Fatalf("code = %q, want all_providers_failed_over", providerErr.Code)
	}
	if providerErr.Status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", providerErr.Status, http.StatusServiceUnavailable)
	}
}
