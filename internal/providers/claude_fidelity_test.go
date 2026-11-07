package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
)

func TestClaudeFidelityParser(t *testing.T) {
	for _, n := range []int{2, 3} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			var wire strings.Builder
			emit := func(v any) { b, _ := json.Marshal(v); fmt.Fprintf(&wire, "data: %s\n\n", b) }
			emit(map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "thinking", "thinking": ""}})
			emit(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "thinking_delta", "thinking": "thought"}})
			emit(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "signature_delta", "signature": "signed"}})
			emit(map[string]any{"type": "content_block_stop", "index": 0})
			emit(map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "redacted_thinking", "data": "opaque"}})
			emit(map[string]any{"type": "content_block_stop", "index": 1})
			for i := 0; i < n; i++ {
				emit(map[string]any{"type": "content_block_start", "index": i + 2, "content_block": map[string]any{"type": "tool_use", "id": fmt.Sprint("id", i), "name": fmt.Sprint("tool", i), "input": map[string]any{}}})
			}
			for _, part := range []string{`{"v":`, `1}`} {
				for i := 0; i < n; i++ {
					emit(map[string]any{"type": "content_block_delta", "index": i + 2, "delta": map[string]any{"type": "input_json_delta", "partial_json": part}})
				}
			}
			for i := 0; i < n; i++ {
				emit(map[string]any{"type": "content_block_stop", "index": i + 2})
			}
			emit(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "max_tokens", "stop_sequence": "END"}})
			args := map[int]string{}
			ids := map[int]string{}
			var signature, data string
			var finish canonical.Event
			for ev := range parseAnthropicStream(context.Background(), io.NopCloser(strings.NewReader(wire.String())), nil, false) {
				switch ev.Type {
				case canonical.EventToolCallDelta:
					idx := ev.ToolCall["index"].(int)
					fn := ev.ToolCall["function"].(map[string]any)
					args[idx] += stringValue(fn["arguments"])
					if id := stringValue(ev.ToolCall["id"]); id != "" {
						ids[idx] = id
					}
				case canonical.EventSignatureDelta:
					signature = ev.Signature
				case canonical.EventContentBlockStart:
					if ev.ContentBlock["type"] == "redacted_thinking" {
						data = stringValue(ev.ContentBlock["data"])
					}
				case canonical.EventMessageEnd:
					finish = ev
				}
			}
			for i := 0; i < n; i++ {
				if ids[i] != fmt.Sprint("id", i) || args[i] != `{"v":1}` {
					t.Fatalf("ids=%v args=%v", ids, args)
				}
			}
			if signature != "signed" || data != "opaque" || finish.StopReason != "max_tokens" || finish.FinishReason != "length" || finish.StopSequence == nil || *finish.StopSequence != "END" {
				t.Fatalf("signature=%q data=%q end=%+v", signature, data, finish)
			}
		})
	}
}

func TestClaudeFidelityNonStream(t *testing.T) {
	blocks := []any{map[string]any{"type": "thinking", "thinking": "hmm", "signature": "sig"}, map[string]any{"type": "redacted_thinking", "data": "opaque"}, map[string]any{"type": "tool_use", "id": "a", "name": "x", "input": map[string]any{"n": float64(1)}}, map[string]any{"type": "tool_use", "id": "b", "name": "y", "input": map[string]any{}}}
	for reason, finish := range map[string]string{"end_turn": "stop", "max_tokens": "length", "stop_sequence": "stop", "tool_use": "tool_calls", "pause_turn": "stop", "refusal": "content_filter"} {
		result := claudeMessageResponse(map[string]any{"content": blocks, "stop_reason": reason, "stop_sequence": "END"})
		if result.StopReason != reason || result.FinishReason != finish || result.StopSequence == nil || *result.StopSequence != "END" || len(result.ToolCalls) != 2 || !reflect.DeepEqual(result.ContentBlocks[0], blocks[0]) || !reflect.DeepEqual(result.ContentBlocks[1], blocks[1]) {
			t.Fatalf("%s: %+v", reason, result)
		}
	}
}

func TestClaudeFidelityStreamStops(t *testing.T) {
	for reason, finish := range map[string]string{"end_turn": "stop", "max_tokens": "length", "stop_sequence": "stop", "tool_use": "tool_calls", "pause_turn": "stop", "refusal": "content_filter"} {
		t.Run(reason, func(t *testing.T) {
			wire := fmt.Sprintf(`data: {"type":"message_delta","delta":{"stop_reason":%q,"stop_sequence":"END"}}`+"\n\n", reason)
			var last canonical.Event
			for event := range parseAnthropicStream(context.Background(), io.NopCloser(strings.NewReader(wire)), nil, false) {
				last = event
			}
			if last.Type != canonical.EventMessageEnd || last.StopReason != reason || last.FinishReason != finish || last.StopSequence == nil || *last.StopSequence != "END" {
				t.Fatalf("%+v", last)
			}
		})
	}
}
