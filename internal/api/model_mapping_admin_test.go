package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/router"
	"github.com/tproxy/tproxy/internal/store"
)

func modelMappingTestServer(t *testing.T, upstreamModel string) (http.Handler, *[]string) {
	t.Helper()
	seenModels := &[]string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if model, ok := body["model"].(string); ok {
			*seenModels = append(*seenModels, model)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","model":"` + upstreamModel + `","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	t.Cleanup(upstream.Close)

	cfg := &config.Config{
		Server:        config.ServerConfig{AllowLocalWithoutKey: false, AllowRemoteManagement: false},
		ClientAPIKeys: []config.ClientAPIKey{{ID: "test-client", Name: "Test", KeyEnv: "TPROXY_MODEL_MAPPING_TEST_KEY"}},
		Providers: []config.ProviderConfig{{
			ID: "mock", Type: "openai-compatible", Name: "Mock", BaseURL: upstream.URL, Enabled: true,
			Credentials: []config.CredentialConfig{{ID: "mock-credential", AuthType: "none"}},
		}},
		Models: []config.PublicModelConfig{{
			ID: "deepseek-v4-pro", DisplayName: "DeepSeek V4 Pro", Enabled: true, RewriteResponseModel: true,
			Routes: []config.RouteTargetConfig{{ID: "route", Provider: "mock", UpstreamModel: "upstream-deepseek", Priority: 10}},
		}},
	}
	dataStore := apiTestStore(t, cfg)
	handler := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry())).Handler()
	return handler, seenModels
}

