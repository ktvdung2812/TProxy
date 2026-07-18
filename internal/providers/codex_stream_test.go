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
	}})
	shortName := shortMap[longName]
	if shortName == "" || len(shortName) > codexToolNameMaxLen {
		t.Fatalf("shortName = %q", shortName)
	}
	if reverseMap[shortName] != longName {
		t.Fatalf("reverse = %#v", reverseMap)
	}
}
