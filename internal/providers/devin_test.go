package providers

import (
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

func TestBuildDevinChatRequestOrphanToolResult(t *testing.T) {
	req := canonical.Request{
		UpstreamModel: "claude-opus-5",
		Messages: []canonical.Message{
			{Role: "user", Content: "hi"},
			// tool result with no matching assistant tool call → downgraded to user
			{Role: "tool", Content: "orphan result", ToolCallID: "call_missing"},
		},
	}
	built := buildDevinChatRequest(req, "devin-session-token$k")
	msgs := built.request.Messages
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2", len(msgs))
	}
	if msgs[1].Role != "user" || msgs[1].ToolCallID != "" {
		t.Fatalf("orphan tool result not downgraded: %+v", msgs[1])
	}
}

func TestBuildDevinChatRequestMatchedToolResult(t *testing.T) {
	req := canonical.Request{
		UpstreamModel: "swe-1-6-fast",
		Messages: []canonical.Message{
			{Role: "user", Content: "run it"},
			{Role: "assistant", ToolCalls: []map[string]any{{
				"id": "call_1", "type": "function",
				"function": map[string]any{"name": "exec", "arguments": `{"cmd":"ls"}`},
			}}},
			{Role: "tool", Content: "ok", ToolCallID: "call_1"},
		},
	}
	built := buildDevinChatRequest(req, "devin-session-token$k")
	msgs := built.request.Messages
	if len(msgs) != 3 {
		t.Fatalf("messages = %d, want 3", len(msgs))
	}
	if msgs[2].Role != "tool" || msgs[2].ToolCallID != "call_1" {
		t.Fatalf("matched tool result: %+v", msgs[2])
	}
	if msgs[1].ToolCalls[0].Name != "exec" {
		t.Fatalf("assistant tool call: %+v", msgs[1].ToolCalls[0])
	}
}

func TestBuildDevinChatRequestModelResolution(t *testing.T) {
	req := canonical.Request{
		UpstreamModel: "claude-opus-5",
		Reasoning:     map[string]any{"effort": "max"},
		Messages:      []canonical.Message{{Role: "user", Content: "hi"}},
	}
	built := buildDevinChatRequest(req, "devin-session-token$k")
	if built.request.ModelUID != "claude-opus-5-max" {
		t.Fatalf("model uid = %q", built.request.ModelUID)
	}
}

func TestDevinAPIKeyNormalization(t *testing.T) {
	key, err := devinAPIKey(store.Credential{Secret: "raw-token"})
	if err != nil {
		t.Fatal(err)
	}
	if key != "devin-session-token$raw-token" {
		t.Fatalf("key = %q", key)
	}
	key, err = devinAPIKey(store.Credential{Secret: "devin-session-token$already"})
	if err != nil {
		t.Fatal(err)
	}
	if key != "devin-session-token$already" {
		t.Fatalf("key = %q", key)
	}
}
