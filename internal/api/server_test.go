package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/router"
	"github.com/tproxy/tproxy/internal/security"
	"github.com/tproxy/tproxy/internal/store"
)

func TestChatAliasAndAPIKeyAuthentication(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "upstream-model" {
			t.Errorf("upstream model = %v", body["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","model":"upstream-model","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`))
	}))
	defer upstream.Close()

	t.Setenv("TPROXY_TEST_API_KEY", "client-secret")
	cfg := &config.Config{
		Server:        config.ServerConfig{AllowLocalWithoutKey: false},
		Security:      config.SecurityConfig{ManagementSecretEnv: "TPROXY_TEST_MANAGEMENT"},
		ClientAPIKeys: []config.ClientAPIKey{{ID: "test-client", Name: "Test", KeyEnv: "TPROXY_TEST_API_KEY"}},
		Providers:     []config.ProviderConfig{{ID: "mock", Type: "openai-compatible", Name: "Mock", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "mock-credential", AuthType: "none"}}}},
		Models:        []config.PublicModelConfig{{ID: "td-coder", DisplayName: "TD Coder", Aliases: []string{"coder"}, Enabled: true, RewriteResponseModel: true, Routes: []config.RouteTargetConfig{{ID: "route", Provider: "mock", UpstreamModel: "upstream-model", Priority: 10}}}},
	}
	dataStore := apiTestStore(t, cfg)
	handler := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler()

	unauthorized := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"coder","messages":[{"role":"user","content":"hi"}]}`))
	unauthorized.RemoteAddr = "203.0.113.10:1234"
	unauthorizedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedRecorder, unauthorized)
	if unauthorizedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorizedRecorder.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"coder","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Authorization", "Bearer client-secret")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"model":"td-coder"`) {
		t.Fatalf("public model not rewritten: %s", recorder.Body.String())
	}
}

func TestProviderPrefixedRouteRewritesPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("upstream path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","model":"upstream-model","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`))
	}))
	defer upstream.Close()

	t.Setenv("TPROXY_TEST_API_KEY", "client-secret")
	cfg := &config.Config{
		Server:        config.ServerConfig{AllowLocalWithoutKey: false},
		Security:      config.SecurityConfig{ManagementSecretEnv: "TPROXY_TEST_MANAGEMENT"},
		ClientAPIKeys: []config.ClientAPIKey{{ID: "test-client", Name: "Test", KeyEnv: "TPROXY_TEST_API_KEY"}},
		Providers:     []config.ProviderConfig{{ID: "mock", Type: "openai-compatible", Name: "Mock", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "mock-credential", AuthType: "none"}}}},
		Models:        []config.PublicModelConfig{{ID: "td-coder", DisplayName: "TD Coder", Aliases: []string{"coder"}, Enabled: true, RewriteResponseModel: true, Routes: []config.RouteTargetConfig{{ID: "route", Provider: "mock", UpstreamModel: "upstream-model", Priority: 10}}}},
	}
	dataStore := apiTestStore(t, cfg)
	handler := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler()

	request := httptest.NewRequest(http.MethodPost, "/mock/v1/chat/completions", bytes.NewBufferString(`{"model":"coder","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Authorization", "Bearer client-secret")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestQueryCredentialRejected(t *testing.T) {
	cfg := &config.Config{Server: config.ServerConfig{AllowLocalWithoutKey: false}}
	dataStore := apiTestStore(t, cfg)
	handler := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler()

	request := httptest.NewRequest(http.MethodGet, "/v1/models?api_key=secret", nil)
	request.Header.Set("Authorization", "Bearer secret")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAdminCanCreateVirtualModelWithoutExposingSecrets(t *testing.T) {
	cfg := &config.Config{
		Server:    config.ServerConfig{AllowRemoteManagement: false},
		Providers: []config.ProviderConfig{{ID: "mock", Type: "openai-compatible", Name: "Mock", BaseURL: "http://127.0.0.1", Enabled: true, Credentials: []config.CredentialConfig{{ID: "mock-credential", AuthType: "none"}}}},
	}
	dataStore := apiTestStore(t, cfg)
	handler := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler()
	body := `{"id":"td-fast","display_name":"TD Fast","aliases":["fast"],"enabled":true,"rewrite_response_model":true,"capabilities":["text"],"routes":[{"id":"fast-route","provider":"mock","upstream_model":"upstream-fast","priority":20}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/admin/models", bytes.NewBufferString(body))
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	model, err := dataStore.ResolveModel(context.Background(), "fast", "")
	if err != nil {
		t.Fatal(err)
	}
	if model.ID != "td-fast" || !model.RewriteResponseModel {
		t.Fatalf("model=%+v", model)
	}
}

func TestRemoteManagementRequiresSecret(t *testing.T) {
	cfg := &config.Config{Server: config.ServerConfig{AllowRemoteManagement: true}}
	dataStore := apiTestStore(t, cfg)
	server := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry()))
	defer server.Close()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/snapshot", nil)
	request.RemoteAddr = "203.0.113.10:1234"
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "management_secret_required") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMediaProxyUsesUpstreamModelAndRewritesResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "embedding-upstream" {
			t.Errorf("upstream model = %v", body["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","model":"embedding-upstream","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}]}`))
	}))
	defer upstream.Close()
	cfg := &config.Config{
		Server:    config.ServerConfig{AllowLocalWithoutKey: true},
		Providers: []config.ProviderConfig{{ID: "mock", Type: "openai-compatible", Name: "Mock", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "mock-credential", AuthType: "none"}}}},
		Models:    []config.PublicModelConfig{{ID: "td-embed", DisplayName: "TD Embed", Aliases: []string{"embed"}, Enabled: true, RewriteResponseModel: true, Capabilities: []string{"embedding"}, Routes: []config.RouteTargetConfig{{ID: "route", Provider: "mock", UpstreamModel: "embedding-upstream", Priority: 10}}}},
	}
	dataStore := apiTestStore(t, cfg)
	handler := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler()
	request := httptest.NewRequest(http.MethodPost, "/v1/embeddings", bytes.NewBufferString(`{"model":"embed","input":"hello"}`))
	request.RemoteAddr = "127.0.0.1:1234"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"model":"td-embed"`) {
		t.Fatalf("model not rewritten: %s", recorder.Body.String())
	}
}

func TestMediaCreationRequiresIdempotencyKeyBeforeAmbiguousNetworkFallback(t *testing.T) {
	var firstCalls, secondCalls atomic.Int32
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstCalls.Add(1)
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("test server does not support hijacking")
		}
		connection, _, err := hijacker.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		_ = connection.Close()
	}))
	defer failing.Close()
	success := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondCalls.Add(1)
		if r.Header.Get("Idempotency-Key") != "image-job-1" || r.Header.Get("X-Request-ID") == "" {
			t.Errorf("forwarded headers = %+v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"image-1","model":"image-upstream"}`))
	}))
	defer success.Close()
	cfg := &config.Config{
		Server: config.ServerConfig{AllowLocalWithoutKey: true},
		Providers: []config.ProviderConfig{
			{ID: "failing", Type: "openai-compatible", BaseURL: failing.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "failing-credential", AuthType: "none"}}},
			{ID: "success", Type: "openai-compatible", BaseURL: success.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "success-credential", AuthType: "none"}}},
		},
		Models: []config.PublicModelConfig{{ID: "td-image", Enabled: true, RewriteResponseModel: true, Capabilities: []string{"image-output"}, Routes: []config.RouteTargetConfig{
			{ID: "first", Provider: "failing", UpstreamModel: "image-upstream", Priority: 100},
			{ID: "second", Provider: "success", UpstreamModel: "image-upstream", Priority: 50},
		}}},
	}
	dataStore := apiTestStore(t, cfg)
	handler := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler()
	withoutKey := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewBufferString(`{"model":"td-image","prompt":"draw"}`))
	withoutKey.RemoteAddr = "127.0.0.1:1234"
	withoutKey.Header.Set("Content-Type", "application/json")
	withoutKeyRecorder := httptest.NewRecorder()
	handler.ServeHTTP(withoutKeyRecorder, withoutKey)
	if withoutKeyRecorder.Code != http.StatusBadGateway || !strings.Contains(withoutKeyRecorder.Body.String(), "ambiguous_upstream_failure") || firstCalls.Load() != 1 || secondCalls.Load() != 0 {
		t.Fatalf("without key status=%d body=%s first=%d second=%d", withoutKeyRecorder.Code, withoutKeyRecorder.Body.String(), firstCalls.Load(), secondCalls.Load())
	}
	withKey := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewBufferString(`{"model":"td-image","prompt":"draw"}`))
	withKey.RemoteAddr = "127.0.0.1:1234"
	withKey.Header.Set("Content-Type", "application/json")
	withKey.Header.Set("Idempotency-Key", "image-job-1")
	withKeyRecorder := httptest.NewRecorder()
	handler.ServeHTTP(withKeyRecorder, withKey)
	if withKeyRecorder.Code != http.StatusOK || firstCalls.Load() != 2 || secondCalls.Load() != 1 || !strings.Contains(withKeyRecorder.Body.String(), `"model":"td-image"`) {
		t.Fatalf("with key status=%d body=%s first=%d second=%d", withKeyRecorder.Code, withKeyRecorder.Body.String(), firstCalls.Load(), secondCalls.Load())
	}
}

