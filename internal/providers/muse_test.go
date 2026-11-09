package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

func TestMuseUsesResponsesAPI(t *testing.T) {
	for model, want := range map[string]bool{
		"muse-spark-1.3":             true,
		"Muse-Spark-1.2-Contributor": true,
		"muse-wonder-1.0":            true,
		"muse-image-1.0":             false,
		"llama-4-maverick":           false,
	} {
		if got := museUsesResponsesAPI(model); got != want {
			t.Errorf("museUsesResponsesAPI(%q) = %v, want %v", model, got, want)
		}
	}
}

func TestMuseResponsesBodyFromChatRequest(t *testing.T) {
	temperature := 0.4
	body := museResponsesBody(canonical.Request{
		Source:        canonical.ProtocolOpenAI,
		UpstreamModel: "muse-spark-1.3",
		SessionID:     "session-1",
		Messages:      []canonical.Message{{Role: "system", Content: "be brief"}, {Role: "user", Content: "hi"}},
		Tools:         []map[string]any{{"type": "function", "function": map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object"}}}},
		Temperature:   &temperature,
		MaxTokens:     2048,
		Reasoning:     map[string]any{"effort": "high"},
	})
	if body["store"] != false || body["model"] != "muse-spark-1.3" || body["stream"] != true {
		t.Fatalf("base fields = %#v", body)
	}
	if body["temperature"] != 0.4 || body["max_output_tokens"] != 2048 {
		t.Fatalf("sampling fields = temperature %#v, max_output_tokens %#v", body["temperature"], body["max_output_tokens"])
	}
	if reasoning, _ := body["reasoning"].(map[string]any); reasoning["effort"] != "high" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning = %#v", body["reasoning"])
	}
	if include, _ := body["include"].([]any); len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v", body["include"])
	}
	if body["prompt_cache_key"] != "session-1" {
		t.Fatalf("prompt_cache_key = %#v", body["prompt_cache_key"])
	}
	tools, _ := body["tools"].([]map[string]any)
	if len(tools) != 1 || tools[0]["strict"] != false || tools[0]["name"] != "lookup" {
		t.Fatalf("tools = %#v", body["tools"])
	}
	if _, internal := body["_codex_reverse_tool_names"]; !internal {
		t.Fatal("reverse tool-name map must stay for streamResponses to consume")
	}
}

func TestMuseResponsesBodyReasoningEffort(t *testing.T) {
	cases := []struct {
		model, effort, want string
	}{
		{"muse-spark-1.3", "", ""},
		{"muse-spark-1.3", "none", ""},
		{"muse-spark-1.3", "minimal", "minimal"},
		{"muse-spark-1.3", "max", "max"},
		{"muse-wonder-1.0", "max", "max"},
		{"muse-spark-1.2", "max", "xhigh"},
		{"muse-spark-1.3-contributor", "max", "xhigh"},
	}
	for _, tc := range cases {
		request := canonical.Request{Source: canonical.ProtocolOpenAI, UpstreamModel: tc.model, Messages: []canonical.Message{{Role: "user", Content: "hi"}}}
		if tc.effort != "" {
			request.Reasoning = map[string]any{"effort": tc.effort}
		}
		body := museResponsesBody(request)
		reasoning, _ := body["reasoning"].(map[string]any)
		if got := stringValue(reasoning["effort"]); got != tc.want {
			t.Errorf("%s effort %q: reasoning = %#v, want effort %q", tc.model, tc.effort, body["reasoning"], tc.want)
		}
		// Reasoning replay stays on even when Muse picks its own default effort.
		if include, _ := body["include"].([]any); len(include) != 1 {
			t.Errorf("%s effort %q: include = %#v", tc.model, tc.effort, body["include"])
		}
	}
}

func TestMuseResponsesBodyDropsRejectedFields(t *testing.T) {
	body := museResponsesBody(canonical.Request{
		Source:        canonical.ProtocolResponses,
		UpstreamModel: "muse-spark-1.3",
		Raw: map[string]any{
			"model":                "muse-spark-1.3",
			"input":                "hello",
			"frequency_penalty":    0.5,
			"presence_penalty":     0.5,
			"top_logprobs":         3,
			"truncation":           "auto",
			"previous_response_id": "resp_1",
			"include":              []any{"message.output_text.logprobs"},
		},
	})
	for _, key := range []string{"frequency_penalty", "presence_penalty", "top_logprobs", "truncation", "previous_response_id"} {
		if _, exists := body[key]; exists {
			t.Errorf("%s should be dropped: %#v", key, body[key])
		}
	}
	if include, _ := body["include"].([]any); len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v", body["include"])
	}
}

func TestMuseAdapterRoutesSparkToResponses(t *testing.T) {
	var gotPath, gotVersion string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotVersion = r.Header.Get("x-api-version")
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.output_text.delta\n"+
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"+
			"event: response.completed\n"+
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":3,\"output_tokens\":1}}}\n\n")
	}))
	defer server.Close()
	adapter := newMuseAdapter(server.Client())
	provider := store.Provider{ID: "meta", Type: "muse", BaseURL: server.URL + "/v1"}
	credential := store.Credential{ID: "c1", AuthType: "oauth", Secret: "LLM|minted"}
	response, err := adapter.Execute(context.Background(), provider, credential, canonical.Request{
		Source:        canonical.ProtocolOpenAI,
		UpstreamModel: "muse-spark-1.3",
		Messages:      []canonical.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotPath != "/v1/responses" {
		t.Fatalf("path = %q, want /v1/responses", gotPath)
	}
	if gotVersion != "1.0.0" {
		t.Fatalf("x-api-version = %q for a subscription credential", gotVersion)
	}
	if _, internal := gotBody["_codex_reverse_tool_names"]; internal {
		t.Fatal("internal tool-name map leaked upstream")
	}
	if response.Content != "hi" {
		t.Fatalf("content = %#v", response.Content)
	}
}

func TestMuseAdapterKeepsOtherModelsOnChat(t *testing.T) {
	var gotPath, gotVersion string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotVersion = r.Header.Get("x-api-version")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-1",
			"model":   "llama-4-maverick",
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}},
		})
	}))
	defer server.Close()
	adapter := newMuseAdapter(server.Client())
	provider := store.Provider{ID: "meta", Type: "muse", BaseURL: server.URL + "/v1"}
	credential := store.Credential{ID: "c1", AuthType: "api_key", Secret: "LLM|payg"}
	response, err := adapter.Execute(context.Background(), provider, credential, canonical.Request{
		Source:        canonical.ProtocolOpenAI,
		UpstreamModel: "llama-4-maverick",
		Messages:      []canonical.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path = %q, want /v1/chat/completions", gotPath)
	}
	if gotVersion != "" {
		t.Fatalf("x-api-version = %q on a pay-as-you-go key", gotVersion)
	}
	if response.Content != "ok" {
		t.Fatalf("content = %#v", response.Content)
	}
}
