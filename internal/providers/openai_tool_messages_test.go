package providers

import (
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

func sanitizedMessages(t *testing.T, messages any) []map[string]any {
	t.Helper()
	body := sanitizeOpenAIUpstreamBody(
		store.Provider{ID: "consolego", Type: "openai-compatible"},
		map[string]any{"model": "gpt-terra", "messages": messages},
		false,
	)
	items, ok := body["messages"].([]any)
	if !ok {
		t.Fatalf("messages = %#v", body["messages"])
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, asMap(item))
	}
	return result
}

func toolCallMessage(ids ...string) canonical.Message {
	calls := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		calls = append(calls, map[string]any{
			"id":       id,
			"type":     "function",
			"function": map[string]any{"name": "shell", "arguments": "{}"},
		})
	}
	return canonical.Message{Role: "assistant", ToolCalls: calls}
}

// Histories from /responses clients can arrive with parallel calls split across
// assistant messages; upstreams reject an assistant tool_calls message that is
// followed by anything other than its tool results.
func TestSanitizeMergesSplitAssistantToolCalls(t *testing.T) {
	messages := sanitizedMessages(t, []canonical.Message{
		{Role: "user", Content: "run"},
		toolCallMessage("call_1"),
		toolCallMessage("call_2"),
		{Role: "tool", ToolCallID: "call_1", Content: "ok1"},
		{Role: "tool", ToolCallID: "call_2", Content: "ok2"},
		{Role: "user", Content: "hi"},
	})
	if len(messages) != 5 {
		t.Fatalf("messages = %#v", messages)
	}
	calls := openAIToolCalls(messages[1])
	if stringValue(messages[1]["role"]) != "assistant" || len(calls) != 2 {
		t.Fatalf("assistant message = %#v", messages[1])
	}
	if id := stringValue(asMap(calls[0])["id"]); id != "call_1" {
		t.Fatalf("first tool call id = %q", id)
	}
	if id := stringValue(asMap(calls[1])["id"]); id != "call_2" {
		t.Fatalf("second tool call id = %q", id)
	}
	if stringValue(messages[2]["tool_call_id"]) != "call_1" || stringValue(messages[3]["tool_call_id"]) != "call_2" {
		t.Fatalf("tool results = %#v", messages[2:4])
	}
}

// An aborted turn leaves a call with no output; the upstream needs a stand-in.
func TestSanitizeFillsMissingToolResults(t *testing.T) {
	messages := sanitizedMessages(t, []canonical.Message{
		{Role: "user", Content: "run"},
		toolCallMessage("call_1", "call_2"),
		{Role: "tool", ToolCallID: "call_1", Content: "ok1"},
		{Role: "user", Content: "hi"},
	})
	if len(messages) != 5 {
		t.Fatalf("messages = %#v", messages)
	}
	if stringValue(messages[3]["role"]) != "tool" || stringValue(messages[3]["tool_call_id"]) != "call_2" {
		t.Fatalf("placeholder = %#v", messages[3])
	}
	if stringValue(messages[3]["content"]) != missingToolResultText {
		t.Fatalf("placeholder content = %#v", messages[3]["content"])
	}
	if stringValue(messages[4]["role"]) != "user" {
		t.Fatalf("trailing message = %#v", messages[4])
	}
}

func TestSanitizeFillsMissingToolResultAtEndOfHistory(t *testing.T) {
	messages := sanitizedMessages(t, []canonical.Message{
		{Role: "user", Content: "run"},
		toolCallMessage("call_1"),
	})
	if len(messages) != 3 {
		t.Fatalf("messages = %#v", messages)
	}
	if stringValue(messages[2]["role"]) != "tool" || stringValue(messages[2]["tool_call_id"]) != "call_1" {
		t.Fatalf("placeholder = %#v", messages[2])
	}
}

// A tool result answering no open call is rejected by upstreams just like a
// missing one, so it must not be forwarded.
func TestSanitizeDropsOrphanToolMessages(t *testing.T) {
	messages := sanitizedMessages(t, []any{
		map[string]any{"role": "user", "content": "run"},
		map[string]any{"role": "tool", "tool_call_id": "call_stale", "content": "stale"},
		map[string]any{"role": "user", "content": "hi"},
	})
	if len(messages) != 2 {
		t.Fatalf("messages = %#v", messages)
	}
	for _, msg := range messages {
		if stringValue(msg["role"]) == "tool" {
			t.Fatalf("orphan tool message kept: %#v", messages)
		}
	}
}

func TestSanitizeDropsDuplicateToolResults(t *testing.T) {
	messages := sanitizedMessages(t, []any{
		map[string]any{"role": "user", "content": "run"},
		map[string]any{"role": "assistant", "tool_calls": []any{
			map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "shell", "arguments": "{}"}},
		}},
		map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "ok"},
		map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "ok again"},
	})
	if len(messages) != 3 {
		t.Fatalf("messages = %#v", messages)
	}
	if stringValue(messages[2]["content"]) != "ok" {
		t.Fatalf("kept tool result = %#v", messages[2])
	}
}

// Retries reuse the same message maps, so repairing must not mutate them.
func TestSanitizeIsStableAcrossRetries(t *testing.T) {
	source := []any{
		map[string]any{"role": "user", "content": "run"},
		map[string]any{"role": "assistant", "tool_calls": []any{
			map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "shell", "arguments": "{}"}},
		}},
		map[string]any{"role": "assistant", "tool_calls": []any{
			map[string]any{"id": "call_2", "type": "function", "function": map[string]any{"name": "shell", "arguments": "{}"}},
		}},
		map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "ok1"},
		map[string]any{"role": "tool", "tool_call_id": "call_2", "content": "ok2"},
	}
	first := sanitizedMessages(t, source)
	second := sanitizedMessages(t, source)
	if len(first) != len(second) {
		t.Fatalf("first = %d second = %d messages", len(first), len(second))
	}
	if calls := openAIToolCalls(second[1]); len(calls) != 2 {
		t.Fatalf("second pass tool calls = %#v", calls)
	}
}

// /responses keeps the system prompt in "instructions", which chat completions
// providers only see if it is turned into a system message.
func TestBuildOpenAIBodyKeepsResponsesInstructions(t *testing.T) {
	body := buildOpenAIBody(canonical.Request{
		Source:        canonical.ProtocolResponses,
		UpstreamModel: "gpt-terra",
		System:        "You are Codex",
		Messages:      []canonical.Message{{Role: "user", Content: "hi"}},
	})
	messages, ok := body["messages"].([]canonical.Message)
	if !ok || len(messages) != 2 {
		t.Fatalf("messages = %#v", body["messages"])
	}
	if messages[0].Role != "system" || stringValue(messages[0].Content) != "You are Codex" {
		t.Fatalf("system message = %#v", messages[0])
	}
}

func TestSanitizeDropsEmptyToolCallsArray(t *testing.T) {
	messages := sanitizedMessages(t, []any{
		map[string]any{"role": "user", "content": "hi"},
		map[string]any{"role": "assistant", "content": "hello", "tool_calls": []any{}},
	})
	if len(messages) != 2 {
		t.Fatalf("messages = %#v", messages)
	}
	if _, declared := messages[1]["tool_calls"]; declared {
		t.Fatalf("empty tool_calls should be dropped: %#v", messages[1])
	}
}