func TestWebFetchBlocksLoopback(t *testing.T) {
	cfg := &config.Config{Server: config.ServerConfig{AllowLocalWithoutKey: true}}
	dataStore := apiTestStore(t, cfg)
	handler := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler()
	request := httptest.NewRequest(http.MethodPost, "/v1/web/fetch", bytes.NewBufferString(`{"url":"http://127.0.0.1:8080/internal"}`))
	request.RemoteAddr = "127.0.0.1:1234"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestResponsesWebSocketUsesClientAuthAndRewritesModel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("upstream path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"stream-1\",\"model\":\"upstream-ws\",\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"stream-1\",\"model\":\"upstream-ws\",\"choices\":[{\"delta\":{\"content\":\"hello ws\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"stream-1\",\"model\":\"upstream-ws\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()
	t.Setenv("TPROXY_WS_API_KEY", "ws-client-key")
	cfg := &config.Config{
		ClientAPIKeys: []config.ClientAPIKey{{ID: "ws-client", Name: "WebSocket", KeyEnv: "TPROXY_WS_API_KEY"}},
		Providers:     []config.ProviderConfig{{ID: "ws-provider", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "ws-credential", AuthType: "none"}}}},
		Models:        []config.PublicModelConfig{{ID: "td-ws", Enabled: true, RewriteResponseModel: true, Routes: []config.RouteTargetConfig{{ID: "ws-route", Provider: "ws-provider", UpstreamModel: "upstream-ws", Priority: 10}}}},
	}
	dataStore := apiTestStore(t, cfg)
	app := httptest.NewServer(NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler())
	defer app.Close()
	wsURL := "ws" + strings.TrimPrefix(app.URL, "http") + "/v1/responses/ws"
	connection, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Authorization": []string{"Bearer ws-client-key"}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err = connection.WriteJSON(map[string]any{"type": "response.create", "request_id": "ws-req-1", "response": map[string]any{"model": "td-ws", "input": "hello"}}); err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	completed := false
	for i := 0; i < 8 && !completed; i++ {
		var frame map[string]any
		if err = connection.ReadJSON(&frame); err != nil {
			t.Fatal(err)
		}
		switch frame["type"] {
		case "response.created":
			response, _ := frame["response"].(map[string]any)
			if response["model"] != "td-ws" {
				t.Fatalf("created response = %+v", response)
			}
		case "response.output_text.delta":
			text.WriteString(stringValue(frame["delta"]))
		case "response.completed":
			completed = true
		case "error":
			t.Fatalf("websocket error = %+v", frame)
		}
	}
	if !completed || text.String() != "hello ws" {
		t.Fatalf("completed=%v text=%q", completed, text.String())
	}
	_ = connection.Close()
	deadline := time.Now().Add(time.Second)
	for {
		logs, logErr := dataStore.RecentRequestLogs(context.Background(), 10)
		if logErr != nil {
			t.Fatal(logErr)
		}
		found := false
		for _, item := range logs {
			if item.Path == "/v1/responses/ws" && item.Status == http.StatusSwitchingProtocols && item.PublicModelID == "td-ws" && item.ProviderID == "ws-provider" {
				found = true
				break
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("WebSocket request was not recorded: %+v", logs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestProxyPoolCRUDBindingHealthTestAndRedaction(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()
	cfg := &config.Config{Server: config.ServerConfig{AllowLocalWithoutKey: true}}
	dataStore := apiTestStore(t, cfg)
	handler := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler()
	postPool := httptest.NewRequest(http.MethodPost, "/api/admin/proxy-pools", bytes.NewBufferString(`{"id":"pool-1","name":"Primary","url":"direct","enabled":true}`))
	postPool.RemoteAddr = "127.0.0.1:1234"
	postRecorder := httptest.NewRecorder()
	handler.ServeHTTP(postRecorder, postPool)
	if postRecorder.Code != http.StatusCreated {
		t.Fatalf("create pool status=%d body=%s", postRecorder.Code, postRecorder.Body.String())
	}
	provider := httptest.NewRequest(http.MethodPost, "/api/admin/providers", bytes.NewBufferString(`{"id":"proxy-provider","type":"openai-compatible","base_url":"http://127.0.0.1:1","enabled":true,"proxy_pools":["pool-1"],"credentials":[{"id":"proxy-credential","auth_type":"none"}]}`))
	provider.RemoteAddr = "127.0.0.1:1234"
	provider.Header.Set("Content-Type", "application/json")
	providerRecorder := httptest.NewRecorder()
	handler.ServeHTTP(providerRecorder, provider)
	if providerRecorder.Code != http.StatusOK {
		t.Fatalf("provider status=%d body=%s", providerRecorder.Code, providerRecorder.Body.String())
	}
	testPool := httptest.NewRequest(http.MethodPost, "/api/admin/proxy-pools/pool-1/test", bytes.NewBufferString(`{"target_url":"`+target.URL+`"}`))
	testPool.RemoteAddr = "127.0.0.1:1234"
	testPool.Header.Set("Content-Type", "application/json")
	testRecorder := httptest.NewRecorder()
	handler.ServeHTTP(testRecorder, testPool)
	if testRecorder.Code != http.StatusOK || !strings.Contains(testRecorder.Body.String(), `"ok":true`) {
		t.Fatalf("test pool status=%d body=%s", testRecorder.Code, testRecorder.Body.String())
	}
	snapshot := httptest.NewRequest(http.MethodGet, "/api/admin/snapshot", nil)
	snapshot.RemoteAddr = "127.0.0.1:1234"
	snapshotRecorder := httptest.NewRecorder()
	handler.ServeHTTP(snapshotRecorder, snapshot)
	if !strings.Contains(snapshotRecorder.Body.String(), "pool-1") || strings.Contains(snapshotRecorder.Body.String(), "password") {
		t.Fatalf("proxy pool snapshot=%s", snapshotRecorder.Body.String())
	}
	deleteBound := httptest.NewRequest(http.MethodDelete, "/api/admin/proxy-pools/pool-1", nil)
	deleteBound.RemoteAddr = "127.0.0.1:1234"
	deleteBoundRecorder := httptest.NewRecorder()
	handler.ServeHTTP(deleteBoundRecorder, deleteBound)
	if deleteBoundRecorder.Code != http.StatusConflict {
		t.Fatalf("bound delete status=%d body=%s", deleteBoundRecorder.Code, deleteBoundRecorder.Body.String())
	}
	updateProvider := httptest.NewRequest(http.MethodPut, "/api/admin/providers", bytes.NewBufferString(`{"id":"proxy-provider","type":"openai-compatible","base_url":"http://127.0.0.1:1","enabled":true,"proxy_pools":[],"credentials":[]}`))
	updateProvider.RemoteAddr = "127.0.0.1:1234"
	updateProvider.Header.Set("Content-Type", "application/json")
	updateProviderRecorder := httptest.NewRecorder()
	handler.ServeHTTP(updateProviderRecorder, updateProvider)
	if updateProviderRecorder.Code != http.StatusOK {
		t.Fatalf("provider update status=%d body=%s", updateProviderRecorder.Code, updateProviderRecorder.Body.String())
	}
	deletePool := httptest.NewRequest(http.MethodDelete, "/api/admin/proxy-pools/pool-1", nil)
	deletePool.RemoteAddr = "127.0.0.1:1234"
	deletePoolRecorder := httptest.NewRecorder()
	handler.ServeHTTP(deletePoolRecorder, deletePool)
	if deletePoolRecorder.Code != http.StatusOK {
		t.Fatalf("delete pool status=%d body=%s", deletePoolRecorder.Code, deletePoolRecorder.Body.String())
	}
}

func TestVideoMediaJobPersistsStatusAndDeduplicatesIdempotencyKey(t *testing.T) {
	t.Setenv("TPROXY_VIDEO_API_KEY", "video-client-key")
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"video-job-1","status":"completed","model":"video-upstream"}`))
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "video-upstream" {
			t.Errorf("video upstream model = %v", body["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"video-job-1","status":"queued","model":"video-upstream"}`))
	}))
	defer upstream.Close()
	cfg := &config.Config{
		ClientAPIKeys: []config.ClientAPIKey{{ID: "video-client", Name: "Video client", KeyEnv: "TPROXY_VIDEO_API_KEY"}},
		Providers:     []config.ProviderConfig{{ID: "video-provider", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "video-credential", AuthType: "none"}}}},
		Models:        []config.PublicModelConfig{{ID: "td-video", Enabled: true, RewriteResponseModel: true, Capabilities: []string{"video-output"}, Routes: []config.RouteTargetConfig{{ID: "video-route", Provider: "video-provider", UpstreamModel: "video-upstream", Priority: 10}}}},
	}
	dataStore := apiTestStore(t, cfg)
	handler := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler()
	create := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewBufferString(`{"model":"td-video","prompt":"make a clip"}`))
		request.RemoteAddr = "203.0.113.10:1234"
		request.Header.Set("Authorization", "Bearer video-client-key")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "video-idempotency-1")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	first := create()
	if first.Code != http.StatusAccepted || !strings.Contains(first.Body.String(), "video-job-1") || upstreamCalls.Load() != 1 {
		t.Fatalf("first create status=%d body=%s calls=%d", first.Code, first.Body.String(), upstreamCalls.Load())
	}
	second := create()
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), "queued") || upstreamCalls.Load() != 1 {
		t.Fatalf("deduplicated create status=%d body=%s calls=%d", second.Code, second.Body.String(), upstreamCalls.Load())
	}
	status := httptest.NewRequest(http.MethodGet, "/v1/videos/video-job-1", nil)
	status.RemoteAddr = "203.0.113.10:1234"
	status.Header.Set("Authorization", "Bearer video-client-key")
	statusRecorder := httptest.NewRecorder()
	handler.ServeHTTP(statusRecorder, status)
	if statusRecorder.Code != http.StatusOK || !strings.Contains(statusRecorder.Body.String(), `"status":"completed"`) || !strings.Contains(statusRecorder.Body.String(), `"model":"td-video"`) {
		t.Fatalf("status code=%d body=%s", statusRecorder.Code, statusRecorder.Body.String())
	}
}

