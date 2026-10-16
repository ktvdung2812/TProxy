package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/router"
	"github.com/tproxy/tproxy/internal/store"
)

func limitsRequest(handler http.Handler, method, path, token, body, idempotency string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if idempotency != "" {
		req.Header.Set("Idempotency-Key", idempotency)
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func TestInheritedLimitsSummaryAndExplicitScopes(t *testing.T) {
	cfg := &config.Config{Limits: config.LimitPolicy{RequestsPerMinute: 90}, Teams: []config.TeamPolicyConfig{{ID: "team", Limits: config.LimitPolicy{RequestsPerMinute: 80}}}, Providers: []config.ProviderConfig{{ID: "p", Type: "openai-compatible", Enabled: true, Limits: config.LimitPolicy{RequestsPerMinute: 100, BudgetUSDPerDay: 10}, Credentials: []config.CredentialConfig{{ID: "a", AuthType: "none", Metadata: map[string]any{"max_concurrent_requests": 3}}, {ID: "b", AuthType: "none", Metadata: map[string]any{"max_concurrent_requests": 5}}}}}, Models: []config.PublicModelConfig{{ID: "m", Enabled: true, Routes: []config.RouteTargetConfig{{ID: "r", Provider: "p", UpstreamModel: "up"}}}}}
	data := apiTestStore(t, cfg)
	keyID, _, err := data.CreateAPIKey(context.Background(), "old", "Old", []string{"m"}, config.ClientKeyPolicy{Team: "team", Limits: config.LimitPolicy{RequestsPerMinute: 50}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = data.CreateAPIKey(context.Background(), "new", "New", []string{"m"})
	if err != nil {
		t.Fatal(err)
	}
	if err := data.AddUsage(context.Background(), store.UsageEvent{RequestID: "spent", ClientAPIKeyID: keyID, ProviderID: "p", CredentialID: "a", EstimatedCostUSD: 4, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s := NewServer(cfg, data, router.New(data, providers.NewRegistry()))
	defer s.Close()
	res := limitsRequest(s.Handler(), http.MethodGet, "/api/admin/quota/summary?model=m", testDashboardPassword, "", "")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	var payload struct {
		APIKeys []struct {
			ID string `json:"id"`
			router.CapacitySnapshot
		} `json:"api_keys"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range payload.APIKeys {
		wantRPM := float64(90)
		if key.ID == "old" {
			wantRPM = 50
		}
		if *key.SharedLimits["requests_per_minute"].Value != 100 || key.EligibleAccountCount != 2 || *key.EffectiveLimits["concurrent_requests"].Value != 8 || *key.EffectiveLimits["requests_per_minute"].Value != wantRPM || *key.EffectiveLimits["budget_usd_per_day"].Remaining != 6 {
			t.Fatalf("key capacity = %+v", key)
		}
	}
	keys, _ := data.APIKeys(context.Background())
	for _, key := range keys {
		if key.Policy.Limits.ConcurrentStreams != 0 {
			t.Fatal("derived capacity was persisted")
		}
	}
	key := &store.APIKey{ID: "new", Models: []string{"m"}}
	release, err := s.acquireClientStream(key)
	if err != nil {
		t.Fatal(err)
	}
	s.setConfig(&config.Config{Limits: config.LimitPolicy{ConcurrentStreams: 1}})
	release()
	release()
	if s.limiter.streams["global"] != 0 || s.limiter.streams["key:new"] != 0 {
		t.Fatal("release recalculated scopes from changed config")
	}
}

func TestInheritedOutputLimitAcrossHTTPAndMCP(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if intValue(body["max_tokens"]) != 3 {
			t.Errorf("upstream output limit: %+v", body)
		}
		if r.Header.Get("Authorization") != "Bearer account-a" {
			t.Errorf("credential policy lost: %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"ok","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer upstream.Close()
	cfg := &config.Config{Limits: config.LimitPolicy{MaxOutputTokens: 10}, Providers: []config.ProviderConfig{{ID: "p", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true, Limits: config.LimitPolicy{MaxOutputTokens: 3}, Credentials: []config.CredentialConfig{{ID: "a", AuthType: "api_key", Secret: "account-a", Priority: 1}, {ID: "b", AuthType: "api_key", Secret: "account-b", Priority: 10}}}}, Models: []config.PublicModelConfig{{ID: "m", Enabled: true, Routes: []config.RouteTargetConfig{{ID: "r", Provider: "p", UpstreamModel: "up"}}}}}
	data := apiTestStore(t, cfg)
	_, token, err := data.CreateAPIKey(context.Background(), "client", "Client", []string{"m"}, config.ClientKeyPolicy{CredentialIDs: []string{"a"}, Limits: config.LimitPolicy{MaxOutputTokens: 5}})
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(cfg, data, router.New(data, providers.NewRegistry()))
	defer s.Close()
	for _, tc := range []struct{ path, body string }{
		{"/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`},
		{"/v1/responses", `{"model":"m","input":"hi"}`},
		{"/v1/messages", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`},
		{"/v1beta/models/m:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`},
		{"/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tproxy_chat","arguments":{"prompt":"hi"}}}`},
	} {
		res := limitsRequest(s.Handler(), http.MethodPost, tc.path, token, tc.body, "")
		if res.Code != http.StatusOK || strings.Contains(res.Body.String(), `"error"`) {
			t.Errorf("%s: %d %s", tc.path, res.Code, res.Body.String())
		}
	}
	if calls.Load() != 5 {
		t.Fatalf("upstream calls=%d", calls.Load())
	}
	res := limitsRequest(s.Handler(), http.MethodPost, "/v1/responses", token, `{"model":"m","input":"hi","max_output_tokens":6}`, "")
	if res.Code != http.StatusBadRequest || calls.Load() != 5 {
		t.Fatal("explicit key cap not enforced", res.Body.String())
	}
}

func TestResponsesWebSocketLimitsEveryTurn(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"ok\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: {\"id\":\"ok\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	cfg := &config.Config{Limits: config.LimitPolicy{RequestsPerMinute: 2, MaxOutputTokens: 5}, Providers: []config.ProviderConfig{{ID: "p", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "a", AuthType: "none"}}}}, Models: []config.PublicModelConfig{{ID: "m", Enabled: true, Routes: []config.RouteTargetConfig{{ID: "r", Provider: "p", UpstreamModel: "up"}}}}}
	data := apiTestStore(t, cfg)
	_, token, err := data.CreateAPIKey(context.Background(), "key", "Key", []string{"m"})
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(cfg, data, router.New(data, providers.NewRegistry()))
	defer s.Close()
	app := httptest.NewServer(s.Handler())
	defer app.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(app.URL, "http")+"/v1/responses/ws", http.Header{"Authorization": []string{"Bearer " + token}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for i := range 3 {
		if err := conn.WriteJSON(map[string]any{"type": "response.create", "request_id": fmt.Sprint(i), "response": map[string]any{"model": "m", "input": "hi"}}); err != nil {
			t.Fatal(err)
		}
		for {
			var frame map[string]any
			if err := conn.ReadJSON(&frame); err != nil {
				t.Fatal(err)
			}
			if frame["type"] == "error" {
				encoded, _ := json.Marshal(frame)
				if i != 2 || !strings.Contains(string(encoded), "rate_limit_exceeded") {
					t.Fatal(string(encoded))
				}
				break
			}
			if frame["type"] == "response.completed" {
				if i == 2 {
					t.Fatal("third turn bypassed RPM")
				}
				break
			}
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("handshake double-counted or a turn bypassed admission: %d", calls.Load())
	}
	if err := data.UpdateAPIKey(context.Background(), "key", "Key", []string{"m"}, false); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteJSON(map[string]any{"type": "response.create", "response": map[string]any{"model": "m", "input": "hi"}}); err != nil {
		t.Fatal(err)
	}
	var rejected map[string]any
	if err := conn.ReadJSON(&rejected); err != nil {
		t.Fatal(err)
	}
	if rejected["status"] != float64(http.StatusUnauthorized) || calls.Load() != 2 {
		t.Fatalf("revoked key reused websocket: %+v", rejected)
	}
}

func TestMediaReservationAndIdempotencyAcrossKeys(t *testing.T) {
	started, finish := make(chan struct{}, 1), make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"id":"video-1","status":"completed"}`)
			return
		}
		n := calls.Add(1)
		if n == 1 {
			started <- struct{}{}
			<-finish
		}
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, `{"id":"video-%d","status":"queued"}`, n)
	}))
	defer upstream.Close()
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "p", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true, Limits: config.LimitPolicy{MediaJobs: 1}, Credentials: []config.CredentialConfig{{ID: "a", AuthType: "none"}}}}, Models: []config.PublicModelConfig{{ID: "m", Enabled: true, Capabilities: []string{"video-output"}, Routes: []config.RouteTargetConfig{{ID: "r", Provider: "p", UpstreamModel: "up"}}}}}
	data := apiTestStore(t, cfg)
	_, a, _ := data.CreateAPIKey(context.Background(), "a", "A", []string{"m"})
	_, b, _ := data.CreateAPIKey(context.Background(), "b", "B", []string{"m"})
	s := NewServer(cfg, data, router.New(data, providers.NewRegistry()))
	defer s.Close()
	handler := s.Handler()
	create := func(token, id string) *httptest.ResponseRecorder {
		return limitsRequest(handler, http.MethodPost, "/v1/videos", token, `{"model":"m","prompt":"clip"}`, id)
	}
	first, duplicate := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- create(a, "same") }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(finish)
		t.Fatal("creation did not start")
	}
	go func() { duplicate <- create(a, "same") }()
	blocked := create(b, "different")
	close(finish)
	if blocked.Code != http.StatusTooManyRequests {
		t.Fatal("pending provider capacity was not shared", blocked.Body.String())
	}
	if res := <-first; res.Code != http.StatusAccepted {
		t.Fatal(res.Body.String())
	}
	if res := <-duplicate; res.Code != http.StatusOK {
		t.Fatal("duplicate did not use persisted result", res.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate dispatched upstream")
	}
	if res := create(b, "later"); res.Code != http.StatusTooManyRequests {
		t.Fatal("persisted capacity lost", res.Body.String())
	}
	if res := limitsRequest(handler, http.MethodGet, "/v1/videos/video-1", a, "", ""); res.Code != http.StatusOK {
		t.Fatal(res.Body.String())
	}
	if res := create(b, "later"); res.Code != http.StatusAccepted {
		t.Fatal("terminal job did not free capacity", res.Body.String())
	}
}

