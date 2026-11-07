package claudeopenai_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/translator/claudeopenai"
)

func TestClaudeFidelityRenderer(t *testing.T) {
	for _, n := range []int{2, 3} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			events := make(chan canonical.Event, 64)
			idx := func(i int) *int { return &i }
			events <- canonical.Event{Type: canonical.EventContentBlockStart, BlockIndex: idx(0), ContentBlock: map[string]any{"type": "thinking", "thinking": ""}}
			events <- canonical.Event{Type: canonical.EventReasoningDelta, BlockIndex: idx(0), Reasoning: "thought"}
			events <- canonical.Event{Type: canonical.EventSignatureDelta, BlockIndex: idx(0), Signature: "sig"}
			events <- canonical.Event{Type: canonical.EventContentBlockStop, BlockIndex: idx(0)}
			events <- canonical.Event{Type: canonical.EventContentBlockStart, BlockIndex: idx(1), ContentBlock: map[string]any{"type": "redacted_thinking", "data": "opaque"}}
			events <- canonical.Event{Type: canonical.EventContentBlockStop, BlockIndex: idx(1)}
			for i := 0; i < n; i++ {
				events <- canonical.Event{Type: canonical.EventContentBlockStart, BlockIndex: idx(i + 2), ContentBlock: map[string]any{"type": "tool_use", "id": fmt.Sprint("id", i), "name": fmt.Sprint("tool", i), "input": map[string]any{}}}
			}
			for _, part := range []string{`{"v":`, `1}`} {
				for i := 0; i < n; i++ {
					events <- canonical.Event{Type: canonical.EventToolCallDelta, BlockIndex: idx(i + 2), ToolCall: map[string]any{"function": map[string]any{"arguments": part}}}
				}
			}
			for i := 0; i < n; i++ {
				events <- canonical.Event{Type: canonical.EventContentBlockStop, BlockIndex: idx(i + 2)}
			}
			sequence := "END"
			events <- canonical.Event{Type: canonical.EventMessageEnd, FinishReason: "length", StopReason: "max_tokens", StopSequence: &sequence}
			close(events)
			out := claudeopenai.CollectClaudeStreamEvents(events, "req", "model")
			args := map[int]string{}
			starts := map[int]map[string]any{}
			stops := map[int]bool{}
			var sig string
			var delta map[string]any
			for _, e := range out {
				index := toInt(e["index"])
				switch e["type"] {
				case "content_block_start":
					starts[index], _ = e["content_block"].(map[string]any)
				case "content_block_stop":
					stops[index] = true
				case "content_block_delta":
					d, _ := e["delta"].(map[string]any)
					if d["type"] == "input_json_delta" {
						args[index] += d["partial_json"].(string)
					}
					if d["type"] == "signature_delta" {
						sig = d["signature"].(string)
					}
				case "message_delta":
					delta, _ = e["delta"].(map[string]any)
				}
			}
			if sig != "sig" || starts[1]["data"] != "opaque" || delta["stop_reason"] != "max_tokens" || delta["stop_sequence"] != "END" {
				t.Fatalf("out=%v", out)
			}
			for i := 0; i < n; i++ {
				if starts[i+2]["id"] != fmt.Sprint("id", i) || starts[i+2]["name"] != fmt.Sprint("tool", i) || args[i+2] != `{"v":1}` || !stops[i+2] {
					t.Fatalf("starts=%v args=%v stops=%v", starts, args, stops)
				}
			}
		})
	}
}

func TestClaudeFidelityStopsAndBody(t *testing.T) {
	blocks := []map[string]any{{"type": "thinking", "thinking": "hmm", "signature": "sig"}, {"type": "redacted_thinking", "data": "opaque"}, {"type": "tool_use", "id": "a", "name": "a", "input": map[string]any{}}}
	for reason, openai := range map[string]string{"end_turn": "stop", "max_tokens": "length", "stop_sequence": "stop", "tool_use": "tool_calls", "pause_turn": "stop", "refusal": "content_filter"} {
		t.Run(reason, func(t *testing.T) {
			seq := "END"
			response := &canonical.Response{ContentBlocks: blocks, StopReason: reason, StopSequence: &seq, FinishReason: reason}
			body := claudeopenai.RenderClaudeResponse(response, "req")
			if body["stop_reason"] != reason || *body["stop_sequence"].(*string) != "END" || !reflect.DeepEqual(body["content"], []any{blocks[0], blocks[1], blocks[2]}) {
				t.Fatalf("body=%v", body)
			}
			converted := claudeopenai.RenderOpenAIResponse(response, "req")
			choice := converted["choices"].([]any)[0].(map[string]any)
			if choice["finish_reason"] != openai {
				t.Fatalf("finish=%v", choice)
			}
			events := make(chan canonical.Event, 1)
			events <- canonical.Event{Type: canonical.EventMessageEnd, StopReason: reason, StopSequence: &seq}
			close(events)
			out := claudeopenai.CollectClaudeStreamEvents(events, "req", "m")
			d := out[len(out)-2]["delta"].(map[string]any)
			if d["stop_reason"] != reason || d["stop_sequence"] != "END" {
				t.Fatalf("delta=%v", d)
			}
		})
	}
	for reason, want := range map[string]string{"length": "max_tokens", "stop": "end_turn", "tool_calls": "tool_use", "content_filter": "refusal"} {
		b := claudeopenai.RenderClaudeResponse(&canonical.Response{FinishReason: reason}, "r")
		if b["stop_reason"] != want {
			t.Errorf("%s: %v", reason, b)
		}
	}
}

func TestOpenAIFinishReasonsRemainOpenAI(t *testing.T) {
	for _, reason := range []string{"stop", "length", "tool_calls", "content_filter", "function_call"} {
		body := claudeopenai.RenderOpenAIResponse(&canonical.Response{FinishReason: reason}, "req")
		choice := body["choices"].([]any)[0].(map[string]any)
		if choice["finish_reason"] != reason {
			t.Errorf("%s: %v", reason, choice)
		}
	}
}
