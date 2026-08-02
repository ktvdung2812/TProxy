package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

func TestNormalizeOpenAIToolChoiceAnthropicShape(t *testing.T) {
	got := normalizeOpenAIToolChoice(map[string]any{"type": "tool", "name": "search"})
	want := map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": "search",
		},
	}
	if normalizeMap(got) != normalizeMap(want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestNormalizeOpenAIToolChoiceFunctionNameOnly(t *testing.T) {
	got := normalizeOpenAIToolChoice(map[string]any{"type": "function", "name": "search"})
	want := map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": "search",
		},
	}
	if normalizeMap(got) != normalizeMap(want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestUnwrapOpenAIChatCompletionClineEnvelope(t *testing.T) {
	raw := map[string]any{
		"success": true,
		"data": map[string]any{
			"id":    "gen_1",
			"model": "moonshotai/kimi-k2.6",
			"choices": []any{
				map[string]any{
					"finish_reason": "stop",
					"message": map[string]any{
						"role":      "assistant",
						"content":   "Hello!",
						"reasoning": "greeting",
					},
				},
			},
			"usage": map[string]any{"prompt_tokens": 8, "completion_tokens": 12},
		},
	}
	got := unwrapOpenAIChatCompletion(raw)
	if stringValue(got["id"]) != "gen_1" {
		t.Fatalf("id = %v", got["id"])
	}
	choices, _ := got["choices"].([]any)
	if len(choices) != 1 {
		t.Fatalf("choices = %#v", got["choices"])
	}
	// Standard OpenAI body is left alone.
	standard := map[string]any{"id": "chatcmpl", "choices": []any{map[string]any{"message": map[string]any{"content": "ok"}}}}
	if unwrapOpenAIChatCompletion(standard)["id"] != "chatcmpl" {
		t.Fatalf("standard unwrap mutated: %#v", unwrapOpenAIChatCompletion(standard))
	}
}

func TestClinePassExecuteUnwrapsSuccessDataEnvelope(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" && r.URL.Path != "/chat/completions" {
			// BaseURL includes /api/v1 so path is /chat/completions
			if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
				t.Fatalf("path = %s", r.URL.Path)
			}
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer workos:") {
			t.Fatalf("auth = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":"gen_1","model":"moonshotai/kimi-k2.6","object":"chat.completion","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"Hello!","reasoning":"think"}}],"usage":{"prompt_tokens":8,"completion_tokens":20,"completion_tokens_details":{"reasoning_tokens":10}}}}`))
	}))
	defer upstream.Close()
	adapter, err := NewRegistry().Adapter("clinepass")
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.Execute(context.Background(), store.Provider{
		ID: "clinepass", Type: "clinepass", BaseURL: upstream.URL + "/api/v1",
	}, store.Credential{AuthType: "oauth", Secret: "workos:token"}, canonical.Request{
		RequestID: "probe", UpstreamModel: "cline-pass/kimi-k2.6",
		Messages: []canonical.Message{{Role: "user", Content: "hi"}}, MaxTokens: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "Hello!" {
		t.Fatalf("content = %#v", response.Content)
	}
	if response.Reasoning != "think" {
		t.Fatalf("reasoning = %q", response.Reasoning)
	}
	if response.Usage.InputTokens != 8 || response.Usage.OutputTokens != 20 || response.Usage.ReasoningTokens != 10 {
		t.Fatalf("usage = %+v", response.Usage)
	}
	if response.ID != "gen_1" {
		t.Fatalf("id = %q", response.ID)
	}
	if !llmPingSucceeded(response) {
		t.Fatalf("ping should succeed for unwrapped response")
	}
}

func TestSanitizeGLMRequestStripsStreamOptionsAndReasoningEffort(t *testing.T) {
	body := sanitizeOpenAIUpstreamBody(store.Provider{
		ID: "glm", Type: "openai-compatible", BaseURL: "https://api.z.ai/api/coding/paas/v4",
	}, map[string]any{
		"model":            "glm-5.2",
		"messages":         []any{map[string]any{"role": "user", "content": "hi"}},
		"stream":           true,
		"stream_options":   map[string]any{"include_usage": true},
		"reasoning_effort": "medium",
	}, true)
	if _, ok := body["stream_options"]; ok {
		t.Fatalf("stream_options should be removed: %#v", body)
	}
	if _, ok := body["reasoning_effort"]; ok {
		t.Fatalf("reasoning_effort should be removed: %#v", body)
	}
	if thinking := asMap(body["thinking"]); thinking == nil || stringValue(thinking["type"]) != "enabled" {
		t.Fatalf("thinking = %#v", body["thinking"])
	}
}

func TestSanitizeGLMDefaultEnablesThinking(t *testing.T) {
	body := sanitizeOpenAIUpstreamBody(store.Provider{
		ID: "glm", Type: "openai-compatible", BaseURL: "https://api.z.ai/api/paas/v4",
	}, map[string]any{
		"model":    "glm-5.2",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}, false)
	if thinking := asMap(body["thinking"]); thinking == nil || stringValue(thinking["type"]) != "enabled" {
		t.Fatalf("thinking = %#v", body["thinking"])
	}
}

func TestSanitizeGLMStripsImageContent(t *testing.T) {
	body := sanitizeOpenAIUpstreamBody(store.Provider{
		ID: "glm", Type: "openai-compatible", BaseURL: "https://api.z.ai/api/paas/v4",
	}, map[string]any{
		"model": "glm-5.2",
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "text", "text": "what is this"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,abc"}},
			},
		}},
	}, false)
	msg := asMap(body["messages"].([]any)[0])
	parts, ok := msg["content"].([]any)
	if !ok || len(parts) != 1 || stringValue(asMap(parts[0])["type"]) != "text" {
		t.Fatalf("image content should be stripped: %#v", msg["content"])
	}
}

func normalizeMap(value any) string {
	mapped := asMap(value)
	if mapped == nil {
		return ""
	}
	return stringValue(mapped["type"]) + ":" + stringValue(asMap(mapped["function"])["name"])
}