func TestBackgroundQuotaRefreshWithoutDashboard(t *testing.T) {
	var used, calls atomic.Int32
	used.Store(100)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprintf(w, `{"usage":{"limit":100,"used":%d,"remaining":%d}}`, used.Load(), 100-used.Load())
	}))
	defer upstream.Close()
	off := false
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "kimi", Type: "kimi", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "auto", AuthType: "api_key", Secret: "test"}, {ID: "manual", AuthType: "api_key", Secret: "test", Enabled: &off}}}}}
	data := apiTestStore(t, cfg)
	// Start with a full window, then restart with a recovered window. No browser
	// or quota HTTP endpoint participates in either background refresh.
	for _, depleted := range []bool{true, false} {
		if !depleted {
			used.Store(0)
		}
		router := router.New(data, providers.NewRegistry())
		s := NewServer(cfg, data, router)
		s.StartBackground(context.Background())
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) && router.CachedQuota("auto", "kimi").CheckedAt.IsZero() {
			time.Sleep(time.Millisecond)
		}
		s.Close()
		account, err := data.CredentialByID(context.Background(), "auto")
		if err != nil || account.Enabled == depleted || store.QuotaAutoDisabled(account.Metadata) != depleted {
			t.Fatalf("depleted=%v account=%+v err=%v", depleted, account, err)
		}
		manual, _ := data.CredentialByID(context.Background(), "manual")
		if manual.Enabled {
			t.Fatal("manual disable was overwritten")
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("background probes=%d, want two auto-account probes", calls.Load())
	}
}

