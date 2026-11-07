package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/cluster"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/router"
	"github.com/tproxy/tproxy/internal/security"
	"github.com/tproxy/tproxy/internal/store"
)

// clusterE2EUpstream is an OpenAI-compatible upstream shared by both nodes. It
// echoes the bearer secret it received, which tells the test whose account
// served a request.
type clusterE2EUpstream struct {
	server *httptest.Server
	mu     sync.Mutex
	calls  []clusterE2ECall
}

type clusterE2ECall struct {
	Secret string
	Model  string
	Stream bool
}

func newClusterE2EUpstream(t *testing.T) *clusterE2EUpstream {
	t.Helper()
	upstream := &clusterE2EUpstream{}
	upstream.server = httptest.NewServer(http.HandlerFunc(upstream.serve))
	t.Cleanup(upstream.server.Close)
	return upstream
}

func (u *clusterE2EUpstream) serve(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/models") {
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": []map[string]any{{"id": "up-model", "object": "model"}}})
		return
	}
	if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	secret := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	u.mu.Lock()
	u.calls = append(u.calls, clusterE2ECall{Secret: secret, Model: body.Model, Stream: body.Stream})
	u.mu.Unlock()
	if body.Model == "fail-model" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"message": "upstream overloaded", "type": "server_error"}})
		return
	}
	content := "hello from " + secret
	usage := map[string]any{"prompt_tokens": 3, "completion_tokens": 4, "total_tokens": 7}
	if !body.Stream {
		writeJSON(w, http.StatusOK, map[string]any{
			"id": "chatcmpl-e2e", "object": "chat.completion", "model": body.Model,
			"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}},
			"usage":   usage,
		})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	chunks := []map[string]any{
		{"id": "chatcmpl-e2e", "object": "chat.completion.chunk", "model": body.Model, "choices": []map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant", "content": "hello "}}}},
		{"id": "chatcmpl-e2e", "object": "chat.completion.chunk", "model": body.Model, "choices": []map[string]any{{"index": 0, "delta": map[string]any{"content": "from " + secret}}}},
		{"id": "chatcmpl-e2e", "object": "chat.completion.chunk", "model": body.Model, "choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}},
		{"id": "chatcmpl-e2e", "object": "chat.completion.chunk", "model": body.Model, "choices": []map[string]any{}, "usage": usage},
	}
	for _, chunk := range chunks {
		encoded, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", encoded)
		if flusher != nil {
			flusher.Flush()
		}
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func (u *clusterE2EUpstream) count(secret, model string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	count := 0
	for _, call := range u.calls {
		if call.Secret == secret && call.Model == model {
			count++
		}
	}
	return count
}

type clusterE2ENode struct {
	server *Server
	store  *store.Store
	http   *httptest.Server
	keyID  string
	key    string
}

func newClusterE2ENode(t *testing.T, cfg *config.Config) *clusterE2ENode {
	t.Helper()
	db := apiTestStore(t, cfg)
	server := NewServer(cfg, db, router.New(db, providers.NewRegistry()))
	httpServer := httptest.NewServer(server.Handler())
	ctx, cancel := context.WithCancel(context.Background())
	server.StartBackground(ctx)
	t.Cleanup(func() {
		cancel()
		server.Close()
		httpServer.Close()
	})
	keyID, key, err := db.CreateAPIKey(context.Background(), "", "e2e client", nil)
	if err != nil {
		t.Fatal(err)
	}
	return &clusterE2ENode{server: server, store: db, http: httpServer, keyID: keyID, key: key}
}

func (n *clusterE2ENode) admin(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, n.http.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+testDashboardPassword)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	return response.StatusCode, data
}

func (n *clusterE2ENode) mustAdmin(t *testing.T, method, path string, body any) []byte {
	t.Helper()
	status, data := n.admin(t, method, path, body)
	if status != http.StatusOK {
		t.Fatalf("%s %s: %d %s", method, path, status, data)
	}
	return data
}

type clusterE2EStatus struct {
	Role string `json:"role"`
	Node struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"node"`
	Link *struct {
		State     string `json:"state"`
		LastError string `json:"last_error"`
	} `json:"link"`
	Nodes []struct {
		ID             string `json:"id"`
		Role           string `json:"role"`
		Status         string `json:"status"`
		Online         bool   `json:"online"`
		Self           bool   `json:"self"`
		LendingEnabled bool   `json:"lending_enabled"`
		Offers         []struct {
			ModelID          string `json:"model_id"`
			EligibleAccounts int    `json:"eligible_accounts"`
		} `json:"offers"`
	} `json:"nodes"`
	Catalog *struct {
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	} `json:"catalog"`
}

func (n *clusterE2ENode) status(t *testing.T) clusterE2EStatus {
	t.Helper()
	var status clusterE2EStatus
	if err := json.Unmarshal(n.mustAdmin(t, http.MethodGet, "/api/admin/cluster", nil), &status); err != nil {
		t.Fatal(err)
	}
	return status
}

func (s clusterE2EStatus) linkState() string {
	if s.Link == nil {
		return ""
	}
	return s.Link.State
}

func (s clusterE2EStatus) online(nodeID string) bool {
	for _, node := range s.Nodes {
		if node.ID == nodeID {
			return node.Online && node.Status == cluster.NodeActive
		}
	}
	return false
}

func (s clusterE2EStatus) offers(nodeID, model string) bool {
	for _, node := range s.Nodes {
		if node.ID != nodeID || !node.Online || !node.LendingEnabled {
			continue
		}
		for _, offer := range node.Offers {
			if offer.ModelID == model && offer.EligibleAccounts > 0 {
				return true
			}
		}
	}
	return false
}

func (s clusterE2EStatus) catalogHas(model string) bool {
	if s.Catalog == nil {
		return false
	}
	return slices.ContainsFunc(s.Catalog.Models, func(entry struct {
		ID string `json:"id"`
	}) bool {
		return entry.ID == model
	})
}

// chat calls the node's public endpoint with a client key.
func (n *clusterE2ENode) chat(t *testing.T, key, model string, stream bool) (*http.Response, string) {
	t.Helper()
	encoded, _ := json.Marshal(map[string]any{"model": model, "stream": stream, "messages": []map[string]any{{"role": "user", "content": "hi"}}})
	request, err := http.NewRequest(http.MethodPost, n.http.URL+"/v1/chat/completions", bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	return response, string(data)
}

// saveRoutes re-saves a model exactly as stored, replacing only its routes, the
// way the dashboard edits routes on a hub-managed model.
func (n *clusterE2ENode) saveRoutes(t *testing.T, modelID string, routes ...config.RouteTargetConfig) {
	t.Helper()
	current, err := n.store.PublicModel(context.Background(), modelID)
	if err != nil {
		t.Fatal(err)
	}
	n.mustAdmin(t, http.MethodPost, "/api/admin/models", config.PublicModelConfig{
		ID: current.ID, DisplayName: current.DisplayName, Aliases: current.Aliases, Enabled: current.Enabled,
		ExposeUpstreamName: current.ExposeUpstreamName, RewriteResponseModel: current.RewriteResponseModel,
		Capabilities: current.Capabilities, Limits: current.Limits, Routes: routes,
	})
}

func (n *clusterE2ENode) peerServes(nodeID, model string) bool {
	credentials, err := n.store.Credentials(context.Background(), cluster.ProviderID)
	if err != nil {
		return false
	}
	for _, credential := range credentials {
		if credential.ID != cluster.CredentialPrefix+nodeID || !credential.Enabled {
			continue
		}
		switch models := credential.Metadata["serves_models"].(type) {
		case []any:
			if slices.Contains(models, any(model)) {
				return true
			}
		case []string:
			if slices.Contains(models, model) {
				return true
			}
		}
	}
	return false
}

func (n *clusterE2ENode) usage(t *testing.T, clientKeyID, model string) int {
	t.Helper()
	events, err := n.store.RecentUsage(context.Background(), 500)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.ClientAPIKeyID == clientKeyID && event.PublicModelID == model {
			count++
		}
	}
	return count
}

func clusterE2EWait(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func clusterE2ERoute(id, provider, upstream string) config.RouteTargetConfig {
	return config.RouteTargetConfig{ID: id, Provider: provider, UpstreamModel: upstream, Priority: 10, Weight: 1}
}

func clusterE2EToken(t *testing.T, hub *clusterE2ENode) string {
	t.Helper()
	var created struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(hub.mustAdmin(t, http.MethodPost, "/api/admin/cluster/tokens", map[string]any{"label": "e2e"}), &created); err != nil || !strings.HasPrefix(created.Token, cluster.JoinTokenPrefix) {
		t.Fatalf("token: %q %v", created.Token, err)
	}
	return created.Token
}

func TestClusterE2EHubAndMember(t *testing.T) {
	t.Setenv("TPROXY_SKIP_TUNNEL_AUTO", "1")
	t.Setenv("TPROXY_SKIP_CLUSTER_AUTO", "")
	t.Setenv("TPROXY_CLUSTER_JOIN_TOKEN", "")
	previousHook := clusterOptionsHook
	clusterOptionsHook = func(options *cluster.Options) {
		options.PollInterval = 100 * time.Millisecond
		options.PingInterval = 200 * time.Millisecond
		options.PingTimeout = time.Second
	}
	t.Cleanup(func() { clusterOptionsHook = previousHook })

	started := time.Now()
	step := func(name string) { t.Logf("step %s at %s", name, time.Since(started).Round(time.Millisecond)) }
	upstream := newClusterE2EUpstream(t)
	hub := newClusterE2ENode(t, &config.Config{
		Server: config.ServerConfig{Host: "127.0.0.1", Port: 1},
		Providers: []config.ProviderConfig{{ID: "up-hub", Type: "openai-compatible", Name: "Hub upstream", BaseURL: upstream.server.URL, Enabled: true,
			Credentials: []config.CredentialConfig{{ID: "hub-cred", AuthType: "api_key", Secret: "hub-secret"}}}},
		Models: []config.PublicModelConfig{
			{ID: "shared-model", DisplayName: "Shared model", Enabled: true, Routes: []config.RouteTargetConfig{clusterE2ERoute("hub-shared", "up-hub", "up-model")}},
			{ID: "member-model", DisplayName: "Member model", Enabled: true},
			{ID: "loop-model", DisplayName: "Loop model", Enabled: true, Routes: []config.RouteTargetConfig{clusterE2ERoute("hub-loop", "up-hub", "fail-model")}},
		},
	})
	member := newClusterE2ENode(t, &config.Config{
		Server: config.ServerConfig{Host: "127.0.0.1", Port: 1},
		Providers: []config.ProviderConfig{{ID: "up-member", Type: "openai-compatible", Name: "Member upstream", BaseURL: upstream.server.URL, Enabled: true,
			Credentials: []config.CredentialConfig{{ID: "member-cred", AuthType: "api_key", Secret: "member-secret"}}}},
	})

	step("1-join")
	// 1. The hub creates the cluster and shares a catalog; the member joins.
	hub.mustAdmin(t, http.MethodPost, "/api/admin/cluster/create", map[string]any{"name": "e2e", "hub_url": hub.http.URL, "allow_insecure": true})
	hub.mustAdmin(t, http.MethodPut, "/api/admin/cluster/shared", map[string]any{
		"models": []string{"shared-model", "member-model", "loop-model"}, "combos": []string{}, "api_keys": []string{hub.keyID},
	})
	if status, data := hub.admin(t, http.MethodPost, "/api/admin/cluster/join", map[string]any{"token": "garbage"}); status == http.StatusOK {
		t.Fatalf("a hub joined another cluster: %s", data)
	}
	member.mustAdmin(t, http.MethodPost, "/api/admin/cluster/join", map[string]any{"token": clusterE2EToken(t, hub), "node_name": "member", "allow_insecure": true})
	hubID, memberID := hub.status(t).Node.ID, member.status(t).Node.ID
	clusterE2EWait(t, "member online at the hub", func() bool { return hub.status(t).online(memberID) })
	clusterE2EWait(t, "member link connected with the catalog applied", func() bool {
		status := member.status(t)
		return status.linkState() == cluster.LinkConnected && status.catalogHas("shared-model")
	})
	clusterE2EWait(t, "member peer credential for the hub", func() bool { return member.peerServes(hubID, "shared-model") })

	step("2-member-borrows")
	// 2. The member borrows shared-model from the hub's account.
	response, body := member.chat(t, member.key, "shared-model", false)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "hello from hub-secret") {
		t.Fatalf("borrowed chat: %d %s", response.StatusCode, body)
	}
	response, body = member.chat(t, member.key, "shared-model", true)
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") ||
		!strings.Contains(body, "hub-secret") || !strings.Contains(body, "[DONE]") {
		t.Fatalf("borrowed stream: %d %q %s", response.StatusCode, response.Header.Get("Content-Type"), body)
	}
	if upstream.count("hub-secret", "up-model") < 2 || upstream.count("member-secret", "up-model") != 0 {
		t.Fatalf("upstream calls: %+v", upstream.calls)
	}
	clusterE2EWait(t, "hub usage attributed to the member", func() bool {
		return hub.usage(t, cluster.PrincipalPrefix+memberID, "shared-model") >= 2
	})

	step("3-hub-borrows")
	// 3. A route-only save on a managed model is allowed; the hub then borrows it
	// from the member over the link.
	member.saveRoutes(t, "member-model", clusterE2ERoute("member-local", "up-member", "up-model"))
	clusterE2EWait(t, "member offering member-model", func() bool { return hub.status(t).offers(memberID, "member-model") })
	before := upstream.count("member-secret", "up-model")
	response, body = hub.chat(t, hub.key, "member-model", false)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "hello from member-secret") || upstream.count("member-secret", "up-model") != before+1 {
		t.Fatalf("hub borrowing from member: %d %s", response.StatusCode, body)
	}
	if member.usage(t, cluster.PrincipalPrefix+hubID, "member-model") == 0 {
		t.Fatal("member did not attribute usage to the hub")
	}

	step("4-lending-limit")
	// 4. The hub's lending limit reaches the member's client as a 429; once the
	// member has its own route, fallback mode serves locally first.
	hub.mustAdmin(t, http.MethodPut, "/api/admin/cluster/lending", map[string]any{"enabled": true, "models": []string{}, "limits": map[string]any{"requests_per_minute": 1}})
	limited := false
	for range 3 {
		response, body = member.chat(t, member.key, "shared-model", false)
		if response.StatusCode == http.StatusOK {
			continue
		}
		if response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Retry-After") == "" {
			t.Fatalf("lending limit: %d retry-after=%q %s", response.StatusCode, response.Header.Get("Retry-After"), body)
		}
		limited = true
		break
	}
	if !limited {
		t.Fatal("hub lending limit never applied")
	}
	member.saveRoutes(t, "shared-model", clusterE2ERoute("member-shared", "up-member", "up-model"))
	member.mustAdmin(t, http.MethodPut, "/api/admin/cluster/borrowing", map[string]any{"enabled": true, "mode": "fallback"})
	hubCalls, memberCalls := upstream.count("hub-secret", "up-model"), upstream.count("member-secret", "up-model")
	response, body = member.chat(t, member.key, "shared-model", false)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "member-secret") ||
		upstream.count("hub-secret", "up-model") != hubCalls || upstream.count("member-secret", "up-model") != memberCalls+1 {
		t.Fatalf("local fallback: %d %s", response.StatusCode, body)
	}

	step("5-shared-key")
	// 5. The hub's shared client key works on the member.
	response, body = member.chat(t, hub.key, "shared-model", false)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "member-secret") {
		t.Fatalf("shared key on member: %d %s", response.StatusCode, body)
	}
	member.mustAdmin(t, http.MethodPut, "/api/admin/cluster/lending", map[string]any{"enabled": true, "models": []string{"member-model"}})
	emptyCache := map[string]struct{}{}
	member.server.managedKeys.Store(&emptyCache)
	response, body = member.chat(t, hub.key, "shared-model", false)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("shared key bypassed lending on cache miss: %d %s", response.StatusCode, body)
	}
	member.mustAdmin(t, http.MethodPut, "/api/admin/cluster/lending", map[string]any{"enabled": true, "models": []string{}})

	step("6-no-loop")
	// 6. A model that fails on both nodes must not bounce: the member serves the
	// hub's relay from local accounts only and never relays it back.
	member.saveRoutes(t, "loop-model", clusterE2ERoute("member-loop", "up-member", "fail-model"))
	clusterE2EWait(t, "member offering loop-model", func() bool { return hub.status(t).offers(memberID, "loop-model") })
	loopStarted := time.Now()
	response, body = hub.chat(t, hub.key, "loop-model", false)
	if response.StatusCode == http.StatusOK || time.Since(loopStarted) > 30*time.Second {
		t.Fatalf("loop-model: %d after %s %s", response.StatusCode, time.Since(loopStarted), body)
	}
	if member.usage(t, cluster.PrincipalPrefix+hubID, "loop-model") == 0 {
		t.Fatalf("hub never relayed loop-model to the member: %s", body)
	}
	if relayedBack := hub.usage(t, cluster.PrincipalPrefix+memberID, "loop-model"); relayedBack != 0 {
		t.Fatalf("member relayed loop-model back to the hub %d times", relayedBack)
	}

	step("7-revoke")
	// 7. Revoking the member closes its membership and clears its peers.
	if status, data := hub.admin(t, http.MethodDelete, "/api/admin/cluster/nodes/"+memberID, nil); status != http.StatusOK {
		t.Fatalf("revoke: %d %s", status, data)
	}
	clusterE2EWait(t, "member revoked", func() bool { return member.status(t).linkState() == cluster.LinkRevoked })
	clusterE2EWait(t, "member peers cleared", func() bool {
		credentials, err := member.store.Credentials(context.Background(), cluster.ProviderID)
		return err == nil && len(credentials) == 0
	})
	// Simulate a stale managed row left behind after revocation.
	if _, err := member.store.ApplyClusterCatalog(context.Background(), store.ClusterCatalog{APIKeys: []store.ClusterCatalogAPIKey{{ID: hub.keyID, Name: "shared", KeyHash: security.HashAPIKey(hub.key), Enabled: true, Models: []string{"shared-model"}}}}); err != nil {
		t.Fatal(err)
	}
	response, body = member.chat(t, hub.key, "shared-model", false)
	if response.StatusCode != http.StatusForbidden || !strings.Contains(body, "cluster_not_active") {
		t.Fatalf("shared key on revoked member: %d %s", response.StatusCode, body)
	}

	step("8-rejoin-leave")
	// 8. A revoked member can join again with a new token, then leave cleanly.
	member.mustAdmin(t, http.MethodPost, "/api/admin/cluster/join", map[string]any{"token": clusterE2EToken(t, hub), "node_name": "member", "allow_insecure": true})
	clusterE2EWait(t, "member reconnected", func() bool { return member.status(t).linkState() == cluster.LinkConnected })
	clusterE2EWait(t, "member online again", func() bool { return hub.status(t).online(memberID) })
	member.mustAdmin(t, http.MethodPost, "/api/admin/cluster/leave", nil)
	if status := member.status(t); status.Role != string(cluster.RoleStandalone) {
		t.Fatalf("role after leave: %q", status.Role)
	}
	if _, err := member.store.Provider(context.Background(), cluster.ProviderID); err == nil {
		t.Fatal("cluster provider survived leave")
	}
	managed, err := member.store.ClusterManagedItems(context.Background())
	if err != nil || len(managed.Models)+len(managed.Combos)+len(managed.APIKeys)+len(managed.Aliases) != 0 {
		t.Fatalf("managed items after leave: %+v %v", managed, err)
	}
}