func TestAdminOAuthBrowserFlowIsSingleUseAndRedacted(t *testing.T) {
	t.Setenv("TPROXY_API_OAUTH_CLIENT", "api-client")
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("code_verifier") == "" || r.Form.Get("code") != "api-code" {
			t.Errorf("token form = %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"api-access-secret","refresh_token":"api-refresh-secret","expires_in":3600}`))
	}))
	defer providerServer.Close()
	cfg := &config.Config{
		Server:    config.ServerConfig{AllowLocalWithoutKey: true},
		Providers: []config.ProviderConfig{{ID: "oauth", Type: "openai-compatible", Name: "OAuth", BaseURL: providerServer.URL, Enabled: true, OAuth: &config.OAuthConfig{AuthorizationURL: providerServer.URL + "/authorize", TokenURL: providerServer.URL, ClientIDEnv: "TPROXY_API_OAUTH_CLIENT", RedirectURL: "http://127.0.0.1:8317/api/admin/oauth/callback"}}},
	}
	dataStore := apiTestStore(t, cfg)
	server := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry()))
	defer server.Close()
	handler := server.Handler()

	startRequest := httptest.NewRequest(http.MethodPost, "/api/admin/oauth/start", bytes.NewBufferString(`{"provider_id":"oauth","credential_id":"api-oauth","mode":"browser"}`))
	startRequest.RemoteAddr = "127.0.0.1:1234"
	startRequest.Header.Set("Content-Type", "application/json")
	startRecorder := httptest.NewRecorder()
	handler.ServeHTTP(startRecorder, startRequest)
	if startRecorder.Code != http.StatusCreated {
		t.Fatalf("start status=%d body=%s", startRecorder.Code, startRecorder.Body.String())
	}
	var started struct {
		AuthorizationURL string `json:"authorization_url"`
		SessionID        string `json:"session_id"`
	}
	if err := json.Unmarshal(startRecorder.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	authorization, err := url.Parse(started.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	state := authorization.Query().Get("state")
	callbackPath := "/api/admin/oauth/callback?state=" + url.QueryEscape(state) + "&code=api-code"
	callback := httptest.NewRequest(http.MethodGet, callbackPath, nil)
	callback.RemoteAddr = "127.0.0.1:1234"
	callbackRecorder := httptest.NewRecorder()
	handler.ServeHTTP(callbackRecorder, callback)
	if callbackRecorder.Code != http.StatusOK {
		t.Fatalf("callback status=%d body=%s", callbackRecorder.Code, callbackRecorder.Body.String())
	}

	reused := httptest.NewRequest(http.MethodGet, callbackPath, nil)
	reused.RemoteAddr = "127.0.0.1:1234"
	reusedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(reusedRecorder, reused)
	if reusedRecorder.Code != http.StatusBadRequest || !strings.Contains(reusedRecorder.Body.String(), "invalid_state") {
		t.Fatalf("reused callback status=%d body=%s", reusedRecorder.Code, reusedRecorder.Body.String())
	}

	statusRequest := httptest.NewRequest(http.MethodGet, "/api/admin/oauth/status?credential_id=api-oauth", nil)
	statusRequest.RemoteAddr = "127.0.0.1:1234"
	statusRecorder := httptest.NewRecorder()
	handler.ServeHTTP(statusRecorder, statusRequest)
	if statusRecorder.Code != http.StatusOK || strings.Contains(statusRecorder.Body.String(), "api-access-secret") || strings.Contains(statusRecorder.Body.String(), "api-refresh-secret") {
		t.Fatalf("status response=%s", statusRecorder.Body.String())
	}

	snapshotRequest := httptest.NewRequest(http.MethodGet, "/api/admin/snapshot", nil)
	snapshotRequest.RemoteAddr = "127.0.0.1:1234"
	snapshotRecorder := httptest.NewRecorder()
	handler.ServeHTTP(snapshotRecorder, snapshotRequest)
	if strings.Contains(snapshotRecorder.Body.String(), "api-access-secret") || strings.Contains(snapshotRecorder.Body.String(), "api-refresh-secret") {
		t.Fatalf("snapshot leaked token: %s", snapshotRecorder.Body.String())
	}
}

func TestManagementCRUDForCredentialsModelsProvidersAndAPIKeys(t *testing.T) {
	cfg := &config.Config{
		Server:    config.ServerConfig{AllowLocalWithoutKey: false},
		Providers: []config.ProviderConfig{{ID: "crud-provider", Type: "openai-compatible", Name: "CRUD", BaseURL: "http://127.0.0.1:9999", Enabled: true}},
		Models:    []config.PublicModelConfig{{ID: "crud-model", Enabled: true, RewriteResponseModel: true, Routes: []config.RouteTargetConfig{{ID: "crud-route", Provider: "crud-provider", UpstreamModel: "upstream", Priority: 10}}}},
	}
	dataStore := apiTestStore(t, cfg)
	server := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry()))
	defer server.Close()
	handler := server.Handler()

	credentialRequest := httptest.NewRequest(http.MethodPost, "/api/admin/credentials", bytes.NewBufferString(`{"provider_id":"crud-provider","credential":{"id":"crud-credential","label":"Primary","auth_type":"api_key","secret":"provider-secret","priority":20,"weight":1}}`))
	credentialRequest.RemoteAddr = "127.0.0.1:1234"
	credentialRecorder := httptest.NewRecorder()
	handler.ServeHTTP(credentialRecorder, credentialRequest)
	if credentialRecorder.Code != http.StatusOK {
		t.Fatalf("credential status=%d body=%s", credentialRecorder.Code, credentialRecorder.Body.String())
	}

	keyRequest := httptest.NewRequest(http.MethodPost, "/api/admin/api-keys", bytes.NewBufferString(`{"id":"crud-client","name":"CRUD client","models":["crud-model"]}`))
	keyRequest.RemoteAddr = "127.0.0.1:1234"
	keyRecorder := httptest.NewRecorder()
	handler.ServeHTTP(keyRecorder, keyRequest)
	if keyRecorder.Code != http.StatusCreated {
		t.Fatalf("key status=%d body=%s", keyRecorder.Code, keyRecorder.Body.String())
	}
	var createdKey struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(keyRecorder.Body.Bytes(), &createdKey)
	if createdKey.Key == "" {
		t.Fatal("API key was not returned on creation")
	}

	modelsRequest := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	modelsRequest.RemoteAddr = "203.0.113.20:1234"
	modelsRequest.Header.Set("Authorization", "Bearer "+createdKey.Key)
	modelsRecorder := httptest.NewRecorder()
	handler.ServeHTTP(modelsRecorder, modelsRequest)
	if modelsRecorder.Code != http.StatusOK {
		t.Fatalf("client key status=%d body=%s", modelsRecorder.Code, modelsRecorder.Body.String())
	}

	snapshotRequest := httptest.NewRequest(http.MethodGet, "/api/admin/snapshot", nil)
	snapshotRequest.RemoteAddr = "127.0.0.1:1234"
	snapshotRecorder := httptest.NewRecorder()
	handler.ServeHTTP(snapshotRecorder, snapshotRequest)
	if strings.Contains(snapshotRecorder.Body.String(), "provider-secret") || strings.Contains(snapshotRecorder.Body.String(), createdKey.Key) || !strings.Contains(snapshotRecorder.Body.String(), "crud-client") {
		t.Fatalf("snapshot redaction failed: %s", snapshotRecorder.Body.String())
	}

	disableRequest := httptest.NewRequest(http.MethodPut, "/api/admin/api-keys/crud-client", bytes.NewBufferString(`{"name":"CRUD client","models":["crud-model"],"enabled":false}`))
	disableRequest.RemoteAddr = "127.0.0.1:1234"
	disableRecorder := httptest.NewRecorder()
	handler.ServeHTTP(disableRecorder, disableRequest)
	if disableRecorder.Code != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", disableRecorder.Code, disableRecorder.Body.String())
	}
	disabledClientRequest := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	disabledClientRequest.RemoteAddr = "203.0.113.20:1234"
	disabledClientRequest.Header.Set("Authorization", "Bearer "+createdKey.Key)
	disabledClientRecorder := httptest.NewRecorder()
	handler.ServeHTTP(disabledClientRecorder, disabledClientRequest)
	if disabledClientRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("disabled key status=%d", disabledClientRecorder.Code)
	}

	for _, path := range []string{"/api/admin/credentials/crud-credential", "/api/admin/models/crud-model", "/api/admin/providers/crud-provider", "/api/admin/api-keys/crud-client"} {
		request := httptest.NewRequest(http.MethodDelete, path, nil)
		request.RemoteAddr = "127.0.0.1:1234"
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("delete %s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestClientPolicyLimitsAreTypedAndLogged(t *testing.T) {
	t.Setenv("TPROXY_LIMITED_KEY", "limited-client-key")
	cfg := &config.Config{ClientAPIKeys: []config.ClientAPIKey{{ID: "limited", Name: "Limited", KeyEnv: "TPROXY_LIMITED_KEY", Policy: config.ClientKeyPolicy{Endpoints: []string{"/v1/models", "/v1/responses"}, Limits: config.LimitPolicy{RequestsPerMinute: 2, MaxInputBytes: 128, MaxOutputTokens: 10}}}}}
	dataStore := apiTestStore(t, cfg)
	handler := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler()

	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.RemoteAddr = "203.0.113.10:1234"
		r.Header.Set("Authorization", "Bearer limited-client-key")
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, r)
		return recorder
	}
	if response := request(http.MethodGet, "/v1/models", ""); response.Code != http.StatusOK {
		t.Fatalf("first request status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPost, "/v1/responses", `{"model":"anything","max_output_tokens":11,"input":"hello"}`); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "max_output_tokens_exceeded") {
		t.Fatalf("max output status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, "/v1/models", ""); response.Code != http.StatusTooManyRequests || !strings.Contains(response.Body.String(), "rate_limit_exceeded") {
		t.Fatalf("rate status=%d body=%s", response.Code, response.Body.String())
	}

	logs := httptest.NewRequest(http.MethodGet, "/api/admin/logs?limit=10", nil)
	logs.RemoteAddr = "127.0.0.1:1234"
	logsRecorder := httptest.NewRecorder()
	handler.ServeHTTP(logsRecorder, logs)
	if logsRecorder.Code != http.StatusOK || !strings.Contains(logsRecorder.Body.String(), `"client_api_key_id":"limited"`) || !strings.Contains(logsRecorder.Body.String(), "rate_limit_exceeded") {
		t.Fatalf("logs status=%d body=%s", logsRecorder.Code, logsRecorder.Body.String())
	}

	writeAdmin := httptest.NewRequest(http.MethodPost, "/api/admin/reload", nil)
	writeAdmin.RemoteAddr = "127.0.0.1:1234"
	writeRecorder := httptest.NewRecorder()
	handler.ServeHTTP(writeRecorder, writeAdmin)
	audit := httptest.NewRequest(http.MethodGet, "/api/admin/audit?limit=10", nil)
	audit.RemoteAddr = "127.0.0.1:1234"
	auditRecorder := httptest.NewRecorder()
	handler.ServeHTTP(auditRecorder, audit)
	if auditRecorder.Code != http.StatusOK || !strings.Contains(auditRecorder.Body.String(), "POST /api/admin/reload") {
		t.Fatalf("audit status=%d body=%s", auditRecorder.Code, auditRecorder.Body.String())
	}
}

func TestProviderHealthAndDiscoveryManagementRoutes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("provider catalog request method=%s path=%s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"upstream-a","owned_by":"mock"}]}`))
	}))
	defer upstream.Close()
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "discover-provider", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "discover-credential", AuthType: "none"}}}}}
	dataStore := apiTestStore(t, cfg)
	handler := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler()

	health := httptest.NewRequest(http.MethodPost, "/api/admin/providers/discover-provider/health", nil)
	health.RemoteAddr = "127.0.0.1:1234"
	healthRecorder := httptest.NewRecorder()
	handler.ServeHTTP(healthRecorder, health)
	if healthRecorder.Code != http.StatusOK || !strings.Contains(healthRecorder.Body.String(), `"status":"healthy"`) {
		t.Fatalf("health status=%d body=%s", healthRecorder.Code, healthRecorder.Body.String())
	}
	discovery := httptest.NewRequest(http.MethodGet, "/api/admin/providers/discover-provider/models", nil)
	discovery.RemoteAddr = "127.0.0.1:1234"
	discoveryRecorder := httptest.NewRecorder()
	handler.ServeHTTP(discoveryRecorder, discovery)
	if discoveryRecorder.Code != http.StatusOK || !strings.Contains(discoveryRecorder.Body.String(), `"id":"upstream-a"`) {
		t.Fatalf("discovery status=%d body=%s", discoveryRecorder.Code, discoveryRecorder.Body.String())
	}
}

