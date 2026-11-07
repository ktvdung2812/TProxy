package router

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/pricing"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/resilience"
	"github.com/tproxy/tproxy/internal/store"
)

type fakeClusterRouting struct {
	mode    string
	pricing *config.PricingConfig
	models  map[string]bool
}

func (f *fakeClusterRouting) ClusterRoute(modelID string) (string, *config.PricingConfig, bool) {
	if !f.models[modelID] {
		return "", nil, false
	}
	return f.mode, f.pricing, true
}

// fakePeerTransport answers for every peer node in-process.
type fakePeerTransport struct {
	mu      sync.Mutex
	nodes   []string
	respond func(nodeID string, req *http.Request) *http.Response
}

func (f *fakePeerTransport) Do(ctx context.Context, nodeID string, req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.nodes = append(f.nodes, nodeID)
	f.mu.Unlock()
	return f.respond(nodeID, req), nil
}

func (f *fakePeerTransport) calledNodes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.nodes...)
}

func peerResponse(status int, contentType, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(body))}
}

func peerCredential(id, node string, priority int, models ...string) config.CredentialConfig {
	served := make([]any, 0, len(models))
	for _, model := range models {
		served = append(served, model)
	}
	return config.CredentialConfig{ID: id, AuthType: "none", Priority: priority, Metadata: map[string]any{"cluster_node_id": node, "serves_models": served}}
}

// clusterRouterConfig has model m on two local providers (priorities 100 and
// 50), model solo with no local route, and three peers serving different sets.
func clusterRouterConfig(localURL string) *config.Config {
	return &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "local-a", Type: "openai-compatible", BaseURL: localURL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "a1", AuthType: "none"}}},
			{ID: "local-b", Type: "openai-compatible", BaseURL: localURL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "b1", AuthType: "none"}, {ID: "b2", AuthType: "none"}}},
			{ID: ClusterProviderID, Type: ClusterProviderID, Enabled: true, Credentials: []config.CredentialConfig{
				peerCredential("cluster-node-n1", "n1", 200, "m", "solo"),
				peerCredential("cluster-node-n2", "n2", 150, "other"),
				peerCredential("cluster-node-n3", "n3", 100, "m", "solo"),
			}},
		},
		Models: []config.PublicModelConfig{
			{ID: "m", Enabled: true, Routes: []config.RouteTargetConfig{
				{ID: "r-a", Provider: "local-a", UpstreamModel: "up-a", Priority: 100},
				{ID: "r-b", Provider: "local-b", UpstreamModel: "up-b", Priority: 50},
			}},
			{ID: "solo", Enabled: true},
		},
		Combos: []config.ComboConfig{{ID: "combo", Enabled: true, Items: []config.ComboItemConfig{
			{PublicModelID: "m"}, {PublicModelID: "solo"}, {PublicModelID: "m", RouteTargetID: "r-b"},
		}}},
	}
}

func newClusterRouter(t *testing.T, localURL string, transport providers.ClusterTransport) (*Router, *store.Store) {
	t.Helper()
	data := openTestStore(t, clusterRouterConfig(localURL))
	registry := providers.NewRegistry()
	registry.SetClusterTransport(transport)
	requestRouter := New(data, registry)
	requestRouter.ConfigureRouting(config.RoutingConfig{Strategy: StrategyFillFirst})
	return requestRouter, data
}

