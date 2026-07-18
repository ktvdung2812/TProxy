package providers

import (
	"net/http"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

func TestXAIModelUsesResponsesAPI(t *testing.T) {
	cases := map[string]bool{
		"grok-4.5":           true,
		"grok-4.5-fast":      true,
		"grok-4.3":           false,
		"grok-build-0.1":     false,
		"grok-3":             false,
	}
	for model, want := range cases {
		if got := xaiModelUsesResponsesAPI(model); got != want {
			t.Fatalf("xaiModelUsesResponsesAPI(%q) = %v want %v", model, got, want)
		}
	}
}

func TestXAIUsesResponsesAPIRequiresPublicAPIBaseURL(t *testing.T) {
	provider := store.Provider{BaseURL: "https://cli-chat-proxy.grok.com/v1"}
	if xaiUsesResponsesAPI(provider, "grok-4.5") {
		t.Fatal("grok-cli proxy should not use /responses routing")
	}
	provider.BaseURL = "https://api.x.ai/v1"
	if !xaiUsesResponsesAPI(provider, "grok-4.5") {
		t.Fatal("api.x.ai grok-4.5 should use /responses routing")
	}
}

func TestXAINormalizeResponsesBodyMatchesPiShape(t *testing.T) {
	body := xaiResponsesBody(canonical.Request{
		Source:        canonical.ProtocolResponses,
		UpstreamModel: "grok-4.5",
		SessionID:     "pi-session-123",
		Raw: map[string]any{
			"model":            "grok-4.5",
			"reasoning_effort": "medium",
			"input":            "hello",
		},
	})
	if body["store"] != false {
		t.Fatalf("store = %#v", body["store"])
	}
	if body["prompt_cache_key"] != "pi-session-123" {
		t.Fatalf("prompt_cache_key = %#v", body["prompt_cache_key"])
	}
	reasoning, _ := body["reasoning"].(map[string]any)
	if reasoning["effort"] != "medium" {
		t.Fatalf("reasoning effort = %#v", reasoning)
	}
	if _, exists := reasoning["summary"]; exists {
		t.Fatalf("xAI responses should not send reasoning.summary: %#v", reasoning)
	}
	include, _ := body["include"].([]any)
	if len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v", body["include"])
	}
	if _, exists := body["prompt_cache_retention"]; exists {
		t.Fatalf("prompt_cache_retention should be stripped: %#v", body)
	}
}

func TestApplyXAICompletionsCompat(t *testing.T) {
	body := map[string]any{
		"store":            false,
		"reasoning_effort": "medium",
		"max_tokens":       1024,
	}
	applyXAICompletionsCompat(body)
	if _, exists := body["store"]; exists {
		t.Fatal("store should be removed")
	}
	if _, exists := body["reasoning_effort"]; exists {
		t.Fatal("reasoning_effort should be removed")
	}
	if body["max_completion_tokens"] != 1024 {
		t.Fatalf("max_completion_tokens = %#v", body["max_completion_tokens"])
	}
}

func TestApplyGrokCLIHeaders(t *testing.T) {
	headers := make(http.Header)
	applyGrokCLIHeaders(headers, "https://cli-chat-proxy.grok.com/v1")
	if headers.Get("x-xai-token-auth") != "xai-grok-cli" {
		t.Fatalf("headers = %#v", headers)
	}
	if headers.Get("x-grok-client-identifier") != "grok-cli" {
		t.Fatalf("headers = %#v", headers)
	}

	headers = make(http.Header)
	applyGrokCLIHeaders(headers, "https://api.x.ai/v1")
	if headers.Get("x-xai-token-auth") != "" {
		t.Fatalf("public API should not get grok-cli headers: %#v", headers)
	}
}