func TestProviderModelDiscoveryMergesAllCredentials(t *testing.T) {
	t.Setenv("TPROXY_DISCOVER_ACCOUNT_A", "account-a")
	t.Setenv("TPROXY_DISCOVER_ACCOUNT_B", "account-b")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("provider catalog request method=%s path=%s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Header.Get("Authorization") {
		case "Bearer account-a":
			_, _ = w.Write([]byte(`{"data":[{"id":"shared-model","owned_by":"mock"},{"id":"account-a-only","owned_by":"mock"}]}`))
		case "Bearer account-b":
			_, _ = w.Write([]byte(`{"data":[{"id":"shared-model","owned_by":"mock"},{"id":"account-b-only","owned_by":"mock"}]}`))
		default:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"unauthorized"}}`))
		}
	}))
	defer upstream.Close()
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "discover-provider", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{
		{ID: "account-a", AuthType: "api_key", SecretEnv: "TPROXY_DISCOVER_ACCOUNT_A"},
		{ID: "account-b", AuthType: "api_key", SecretEnv: "TPROXY_DISCOVER_ACCOUNT_B"},
	}}}}
	dataStore := apiTestStore(t, cfg)
	handler := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler()

	discovery := httptest.NewRequest(http.MethodGet, "/api/admin/providers/discover-provider/models", nil)
	discovery.RemoteAddr = "127.0.0.1:1234"
	discoveryRecorder := httptest.NewRecorder()
	handler.ServeHTTP(discoveryRecorder, discovery)
	body := discoveryRecorder.Body.String()
	if discoveryRecorder.Code != http.StatusOK {
		t.Fatalf("discovery status=%d body=%s", discoveryRecorder.Code, body)
	}
	for _, id := range []string{"shared-model", "account-a-only", "account-b-only"} {
		if !strings.Contains(body, `"id":"`+id+`"`) {
			t.Fatalf("missing model %s in body=%s", id, body)
		}
	}
	if !strings.Contains(body, `"credential_ids":["account-a","account-b"]`) {
		t.Fatalf("expected shared model credential ids in body=%s", body)
	}
}

func TestCredentialModelDiscoveryRoute(t *testing.T) {
	t.Setenv("TPROXY_DISCOVER_ACCOUNT_A", "account-a")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("provider catalog request method=%s path=%s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer account-a" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"unauthorized"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"account-a-only","owned_by":"mock"}]}`))
	}))
	defer upstream.Close()
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "discover-provider", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{
		{ID: "account-a", AuthType: "api_key", SecretEnv: "TPROXY_DISCOVER_ACCOUNT_A"},
	}}}}
	dataStore := apiTestStore(t, cfg)
	handler := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler()

	discovery := httptest.NewRequest(http.MethodGet, "/api/admin/credentials/account-a/models", nil)
	discovery.RemoteAddr = "127.0.0.1:1234"
	discoveryRecorder := httptest.NewRecorder()
	handler.ServeHTTP(discoveryRecorder, discovery)
	body := discoveryRecorder.Body.String()
	if discoveryRecorder.Code != http.StatusOK {
		t.Fatalf("discovery status=%d body=%s", discoveryRecorder.Code, body)
	}
	if !strings.Contains(body, `"id":"account-a-only"`) || !strings.Contains(body, `"credential_id":"account-a"`) {
		t.Fatalf("unexpected body=%s", body)
	}
}

