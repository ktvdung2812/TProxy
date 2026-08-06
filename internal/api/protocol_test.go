package api

import (
	"encoding/json"
	"testing"
)

func decodeTestBody(t *testing.T, raw string) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

// Codex emits one function_call item per parallel call. Chat completions requires
// them in a single assistant message, otherwise the upstream rejects the request
// with "insufficient tool messages following tool_calls message".
func TestParseResponsesMergesParallelFunctionCalls(t *testing.T) {
	body := decodeTestBody(t, `{
		"model": "gpt-terra",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "run"}]},
			{"type": "reasoning", "id": "rs_1", "summary": []},
			{"type": "function_call", "call_id": "call_1", "name": "shell", "arguments": "{\"a\":1}"},
			{"type": "function_call", "call_id": "call_2", "name": "shell", "arguments": "{\"a\":2}"},
			{"type": "function_call_output", "call_id": "call_1", "output": "ok1"},
			{"type": "function_call_output", "call_id": "call_2", "output": "ok2"},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "hi"}]}
		]
	}`)
	messages := parseResponses(body, "req_1").Messages
	if len(messages) != 5 {
		t.Fatalf("messages = %#v", messages)
	}
	if messages[1].Role != "assistant" || len(messages[1].ToolCalls) != 2 {
		t.Fatalf("assistant message = %#v", messages[1])
	}
	if id := stringValue(messages[1].ToolCalls[0]["id"]); id != "call_1" {
		t.Fatalf("first tool call id = %q", id)
	}
	if id := stringValue(messages[1].ToolCalls[1]["id"]); id != "call_2" {
		t.Fatalf("second tool call id = %q", id)
	}
	if messages[2].Role != "tool" || messages[2].ToolCallID != "call_1" {
		t.Fatalf("first tool result = %#v", messages[2])
	}
	if messages[3].Role != "tool" || messages[3].ToolCallID != "call_2" {
		t.Fatalf("second tool result = %#v", messages[3])
	}
	if messages[4].Role != "user" {
		t.Fatalf("trailing message = %#v", messages[4])
	}
}

// A function_call that follows a tool result belongs to a new assistant turn and
// must not be folded into the previous one.
func TestParseResponsesKeepsSequentialFunctionCallsSeparate(t *testing.T) {
	body := decodeTestBody(t, `{
		"model": "gpt-terra",
		"input": [
			{"type": "function_call", "call_id": "call_1", "name": "shell", "arguments": "{}"},
			{"type": "function_call_output", "call_id": "call_1", "output": "ok1"},
			{"type": "function_call", "call_id": "call_2", "name": "shell", "arguments": "{}"},
			{"type": "function_call_output", "call_id": "call_2", "output": "ok2"}
		]
	}`)
	messages := parseResponses(body, "req_1").Messages
	if len(messages) != 4 {
		t.Fatalf("messages = %#v", messages)
	}
	for _, index := range []int{0, 2} {
		if messages[index].Role != "assistant" || len(messages[index].ToolCalls) != 1 {
			t.Fatalf("messages[%d] = %#v", index, messages[index])
		}
	}
}