func TestAdminModelMappingRoundTripAndIngress(t *testing.T) {
	t.Setenv("TPROXY_MODEL_MAPPING_TEST_KEY", "client-secret")
	handler, seen := modelMappingTestServer(t, "upstream-deepseek")

	put := httptest.NewRequest(http.MethodPut, "/api/admin/mapping/models", bytes.NewBufferString(`{"overrides":{"gpt-5.6-sol":"deepseek-v4-pro"}}`))
	put.RemoteAddr = "127.0.0.1:1234"
	put.Header.Set("Content-Type", "application/json")
	withDefaultManagementAuth(put)
	putRec := httptest.NewRecorder()
	handler.ServeHTTP(putRec, put)
	if putRec.Code != http.StatusOK {
		t.Fatalf("put status=%d body=%s", putRec.Code, putRec.Body.String())
	}
	if !strings.Contains(putRec.Body.String(), `"gpt-5.6-sol":"deepseek-v4-pro"`) {
		t.Fatalf("expected mapping in response: %s", putRec.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, "/api/admin/mapping/models", nil)
	get.RemoteAddr = "127.0.0.1:1234"
	withDefaultManagementAuth(get)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, get)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getRec.Code, getRec.Body.String())
	}

	resolve := httptest.NewRequest(http.MethodGet, "/api/admin/mapping/models/resolve?model=gpt-5.6-sol", nil)
	resolve.RemoteAddr = "127.0.0.1:1234"
	withDefaultManagementAuth(resolve)
	resolveRec := httptest.NewRecorder()
	handler.ServeHTTP(resolveRec, resolve)
	if resolveRec.Code != http.StatusOK {
		t.Fatalf("resolve status=%d body=%s", resolveRec.Code, resolveRec.Body.String())
	}
	var resolved struct {
		Resolved string `json:"resolved"`
	}
	_ = json.Unmarshal(resolveRec.Body.Bytes(), &resolved)
	if resolved.Resolved != "deepseek-v4-pro" {
		t.Fatalf("resolve result = %q", resolved.Resolved)
	}

	chat := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"GPT-5.6-Sol","messages":[{"role":"user","content":"hi"}]}`))
	chat.Header.Set("Authorization", "Bearer client-secret")
	chat.Header.Set("Content-Type", "application/json")
	chatRec := httptest.NewRecorder()
	handler.ServeHTTP(chatRec, chat)
	if chatRec.Code != http.StatusOK {
		t.Fatalf("chat status=%d body=%s", chatRec.Code, chatRec.Body.String())
	}
	if !strings.Contains(chatRec.Body.String(), `"model":"deepseek-v4-pro"`) {
		t.Fatalf("expected mapped public model in response: %s", chatRec.Body.String())
	}
	if len(*seen) != 1 || (*seen)[0] != "upstream-deepseek" {
		t.Fatalf("unexpected upstream models: %v", *seen)
	}
}

func TestModelMappingDisabledForKey(t *testing.T) {
	t.Setenv("TPROXY_MODEL_MAPPING_TEST_KEY", "client-secret")
	handler, _ := modelMappingTestServer(t, "upstream-deepseek")

	// Create a second key that opts out of global model mapping.
	createBody := `{"id":"optout-client","name":"OptOut","models":["*"],"policy":{"disable_model_mapping":true}}`
	create := httptest.NewRequest(http.MethodPost, "/api/admin/api-keys", bytes.NewBufferString(createBody))
	create.RemoteAddr = "127.0.0.1:1234"
	create.Header.Set("Content-Type", "application/json")
	withDefaultManagementAuth(create)
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, create)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	var created struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil || created.Key == "" {
		t.Fatalf("create response = %s err=%v", createRec.Body.String(), err)
	}

	put := httptest.NewRequest(http.MethodPut, "/api/admin/mapping/models", bytes.NewBufferString(`{"overrides":{"bypass-me":"deepseek-v4-pro"}}`))
	put.RemoteAddr = "127.0.0.1:1234"
	put.Header.Set("Content-Type", "application/json")
	withDefaultManagementAuth(put)
	putRec := httptest.NewRecorder()
	handler.ServeHTTP(putRec, put)
	if putRec.Code != http.StatusOK {
		t.Fatalf("put status=%d body=%s", putRec.Code, putRec.Body.String())
	}

	// The opted-out key keeps its original model id: the router must not
	// rewrite it to the mapped target.
	blockedChat := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"bypass-me","messages":[{"role":"user","content":"hi"}]}`))
	blockedChat.Header.Set("Authorization", "Bearer "+created.Key)
	blockedChat.Header.Set("Content-Type", "application/json")
	blockedRec := httptest.NewRecorder()
	handler.ServeHTTP(blockedRec, blockedChat)
	if strings.Contains(blockedRec.Body.String(), `"model":"deepseek-v4-pro"`) {
		t.Fatalf("opted-out key unexpectedly got mapped model: %s", blockedRec.Body.String())
	}

	// The default key still gets the rewrite.
	defaultChat := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"bypass-me","messages":[{"role":"user","content":"hi"}]}`))
	defaultChat.Header.Set("Authorization", "Bearer client-secret")
	defaultChat.Header.Set("Content-Type", "application/json")
	defaultRec := httptest.NewRecorder()
	handler.ServeHTTP(defaultRec, defaultChat)
	if defaultRec.Code != http.StatusOK {
		t.Fatalf("default chat status=%d body=%s", defaultRec.Code, defaultRec.Body.String())
	}
	if !strings.Contains(defaultRec.Body.String(), `"model":"deepseek-v4-pro"`) {
		t.Fatalf("expected mapped model for default key: %s", defaultRec.Body.String())
	}
}

func TestModelMappingPersistedAcrossRestart(t *testing.T) {
	t.Setenv("TPROXY_MODEL_MAPPING_TEST_KEY", "client-secret")
	cfg := &config.Config{
		Server:        config.ServerConfig{AllowLocalWithoutKey: false, AllowRemoteManagement: false},
		ClientAPIKeys: []config.ClientAPIKey{{ID: "test-client", Name: "Test", KeyEnv: "TPROXY_MODEL_MAPPING_TEST_KEY"}},
	}
	dataStore := apiTestStore(t, cfg)
	first := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry()))
	first.loadModelMappingResolver()
	first.modelMappingResolver().SetMappings(map[string]string{"gpt-5.6-sol": "deepseek-v4-pro"})
	if err := dataStore.SaveModelMappingSettings(context.Background(), store.ModelMappingSettings{Models: map[string]string{"gpt-5.6-sol": "deepseek-v4-pro"}}); err != nil {
		t.Fatalf("save mapping settings: %v", err)
	}

	second := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry()))
	second.loadModelMappingResolver()
	if got := second.modelMappingResolver().Resolve("GPT-5.6-Sol"); got != "deepseek-v4-pro" {
		t.Fatalf("mapping not reloaded after restart: %q", got)
	}
}