func TestAdminModelTestProbesUpstreamModel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("upstream path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","model":"probe-model","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{
			ID: "probe-provider", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true,
			Credentials: []config.CredentialConfig{{ID: "probe-account", AuthType: "none"}},
		}},
	}
	dataStore := apiTestStore(t, cfg)
	handler := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler()

	request := httptest.NewRequest(http.MethodPost, "/api/admin/models/test", bytes.NewBufferString(`{"provider_id":"probe-provider","model_id":"probe-model","kind":"llm"}`))
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, body)
	}
	if !strings.Contains(body, `"ok":true`) || !strings.Contains(body, `"model_id":"probe-model"`) {
		t.Fatalf("unexpected body=%s", body)
	}
}

func TestConfigurationExportRedactsSecretsAndInvalidImportKeepsState(t *testing.T) {
	t.Setenv("TPROXY_EXPORT_SECRET", "do-not-export-this-secret")
	cfg := &config.Config{Server: config.ServerConfig{AllowLocalWithoutKey: true}, Providers: []config.ProviderConfig{{ID: "export-provider", Type: "openai-compatible", BaseURL: "http://127.0.0.1:9", Enabled: true, Credentials: []config.CredentialConfig{{ID: "export-credential", AuthType: "api_key", SecretEnv: "TPROXY_EXPORT_SECRET"}}}}, Models: []config.PublicModelConfig{{ID: "export-model", Enabled: true, Routes: []config.RouteTargetConfig{{Provider: "export-provider", UpstreamModel: "upstream"}}}}}
	dataStore := apiTestStore(t, cfg)
	handler := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler()
	exportRequest := httptest.NewRequest(http.MethodGet, "/api/admin/config/export", nil)
	exportRequest.RemoteAddr = "127.0.0.1:1234"
	exportRecorder := httptest.NewRecorder()
	handler.ServeHTTP(exportRecorder, exportRequest)
	if exportRecorder.Code != http.StatusOK || strings.Contains(exportRecorder.Body.String(), "do-not-export-this-secret") || !strings.Contains(exportRecorder.Body.String(), "TPROXY_CREDENTIAL_EXPORT_CREDENTIAL") {
		t.Fatalf("export status=%d body=%s", exportRecorder.Code, exportRecorder.Body.String())
	}
	invalidImport := httptest.NewRequest(http.MethodPost, "/api/admin/config/import", strings.NewReader(`{"database":{"driver":"postgres"}}`))
	invalidImport.RemoteAddr = "127.0.0.1:1234"
	invalidImport.Header.Set("Content-Type", "application/json")
	invalidRecorder := httptest.NewRecorder()
	handler.ServeHTTP(invalidRecorder, invalidImport)
	if invalidRecorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid import status=%d body=%s", invalidRecorder.Code, invalidRecorder.Body.String())
	}
	snapshot := httptest.NewRequest(http.MethodGet, "/api/admin/snapshot", nil)
	snapshot.RemoteAddr = "127.0.0.1:1234"
	snapshotRecorder := httptest.NewRecorder()
	handler.ServeHTTP(snapshotRecorder, snapshot)
	if snapshotRecorder.Code != http.StatusOK || !strings.Contains(snapshotRecorder.Body.String(), "export-model") {
		t.Fatalf("snapshot after invalid import status=%d body=%s", snapshotRecorder.Code, snapshotRecorder.Body.String())
	}
}

