package providers

import (
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
)

func TestTranslateCodexToolCallLifecycle(t *testing.T) {
	state := newCodexStreamState(nil)

	added := translateCodexEvent(map[string]any{
		"type": "response.output_item.added",
		"item": map[string]any{
			"type":    "function_call",
			"call_id": "call_123",
			"name":    "read_file",
		},
	}, state)
	if len(added) != 1 || added[0].Type != canonical.EventToolCallDelta {
		t.Fatalf("added = %#v", added)
	}
	fn, _ := added[0].ToolCall["function"].(map[string]any)
	if added[0].ToolCall["index"] != 0 || fn["name"] != "read_file" {
		t.Fatalf("tool call = %#v", added[0].ToolCall)
	}

	args := translateCodexEvent(map[string]any{
		"type":  "response.function_call_arguments.delta",
		"delta": `{"path":"main.go"}`,
	}, state)
	if len(args) != 1 {
		t.Fatalf("args = %#v", args)
	}
	argFn, _ := args[0].ToolCall["function"].(map[string]any)
	if argFn["arguments"] != `{"path":"main.go"}` {
		t.Fatalf("arguments = %#v", argFn)
	}

	done := translateCodexEvent(map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
		},
	}, state)
	if len(done) != 2 {
		t.Fatalf("done events = %#v", done)
	}
	if done[0].Type != canonical.EventUsage {
		t.Fatalf("usage event = %#v", done[0])
	}
	if done[1].Type != canonical.EventMessageEnd || done[1].FinishReason != "tool_calls" {
		t.Fatalf("finish event = %#v", done[1])
	}
}

func TestTranslateCodexResponseCreatedDoesNotEmitChunk(t *testing.T) {
	state := newCodexStreamState(nil)
	events := translateCodexEvent(map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id":         "resp_1",
			"model":      "gpt-5.6-luna",
			"created_at": 123,
		},
	}, state)
	if len(events) != 0 {
		t.Fatalf("events = %#v", events)
	}
	if state.responseID != "resp_1" || state.model != "gpt-5.6-luna" {
		t.Fatalf("state = %+v", state)
	}
}

func TestBuildCodexToolNameMapsShortensLongNames(t *testing.T) {
	longName := "mcp__filesystem__read_file_with_a_very_long_suffix_that_exceeds_limits"
	shortMap, reverseMap := buildCodexToolNameMaps([]map[string]any{{
		"function": map[string]any{"name": longName},
	}}, nil)
	shortName := shortMap[longName]
	if shortName == "" || len(shortName) > codexToolNameMaxLen {
		t.Fatalf("shortName = %q", shortName)
	}
	if reverseMap[shortName] != longName {
		t.Fatalf("reverse = %#v", reverseMap)
	}
}

func TestSanitizeCodexToolNameReplacesInvalidCharacters(t *testing.T) {
	got := sanitizeCodexToolName("mcp__plugin.context7/query-docs")
	want := "mcp__plugin_context7_query-docs"
	if got != want {
		t.Fatalf("sanitize = %q want %q", got, want)
	}
}

func TestBuildCodexToolNameMapsSanitizesInvalidCharacters(t *testing.T) {
	invalidName := "plugin.context7/query-docs"
	shortMap, _ := buildCodexToolNameMaps([]map[string]any{{
		"function": map[string]any{"name": invalidName},
	}}, nil)
	shortName := shortMap[invalidName]
	if shortName == "" || codexToolNameSanitizer.MatchString(shortName) {
		t.Fatalf("shortName = %q", shortName)
	}
}

func TestTranslateCodexResponseFailedAtCapacity(t *testing.T) {
	state := newCodexStreamState(nil)
	events := translateCodexEvent(map[string]any{
		"type": "response.failed",
		"response": map[string]any{
			"error": map[string]any{
				"code":    "model_at_capacity",
				"message": "Selected model is at capacity. Please try a different model.",
			},
		},
	}, state)
	if len(events) != 1 || events[0].Type != canonical.EventError {
		t.Fatalf("events = %#v", events)
	}
	providerErr, ok := events[0].Err.(*ProviderError)
	if !ok {
		t.Fatalf("err = %#v", events[0].Err)
	}
	if providerErr.Status != 429 || providerErr.Code != CodeUpstreamModelAtCapacity {
		t.Fatalf("capacity error = %d %q, want 429 %q", providerErr.Status, providerErr.Code, CodeUpstreamModelAtCapacity)
	}
	if providerErr.Message != "Selected model is at capacity. Please try a different model." {
		t.Fatalf("message = %q", providerErr.Message)
	}
}

func TestTranslateCodexResponseFailedGenericStaysBadGateway(t *testing.T) {
	state := newCodexStreamState(nil)
	events := translateCodexEvent(map[string]any{
		"type":    "response.failed",
		"message": "some other failure",
	}, state)
	if len(events) != 1 || events[0].Type != canonical.EventError {
		t.Fatalf("events = %#v", events)
	}
	providerErr, ok := events[0].Err.(*ProviderError)
	if !ok {
		t.Fatalf("err = %#v", events[0].Err)
	}
	if providerErr.Status != 502 || providerErr.Code != "upstream_response_failed" {
		t.Fatalf("generic failure = %d %q, want 502 upstream_response_failed", providerErr.Status, providerErr.Code)
	}
}

func TestBuildUpstreamBodyErrorUpgradesCapacityBadRequest(t *testing.T) {
	err := upstreamBodyErrorForProvider(400, []byte(`{"error":{"code":"model_at_capacity","message":"Selected model is at capacity. Please try a different model."}}`), "", "")
	providerErr, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("err = %#v", err)
	}
	if providerErr.Status != 429 || providerErr.Code != CodeUpstreamModelAtCapacity {
		t.Fatalf("capacity 400 = %d %q, want 429 %q", providerErr.Status, providerErr.Code, CodeUpstreamModelAtCapacity)
	}
}

func TestBuildUpstreamBodyErrorKeepsOtherBadRequests(t *testing.T) {
	err := upstreamBodyErrorForProvider(400, []byte(`{"error":{"message":"invalid request"}}`), "", "")
	providerErr, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("err = %#v", err)
	}
	if providerErr.Status != 400 {
		t.Fatalf("plain 400 became %d", providerErr.Status)
	}
}

func TestModelAtCapacitySSE(t *testing.T) {
	message, ok := ModelAtCapacitySSE([]byte(`{"type":"response.failed","response":{"status":"failed","error":{"code":"model_at_capacity","message":"Selected model is at capacity. Please try a different model."}}}`))
	if !ok || message == "" {
		t.Fatalf("capacity payload not recognised: message=%q ok=%v", message, ok)
	}
	if _, ok := ModelAtCapacitySSE([]byte(`{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"boom"}}}`)); ok {
		t.Fatal("non-capacity payload recognised as capacity")
	}
	if _, ok := ModelAtCapacitySSE([]byte(`not json`)); ok {
		t.Fatal("invalid JSON recognised as capacity")
	}
}