func TestMCPChunkedRequestInheritsSourceInputLimit(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "p", Type: "openai-compatible", Enabled: true, Limits: config.LimitPolicy{MaxInputBytes: 20}, Credentials: []config.CredentialConfig{{ID: "a", AuthType: "none"}}}}, Models: []config.PublicModelConfig{{ID: "m", Enabled: true, Routes: []config.RouteTargetConfig{{ID: "r", Provider: "p", UpstreamModel: "up"}}}}}
	data := apiTestStore(t, cfg)
	_, token, _ := data.CreateAPIKey(context.Background(), "key", "Key", []string{"m"})
	s := NewServer(cfg, data, router.New(data, providers.NewRegistry()))
	defer s.Close()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tproxy_chat","arguments":{"prompt":"hi"}}}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.ContentLength = -1
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusTooManyRequests || !strings.Contains(res.Body.String(), `"status":429`) {
		t.Fatalf("MCP lost source limit/status: %d %s", res.Code, res.Body.String())
	}
}

func TestMediaPollingHonorsSourceRPMAndChangedKeyAccess(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"id":"video-direct","status":"queued"}`)
	}))
	defer upstream.Close()
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "p", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true, Limits: config.LimitPolicy{RequestsPerMinute: 1}, Credentials: []config.CredentialConfig{{ID: "a", AuthType: "none", Priority: 10}, {ID: "b", AuthType: "none", Priority: 1}}}}}
	data := apiTestStore(t, cfg)
	_, token, _ := data.CreateAPIKey(context.Background(), "key", "Key", []string{"p:up"})
	s := NewServer(cfg, data, router.New(data, providers.NewRegistry()))
	defer s.Close()
	created := limitsRequest(s.Handler(), http.MethodPost, "/v1/videos", token, `{"model":"p:up","prompt":"clip"}`, "")
	if created.Code != http.StatusAccepted {
		t.Fatal(created.Body.String())
	}
	poll := limitsRequest(s.Handler(), http.MethodGet, "/v1/videos/video-direct", token, "", "")
	if poll.Code != http.StatusTooManyRequests || poll.Header().Get("Retry-After") == "" || calls.Load() != 1 {
		t.Fatalf("poll bypassed source RPM: %d %s", poll.Code, poll.Body.String())
	}
	if err := data.UpdateAPIKey(context.Background(), "key", "Key", []string{"p:up"}, true, config.ClientKeyPolicy{CredentialIDs: []string{"b"}}); err != nil {
		t.Fatal(err)
	}
	poll = limitsRequest(s.Handler(), http.MethodGet, "/v1/videos/video-direct", token, "", "")
	if poll.Code != http.StatusForbidden || calls.Load() != 1 {
		t.Fatalf("poll ignored changed account access: %d %s", poll.Code, poll.Body.String())
	}
}
