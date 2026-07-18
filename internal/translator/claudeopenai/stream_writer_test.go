package claudeopenai_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/translator/claudeopenai"
)

func TestWriteClaudeStreamTextLifecycle(t *testing.T) {
	events := make(chan canonical.Event, 8)
	go func() {
		defer close(events)
		events <- canonical.Event{Type: canonical.EventTextDelta, Text: "Hello"}
		events <- canonical.Event{Type: canonical.EventTextDelta, Text: " Claude"}
		events <- canonical.Event{Type: canonical.EventMessageEnd, FinishReason: "stop"}
	}()

	payloads := claudeopenai.CollectClaudeStreamEvents(events, "req-claude", "sonnet")
	types := eventTypes(payloads)
	for _, want := range []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	} {
		if !containsType(types, want) {
			t.Fatalf("missing %q in %v", want, types)
		}
	}
	message, _ := payloads[0]["message"].(map[string]any)
	if message["model"] != "sonnet" {
		t.Fatalf("message model = %#v", message["model"])
	}
}

func TestWriteClaudeStreamSanitizesReadToolArgs(t *testing.T) {
	events := make(chan canonical.Event, 8)
	go func() {
		defer close(events)
		events <- canonical.Event{
			Type: canonical.EventToolCallDelta,
			ToolCall: map[string]any{
				"index": 0,
				"id":    "toolu_read",
				"type":  "function",
				"function": map[string]any{
					"name":      "proxy_Read",
					"arguments": `{"file_path":"F:/repo/file.js","offset":"-5","limit":"999999999","pages":""}`,
				},
			},
		}
		events <- canonical.Event{Type: canonical.EventMessageEnd, FinishReason: "tool_calls"}
	}()

	payloads := claudeopenai.CollectClaudeStreamEvents(events, "req-read", "sonnet")
	partial := findPartialJSON(payloads)
	if partial == "" {
		t.Fatalf("missing input_json_delta in %v", eventTypes(payloads))
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(partial), &args); err != nil {
		t.Fatal(err)
	}
	if args["file_path"] != "F:/repo/file.js" {
		t.Fatalf("file_path = %#v", args["file_path"])
	}
	if toInt(args["offset"]) != 0 || toInt(args["limit"]) != 2000 {
		t.Fatalf("sanitized args = %#v", args)
	}
	if _, ok := args["pages"]; ok {
		t.Fatalf("pages should be removed: %#v", args)
	}
}

func eventTypes(payloads []map[string]any) []string {
	types := make([]string, 0, len(payloads))
	for _, payload := range payloads {
		if typ, ok := payload["type"].(string); ok {
			types = append(types, typ)
		}
	}
	return types
}

func containsType(types []string, want string) bool {
	return strings.Contains(strings.Join(types, ","), want)
}

func findPartialJSON(payloads []map[string]any) string {
	for _, payload := range payloads {
		if payload["type"] != "content_block_delta" {
			continue
		}
		delta, _ := payload["delta"].(map[string]any)
		if delta["type"] == "input_json_delta" {
			if partial, ok := delta["partial_json"].(string); ok {
				return partial
			}
		}
	}
	return ""
}

func toInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case float64:
		return int(v)
	default:
		return 0
	}
}
