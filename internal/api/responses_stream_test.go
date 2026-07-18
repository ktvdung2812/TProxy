package api

import (
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
)

func TestResponsesStreamWriterTextLifecycle(t *testing.T) {
	writer := newResponsesStreamWriter("resp_test", "gpt-5.6-terra")
	var types []string
	collect := func(event canonical.Event) {
		payloads, _ := writer.handle(event)
		for _, payload := range payloads {
			types = append(types, stringValue(payload["type"]))
		}
	}

	collect(canonical.Event{Type: canonical.EventTextDelta, Text: "Hello"})
	collect(canonical.Event{Type: canonical.EventTextDelta, Text: " world"})
	collect(canonical.Event{Type: canonical.EventMessageEnd})

	wantPrefix := []string{
		"response.created",
		"response.in_progress",
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
		"response.output_text.delta",
		"response.output_text.done",
		"response.content_part.done",
		"response.output_item.done",
		"response.completed",
	}
	if len(types) < len(wantPrefix) {
		t.Fatalf("events = %v", types)
	}
	for i, want := range wantPrefix {
		if types[i] != want {
			t.Fatalf("event[%d] = %q want %q\nall=%v", i, types[i], want, types)
		}
	}
}

func TestResponsesStreamWriterReasoningLifecycle(t *testing.T) {
	writer := newResponsesStreamWriter("resp_reason", "gpt-5.6-terra")
	var types []string
	collect := func(event canonical.Event) {
		payloads, _ := writer.handle(event)
		for _, payload := range payloads {
			types = append(types, stringValue(payload["type"]))
		}
	}

	collect(canonical.Event{Type: canonical.EventReasoningDelta, Reasoning: "thinking"})
	collect(canonical.Event{Type: canonical.EventTextDelta, Text: "answer"})
	collect(canonical.Event{Type: canonical.EventMessageEnd})

	joined := strings.Join(types, ",")
	for _, want := range []string{
		"response.output_item.added",
		"response.reasoning_summary_part.added",
		"response.reasoning_summary_text.delta",
		"response.reasoning_summary_text.done",
		"response.content_part.added",
		"response.output_text.delta",
		"response.completed",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %v", want, types)
		}
	}
}

func TestResponsesStreamWriterReasoningEncryptedContent(t *testing.T) {
	writer := newResponsesStreamWriter("resp_enc", "gpt-5.6-terra")
	var doneItem map[string]any
	collect := func(event canonical.Event) {
		payloads, _ := writer.handle(event)
		for _, payload := range payloads {
			if stringValue(payload["type"]) != "response.output_item.done" {
				continue
			}
			item, _ := payload["item"].(map[string]any)
			if stringValue(item["type"]) == "reasoning" {
				doneItem = item
			}
		}
	}

	collect(canonical.Event{
		Type:               canonical.EventReasoningDelta,
		ReasoningItemID:    "rs_upstream_1",
		ReasoningEncrypted: "opaque-signature",
	})
	collect(canonical.Event{Type: canonical.EventMessageEnd})

	if doneItem == nil {
		t.Fatal("missing reasoning output_item.done")
	}
	if doneItem["id"] != "rs_upstream_1" {
		t.Fatalf("reasoning id = %#v", doneItem["id"])
	}
	if doneItem["encrypted_content"] != "opaque-signature" {
		t.Fatalf("encrypted_content = %#v", doneItem["encrypted_content"])
	}
}

func TestResponsesStreamWriterToolLifecycle(t *testing.T) {
	writer := newResponsesStreamWriter("resp_tool", "gpt-5.6-terra")
	var types []string
	collect := func(event canonical.Event) {
		payloads, _ := writer.handle(event)
		for _, payload := range payloads {
			types = append(types, stringValue(payload["type"]))
		}
	}

	collect(canonical.Event{
		Type: canonical.EventToolCallDelta,
		ToolCall: map[string]any{
			"index": 0,
			"id":    "call_1",
			"type":  "function",
			"function": map[string]any{
				"name":      "read_file",
				"arguments": `{"path":"main.go"}`,
			},
		},
	})
	collect(canonical.Event{Type: canonical.EventMessageEnd, FinishReason: "tool_calls"})

	joined := strings.Join(types, ",")
	for _, want := range []string{
		"response.output_item.added",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.output_item.done",
		"response.completed",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %v", want, types)
		}
	}
}