func resolveModel(t *testing.T, r *Router, id string) store.PublicModel {
	t.Helper()
	model, err := r.Resolve(context.Background(), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	return *model
}

func routeIDs(routes []store.RouteTarget) []string {
	ids := make([]string, 0, len(routes))
	for _, route := range routes {
		ids = append(ids, route.ID)
	}
	return ids
}

func TestClusterRouteAddedOnlyWhenCatalogOffersModel(t *testing.T) {
	r, _ := newClusterRouter(t, "http://127.0.0.1:1", nil)
	model := resolveModel(t, r, "m")
	routes, err := r.modelRoutes(context.Background(), model, false)
	if err != nil || strings.Join(routeIDs(routes), ",") != "r-a,r-b" {
		t.Fatalf("routes without cluster routing = %v, %v", routeIDs(routes), err)
	}
	routing := &fakeClusterRouting{models: map[string]bool{"other": true}}
	r.SetClusterRouting(routing)
	if routes, _ = r.modelRoutes(context.Background(), model, false); len(routes) != 2 {
		t.Fatalf("routes for a model outside the catalog = %v", routeIDs(routes))
	}
	price := &config.PricingConfig{InputPerMillion: 1, OutputPerMillion: 2}
	routing.models["m"], routing.pricing = true, price
	routes, _ = r.modelRoutes(context.Background(), model, false)
	if len(routes) != 3 {
		t.Fatalf("routes = %v", routeIDs(routes))
	}
	cluster := routes[2]
	want := store.RouteTarget{ID: "cluster:m", PublicModelID: "m", ProviderID: ClusterProviderID, UpstreamModel: "m", Priority: 49, Weight: 1, Enabled: true, Pricing: price}
	if cluster.ID != want.ID || cluster.PublicModelID != want.PublicModelID || cluster.ProviderID != want.ProviderID || cluster.UpstreamModel != want.UpstreamModel || cluster.Priority != want.Priority || cluster.Weight != 1 || !cluster.Enabled || cluster.Pricing != price {
		t.Fatalf("cluster route = %+v, want %+v", cluster, want)
	}
	r.SetClusterRouting(nil)
	if routes, _ = r.modelRoutes(context.Background(), model, false); len(routes) != 2 {
		t.Fatalf("routes after disabling = %v", routeIDs(routes))
	}
	// A provider:model selector never borrows from the cluster.
	r.SetClusterRouting(&fakeClusterRouting{models: map[string]bool{"local-a:raw-upstream": true}})
	direct, err := r.Resolve(context.Background(), "local-a:raw-upstream", nil)
	if err != nil {
		t.Fatal(err)
	}
	if routes, _ = r.modelRoutes(context.Background(), *direct, false); len(routes) != 1 || routes[0].ID != "direct:local-a:raw-upstream" {
		t.Fatalf("selector routes = %+v", routes)
	}
}

func TestClusterRoutePriorityByMode(t *testing.T) {
	r, _ := newClusterRouter(t, "http://127.0.0.1:1", nil)
	model := resolveModel(t, r, "m")

	r.SetClusterRouting(&fakeClusterRouting{mode: "fallback", models: map[string]bool{"m": true, "solo": true}})
	routes, _ := r.modelRoutes(context.Background(), model, false)
	if got := strings.Join(routeIDs(routes), ","); got != "r-a,r-b,cluster:m" || routes[2].Priority != 49 {
		t.Fatalf("fallback order = %s (priority %d)", got, routes[2].Priority)
	}
	routes, _ = r.modelRoutes(context.Background(), resolveModel(t, r, "solo"), false)
	if len(routes) != 1 || routes[0].ID != "cluster:solo" || routes[0].Priority != 0 {
		t.Fatalf("cluster-only routes = %+v", routes)
	}

	r.SetClusterRouting(&fakeClusterRouting{mode: "shared", models: map[string]bool{"m": true, "solo": true}})
	routes, _ = r.modelRoutes(context.Background(), model, false)
	if got := strings.Join(routeIDs(routes), ","); got != "r-a,cluster:m,r-b" || routes[1].Priority != 100 {
		t.Fatalf("shared order = %s (priority %d)", got, routes[1].Priority)
	}
	routes, _ = r.modelRoutes(context.Background(), resolveModel(t, r, "solo"), false)
	if len(routes) != 1 || routes[0].Priority != 0 {
		t.Fatalf("shared cluster-only routes = %+v", routes)
	}

	// Shared ties the cluster with the best local route, so round-robin
	// rotation alternates between them.
	r.ConfigureRouting(config.RoutingConfig{Strategy: StrategyRoundRobin})
	first := map[string]int{}
	for range 4 {
		rotated, err := r.routesForModel(context.Background(), model)
		if err != nil {
			t.Fatal(err)
		}
		first[rotated[0].ID]++
		if rotated[2].ID != "r-b" {
			t.Fatalf("rotation reordered the lower tier: %v", routeIDs(rotated))
		}
	}
	if first["r-a"] != 2 || first["cluster:m"] != 2 {
		t.Fatalf("shared rotation = %v", first)
	}
}

func TestClusterRouteForComboItems(t *testing.T) {
	r, _ := newClusterRouter(t, "http://127.0.0.1:1", nil)
	r.SetClusterRouting(&fakeClusterRouting{models: map[string]bool{"m": true, "solo": true}})
	combo := resolveModel(t, r, "combo")
	routes, err := r.modelRoutes(context.Background(), combo, false)
	if err != nil {
		t.Fatal(err)
	}
	// Each item is offset by its position; the cluster route is placed within
	// its item before the offset, and the pinned third item does not borrow.
	want := []struct {
		id       string
		model    string
		priority int
	}{
		{"r-a", "m", 3_000_100}, {"r-b", "m", 3_000_050}, {"cluster:m", "m", 3_000_049},
		{"cluster:solo", "solo", 2_000_000},
		{"r-b", "m", 1_000_050},
	}
	if len(routes) != len(want) {
		t.Fatalf("combo routes = %v", routeIDs(routes))
	}
	for index, route := range routes {
		if route.ID != want[index].id || route.PublicModelID != want[index].model || route.Priority != want[index].priority {
			t.Fatalf("combo route %d = %s/%s/%d, want %+v (all %v)", index, route.ID, route.PublicModelID, route.Priority, want[index], routeIDs(routes))
		}
	}
}

func TestRelayedRequestNeverSelectsClusterProvider(t *testing.T) {
	r, _ := newClusterRouter(t, "http://127.0.0.1:1", nil)
	r.SetClusterRouting(&fakeClusterRouting{models: map[string]bool{"m": true, "solo": true}})
	model := resolveModel(t, r, "m")
	selections, err := r.selections(context.Background(), model, canonical.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if !selectedProvider(selections, ClusterProviderID) {
		t.Fatalf("plain request did not reach the cluster: %v", selectionIDs(selections))
	}
	for _, relayed := range []any{true, "true"} {
		request := canonical.Request{Metadata: map[string]any{ClusterRelayedMetadataKey: relayed}}
		if !IsClusterRelayed(request) {
			t.Fatalf("IsClusterRelayed(%v) = false", relayed)
		}
		selections, err = r.selections(context.Background(), model, request)
		if err != nil {
			t.Fatal(err)
		}
		if selectedProvider(selections, ClusterProviderID) || len(selections) != 3 {
			t.Fatalf("relayed selections = %v", selectionIDs(selections))
		}
		_, err = r.selections(context.Background(), resolveModel(t, r, "solo"), request)
		if err == nil || providers.Code(err) == "provider_unavailable" || providers.Code(err) == "all_providers_failed_over" {
			t.Fatalf("relayed cluster-only request = %v", err)
		}
	}
	if IsClusterRelayed(canonical.Request{Metadata: map[string]any{ClusterRelayedMetadataKey: "no"}}) || IsClusterRelayed(canonical.Request{}) {
		t.Fatal("IsClusterRelayed accepted a non-relayed request")
	}
}

func selectedProvider(selections []Selection, providerID string) bool {
	for _, selection := range selections {
		if selection.Provider.ID == providerID {
			return true
		}
	}
	return false
}

func selectionIDs(selections []Selection) []string {
	ids := make([]string, 0, len(selections))
	for _, selection := range selections {
		ids = append(ids, selection.Credential.ID)
	}
	return ids
}

func TestServesModelsFiltersPeerCredentials(t *testing.T) {
	r, _ := newClusterRouter(t, "http://127.0.0.1:1", nil)
	r.SetClusterRouting(&fakeClusterRouting{models: map[string]bool{"m": true, "solo": true}})
	selections, err := r.selections(context.Background(), resolveModel(t, r, "m"), canonical.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(selectionIDs(selections), ","); got != "a1,b1,b2,cluster-node-n1,cluster-node-n3" {
		t.Fatalf("selections = %s", got)
	}
	snapshot, err := r.Capacity(context.Background(), nil, "m")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.EligibleAccountCount != 5 {
		t.Fatalf("capacity accounts = %d, want local a1 b1 b2 plus peers n1 n3", snapshot.EligibleAccountCount)
	}
	for _, window := range snapshot.QuotaWindows {
		if window.CredentialID == "cluster-node-n2" {
			t.Fatal("a peer that does not serve the model was counted")
		}
	}
	plain := []store.Credential{{ID: "local"}, {ID: "listed", Metadata: map[string]any{ClusterServesModelsMetadataKey: []string{"x"}}}, {ID: "malformed", Metadata: map[string]any{ClusterServesModelsMetadataKey: "x"}}}
	if got := filterServedModels(plain, "x"); len(got) != 2 || got[0].ID != "local" || got[1].ID != "listed" {
		t.Fatalf("filterServedModels = %+v", got)
	}
}

func TestClusterFailuresDoNotOpenCircuitBreaker(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"local down"}}`)
	}))
	defer local.Close()
	transport := &fakePeerTransport{respond: func(string, *http.Request) *http.Response {
		return peerResponse(http.StatusBadGateway, "application/json", `{"error":{"code":"upstream_server_error","message":"peer down"}}`)
	}}
	r, data := newClusterRouter(t, local.URL, transport)
	r.ConfigureRouting(config.RoutingConfig{Strategy: StrategyFillFirst, Failover: config.FailoverConfig{FailureThreshold: 1}})
	r.SetClusterRouting(&fakeClusterRouting{models: map[string]bool{"m": true, "solo": true}})

	solo := resolveModel(t, r, "solo")
	request := canonical.Request{Source: canonical.ProtocolOpenAI, Raw: map[string]any{"messages": []any{}}}
	for range 2 {
		_, err := r.Execute(context.Background(), solo, request)
		if providers.Code(err) == "all_providers_failed_over" {
			t.Fatalf("cluster provider was failed over: %v", err)
		}
		_ = data.ClearCooldown(context.Background(), "cluster-node-n1")
		_ = data.ClearModelCooldown(context.Background(), "cluster-node-n1", "solo")
		_ = data.ClearModelCooldown(context.Background(), "cluster-node-n3", "solo")
	}
	if _, err := r.ExecuteStream(context.Background(), solo, request); err == nil {
		t.Fatal("stream through a failing peer succeeded")
	}
	breakers := r.CircuitBreakers()
	if state := breakers.Status("solo", ClusterProviderID); state != resilience.StateClosed || len(breakers.Snapshot("solo")) != 0 {
		t.Fatalf("cluster breaker = %s %+v", state, breakers.Snapshot("solo"))
	}
	if len(transport.calledNodes()) < 4 {
		t.Fatalf("peers were not retried across requests: %v", transport.calledNodes())
	}
	until, err := data.ModelCooldownUntil(context.Background(), "cluster-node-n1", "solo", time.Now())
	if err != nil || !until.After(time.Now()) {
		t.Fatalf("failing peer was not cooled down for the model: %v %v", until, err)
	}

	// The same failure on a local provider does trip its breaker.
	_, _ = r.Execute(context.Background(), resolveModel(t, r, "m"), request)
	if state := breakers.Status("m", "local-a"); state != resilience.StateOpen {
		t.Fatalf("local breaker = %s; the threshold setup is not exercising the breaker", state)
	}
}

func TestClusterRouteServesRequestThroughPeer(t *testing.T) {
	transport := &fakePeerTransport{respond: func(nodeID string, req *http.Request) *http.Response {
		return peerResponse(http.StatusOK, "application/json", `{"id":"c","model":"solo","choices":[{"message":{"role":"assistant","content":"from `+nodeID+`"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1000000,"completion_tokens":0}}`)
	}}
	r, data := newClusterRouter(t, "http://127.0.0.1:1", transport)
	r.SetClusterRouting(&fakeClusterRouting{models: map[string]bool{"solo": true}, pricing: &config.PricingConfig{InputPerMillion: 2}})
	result, err := r.Execute(context.Background(), resolveModel(t, r, "solo"), canonical.Request{RequestID: "req-peer", Source: canonical.ProtocolOpenAI, Raw: map[string]any{"messages": []any{}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Selection.Provider.ID != ClusterProviderID || result.Selection.Credential.ID != "cluster-node-n1" || result.Response.Content != "from n1" {
		t.Fatalf("result = %+v %+v", result.Selection, result.Response)
	}
	if result.CostUSD != 2 {
		t.Fatalf("cost = %v, want the cluster route pricing", result.CostUSD)
	}
	snapshot, err := data.Snapshot(context.Background())
	if err != nil || snapshot.Usage.Requests != 1 {
		t.Fatalf("usage = %+v, %v", snapshot.Usage, err)
	}
}

func TestClusterRawRoutingScopesKeyAndForwardsVoicesQuery(t *testing.T) {
	transport := &fakePeerTransport{respond: func(_ string, req *http.Request) *http.Response {
		want := fmt.Sprintf("%x", sha256.Sum256([]byte("client-a\x00same")))
		if req.Header.Get("Idempotency-Key") != want || req.URL.Query().Get("model") != "solo" || req.URL.Query().Get("language") != "en" {
			t.Errorf("peer request query=%s headers=%v", req.URL.RawQuery, req.Header)
		}
		return peerResponse(http.StatusOK, "application/json", `{}`)
	}}
	r, _ := newClusterRouter(t, "http://127.0.0.1:1", transport)
	r.SetClusterRouting(&fakeClusterRouting{models: map[string]bool{"solo": true}})
	_, err := r.ProxyWithOptions(context.Background(), resolveModel(t, r, "solo"), "raw", "/v1/audio/voices", nil, "application/json", RawProxyOptions{Method: http.MethodGet, RawQuery: "model=alias&language=en", ClientAPIKeyID: "client-a", Headers: http.Header{"Idempotency-Key": {"same"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(transport.calledNodes()) != 1 {
		t.Fatal("peer not called")
	}
}

func TestServedModelsMetadataOnlyFiltersClusterCredentials(t *testing.T) {
	cfg := clusterRouterConfig("http://127.0.0.1:1")
	cfg.Providers[0].Credentials[0].Metadata = map[string]any{"serves_models": []any{"unrelated"}}
	data := openTestStore(t, cfg)
	r := New(data, providers.NewRegistry())
	r.SetClusterRouting(&fakeClusterRouting{models: map[string]bool{"m": true}})
	selections, err := r.selections(context.Background(), resolveModel(t, r, "m"), canonical.Request{})
	if err != nil {
		t.Fatal(err)
	}
	foundLocal := false
	for _, selection := range selections {
		if selection.Credential.ID == "a1" {
			foundLocal = true
		}
		if selection.Credential.ID == "cluster-node-n2" {
			t.Fatal("cluster credential for another model was selected")
		}
	}
	if !foundLocal {
		t.Fatal("local credential was filtered by serves_models")
	}
	capacity, err := r.Capacity(context.Background(), nil, "m")
	if err != nil || capacity.EligibleAccountCount != 5 {
		t.Fatalf("capacity=%+v error=%v; want 3 local and 2 cluster accounts", capacity, err)
	}
}

func TestClusterMidStreamCooldown(t *testing.T) {
	for _, tc := range []struct {
		name            string
		err             error
		cancel          bool
		modelCooldown   bool
		accountCooldown bool
	}{
		{"model", &providers.ProviderError{Status: 503, Code: "cluster_peer_no_capacity", RetryAfter: "30"}, false, true, false},
		{"account", &providers.ProviderError{Status: 401, Code: "cluster_unauthorized"}, false, false, true},
		{"local policy", &providers.ProviderError{Status: 429, Code: "provider_limit_exceeded"}, false, false, false},
		{"canceled error", &providers.ProviderError{Status: 503, Code: "cluster_peer_no_capacity", Err: context.Canceled}, false, false, false},
		{"canceled context", &providers.ProviderError{Status: 503, Code: "cluster_peer_no_capacity"}, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, data := newClusterRouter(t, "http://127.0.0.1:1", nil)
			selection := Selection{Provider: store.Provider{ID: ClusterProviderID, Type: ClusterProviderID}, Credential: store.Credential{ID: "cluster-node-n1"}, Route: store.RouteTarget{UpstreamModel: "solo"}, StartedAt: time.Now()}
			input := make(chan canonical.Event, 2)
			input <- canonical.Event{Type: canonical.EventTextDelta, Text: "partial"}
			input <- canonical.Event{Type: canonical.EventError, Err: tc.err}
			close(input)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			for range r.wrapEvents(ctx, store.PublicModel{ID: "solo"}, selection, canonical.Request{}, time.Now(), input, nil) {
			}
			until, err := data.ModelCooldownUntil(context.Background(), selection.Credential.ID, "solo", time.Now())
			if err != nil || until.After(time.Now()) != tc.modelCooldown {
				t.Fatalf("model cooldown=%v error=%v", until, err)
			}
			credentials, err := data.Credentials(context.Background(), ClusterProviderID)
			if err != nil {
				t.Fatal(err)
			}
			for _, credential := range credentials {
				if credential.ID == selection.Credential.ID && credential.CooldownUntil.After(time.Now()) != tc.accountCooldown {
					t.Fatalf("account cooldown=%v", credential.CooldownUntil)
				}
				if credential.ID != selection.Credential.ID && credential.CooldownUntil.After(time.Now()) {
					t.Fatalf("other peer cooled down: %+v", credential)
				}
			}
		})
	}
}

func TestClusterMidStreamPeerFailureChangesNextSelection(t *testing.T) {
	transport := &fakePeerTransport{respond: func(_ string, _ *http.Request) *http.Response {
		return peerResponse(http.StatusOK, "text/event-stream", "data: {\"id\":\"c\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"+
			"data: {\"error\":{\"code\":\"cluster_unauthorized\",\"message\":\"unauthorized\"}}\n\n")
	}}
	r, data := newClusterRouter(t, "http://127.0.0.1:1", transport)
	r.SetClusterRouting(&fakeClusterRouting{models: map[string]bool{"solo": true}})
	model := resolveModel(t, r, "solo")
	request := canonical.Request{Source: canonical.ProtocolOpenAI, Raw: map[string]any{"messages": []any{}}}
	stream, err := r.ExecuteStream(context.Background(), model, request)
	if err != nil {
		t.Fatal(err)
	}
	for range stream.Events {
	}
	credentials, err := data.Credentials(context.Background(), ClusterProviderID)
	if err != nil {
		t.Fatal(err)
	}
	for _, credential := range credentials {
		if credential.ID == "cluster-node-n1" && !credential.CooldownUntil.After(time.Now()) {
			t.Fatal("unauthorized peer was not cooled down")
		}
	}
	selections, err := r.selections(context.Background(), model, request)
	if err != nil || len(selections) == 0 || selections[0].Credential.ID != "cluster-node-n3" {
		t.Fatalf("next selections=%+v error=%v", selections, err)
	}
}

func TestClusterResponsesStreamFailsOverBeforeOutput(t *testing.T) {
	transport := &fakePeerTransport{respond: func(nodeID string, req *http.Request) *http.Response {
		if nodeID == "n1" {
			return peerResponse(http.StatusOK, "text/event-stream", "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\"}}\n\n"+
				"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_is_overloaded\",\"message\":\"busy\"}}}\n\n")
		}
		return peerResponse(http.StatusOK, "text/event-stream", "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"+
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r3\"}}\n\n")
	}}
	r, data := newClusterRouter(t, "http://127.0.0.1:1", transport)
	r.SetClusterRouting(&fakeClusterRouting{models: map[string]bool{"solo": true}})
	if !r.bootstrapBufferingEnabled(store.Provider{Type: ClusterProviderID}, canonical.Request{Source: canonical.ProtocolResponses}) {
		t.Fatal("cluster Responses streams are not bootstrap-buffered")
	}
	if r.bootstrapBufferingEnabled(store.Provider{Type: ClusterProviderID}, canonical.Request{Source: canonical.ProtocolOpenAI}) {
		t.Fatal("bootstrap buffering widened beyond Responses")
	}
	stream, err := r.ExecuteStream(context.Background(), resolveModel(t, r, "solo"), canonical.Request{Source: canonical.ProtocolResponses, Raw: map[string]any{"input": "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	if stream.Selection.Credential.ID != "cluster-node-n3" {
		t.Fatalf("stream served by %s", stream.Selection.Credential.ID)
	}
	var frames []string
	for event := range stream.Events {
		if event.Type == canonical.EventError {
			t.Fatalf("stream error: %v", event.Err)
		}
		if event.Type == canonical.EventResponsesSSE {
			frames = append(frames, event.SSEEvent)
		}
	}
	if strings.Join(frames, ",") != "response.output_text.delta,response.completed" {
		t.Fatalf("client frames = %v (nothing from the failed peer may leak)", frames)
	}
	if nodes := strings.Join(transport.calledNodes(), ","); nodes != "n1,n3" {
		t.Fatalf("peers tried = %s", nodes)
	}
	until, err := data.ModelCooldownUntil(context.Background(), "cluster-node-n1", "solo", time.Now())
	if err != nil || !until.After(time.Now()) {
		t.Fatalf("peer that failed before output was not cooled down: %v %v", until, err)
	}
}

func TestLocalCapacityExcludesClusterAndRespectsCooldowns(t *testing.T) {
	cfg := clusterRouterConfig("http://127.0.0.1:1")
	off := false
	cfg.Providers = append(cfg.Providers, config.ProviderConfig{ID: "local-off", Type: "openai-compatible", Enabled: false, Credentials: []config.CredentialConfig{{ID: "off1", AuthType: "none"}}})
	cfg.Models[0].Routes = append(cfg.Models[0].Routes,
		config.RouteTargetConfig{ID: "r-cluster", Provider: ClusterProviderID, UpstreamModel: "m", Priority: 10},
		config.RouteTargetConfig{ID: "r-off", Provider: "local-off", UpstreamModel: "up-off", Priority: 10},
		config.RouteTargetConfig{ID: "r-a-disabled", Provider: "local-a", UpstreamModel: "up-x", Priority: 10, Enabled: &off},
		config.RouteTargetConfig{ID: "r-b-again", Provider: "local-b", UpstreamModel: "up-b2", Priority: 5},
	)
	data := openTestStore(t, cfg)
	r := New(data, providers.NewRegistry())
	r.SetClusterRouting(&fakeClusterRouting{models: map[string]bool{"m": true, "solo": true}})
	ctx := context.Background()
	before := len(r.rotation)
	count, err := r.LocalCapacity(ctx, "m")
	if err != nil || count != 3 {
		t.Fatalf("LocalCapacity = %d, %v; want a1 b1 b2", count, err)
	}
	if err := data.SetModelCooldown(ctx, "a1", "up-a", "upstream_rate_limited", "busy", time.Now().Add(time.Minute), 429, 1); err != nil {
		t.Fatal(err)
	}
	if err := data.SetCooldown(ctx, "b1", "upstream_auth", "bad", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// b2 cooled for up-b only still serves m through r-b-again.
	if err := data.SetModelCooldown(ctx, "b2", "up-b", "upstream_rate_limited", "busy", time.Now().Add(time.Minute), 429, 1); err != nil {
		t.Fatal(err)
	}
	if count, err = r.LocalCapacity(ctx, "m"); err != nil || count != 1 {
		t.Fatalf("LocalCapacity with cooldowns = %d, %v; want b2", count, err)
	}
	for _, id := range []string{"solo", "combo", "missing"} {
		if count, err = r.LocalCapacity(ctx, id); err != nil || count != 0 {
			t.Fatalf("LocalCapacity(%s) = %d, %v", id, count, err)
		}
	}
	if len(r.rotation) != before {
		t.Fatal("LocalCapacity advanced rotation state")
	}
	r.SetClusterRouting(recursiveRouting{r})
	if _, err = r.LocalCapacity(ctx, "m"); err != nil {
		t.Fatal(err)
	}
}

// recursiveRouting builds the cluster catalog on LocalCapacity, which must
// therefore never consult ClusterRoute itself.
type recursiveRouting struct{ r *Router }

func (c recursiveRouting) ClusterRoute(modelID string) (string, *config.PricingConfig, bool) {
	count, err := c.r.LocalCapacity(context.Background(), modelID)
	return "", nil, err == nil && count == 0
}

func TestClusterModelCatalogVisibility(t *testing.T) {
	r, data := newClusterRouter(t, "http://127.0.0.1:1", nil)
	registry := pricing.NewModelsRegistry(pricing.ModelsRegistryOptions{})
	full, bare, names := pricing.BuildModelsRegistryIndexForTest(map[string]any{"openai/unrelated": map[string]any{"id": "openai/unrelated"}})
	registry.SetIndexForTest(full, bare, names)
	r.SetModelsRegistry(registry)
	solo, err := data.PublicModel(context.Background(), "solo")
	if err != nil {
		t.Fatal(err)
	}
	if r.IsModelCatalogVisible(context.Background(), *solo) {
		t.Fatal("unknown model visible before the cluster offers it")
	}
	r.SetClusterRouting(&fakeClusterRouting{models: map[string]bool{"solo": true}})
	if !r.IsModelCatalogVisible(context.Background(), *solo) {
		t.Fatal("cluster model hidden from the catalog")
	}
	if upstream, known := r.catalogUpstreamModel(context.Background(), *solo); !known || upstream != "solo" {
		t.Fatalf("catalogUpstreamModel = %q %v", upstream, known)
	}
	if entry := r.CatalogDisplayEntry(context.Background(), *solo); entry.ID != "solo" {
		t.Fatalf("display entry = %+v", entry)
	}
}

func TestClusterProviderErrorsKeepPeerSemantics(t *testing.T) {
	transport := &fakePeerTransport{respond: func(nodeID string, req *http.Request) *http.Response {
		if nodeID == "n1" {
			response := peerResponse(http.StatusForbidden, "application/json", `{"error":{"code":"cluster_model_not_shared","message":"not lent"}}`)
			response.Header.Set("X-TProxy-Error-Code", "cluster_model_not_shared")
			return response
		}
		return peerResponse(http.StatusUnauthorized, "application/json", `{"error":{"code":"cluster_unauthorized","message":"bad link"}}`)
	}}
	r, data := newClusterRouter(t, "http://127.0.0.1:1", transport)
	r.SetClusterRouting(&fakeClusterRouting{models: map[string]bool{"solo": true}})
	_, err := r.Execute(context.Background(), resolveModel(t, r, "solo"), canonical.Request{Source: canonical.ProtocolOpenAI, Raw: map[string]any{"messages": []any{}}})
	var providerErr *providers.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Code != "cluster_unauthorized" {
		t.Fatalf("error = %v", err)
	}
	ctx := context.Background()
	// The model-level refusal benches only (n1, solo); the link failure benches n3 entirely.
	if until, _ := data.ModelCooldownUntil(ctx, "cluster-node-n1", "solo", time.Now()); !until.After(time.Now()) {
		t.Fatal("model-level peer refusal did not bench the (peer, model) pair")
	}
	credential, err := data.CredentialByID(ctx, "cluster-node-n1")
	if err != nil || credential.CooldownUntil.After(time.Now()) {
		t.Fatalf("model-level refusal benched the whole peer: %+v %v", credential.CooldownUntil, err)
	}
	credential, err = data.CredentialByID(ctx, "cluster-node-n3")
	if err != nil || !credential.CooldownUntil.After(time.Now()) {
		t.Fatalf("link failure did not bench the peer: %+v %v", credential.CooldownUntil, err)
	}
}
