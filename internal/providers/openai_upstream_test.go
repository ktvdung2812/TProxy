package providers

import (
	"testing"

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