func TestGlobalAndTeamLimitScopesAreEnforcedAtomically(t *testing.T) {
	limiter := newRequestLimiter()
	key := &store.APIKey{ID: "team-key", Policy: config.ClientKeyPolicy{Team: "engineering"}}
	scopes := []limitScope{{ID: "global", Limits: config.LimitPolicy{RequestsPerMinute: 2}}, {ID: "team:engineering", Limits: config.LimitPolicy{RequestsPerMinute: 1}}, {ID: "key:team-key", Limits: config.LimitPolicy{RequestsPerMinute: 10}}}
	if err := limiter.admitRequest(key, "/v1/models", scopes...); err != nil {
		t.Fatal(err)
	}
	if err := limiter.admitRequest(key, "/v1/models", scopes...); err == nil || !strings.Contains(err.Error(), "team:engineering") {
		t.Fatalf("expected team limit error, got %v", err)
	}
	if limiter.windows["global"].Count != 1 {
		t.Fatalf("global count was incremented after rejected team request: %+v", limiter.windows)
	}
}

func apiTestStore(t *testing.T, cfg *config.Config) *store.Store {
	t.Helper()
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := store.OpenSQLite(filepath.Join(t.TempDir(), "api.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	if err = dataStore.Seed(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	return dataStore
}
