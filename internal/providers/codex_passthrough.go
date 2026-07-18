package providers

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
)

// parseCodexResponsesPassthroughSSE forwards Codex /responses SSE with original event framing.
// Matches 9router same-format passthrough for Codex CLI compatibility.
func parseCodexResponsesPassthroughSSE(ctx context.Context, body io.Reader, out chan<- canonical.Event) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	var (
		currentEvent    string
		hasCompleted    bool
		hasFailed       bool
		hasIncomplete   bool
		emittedTerminal bool
	)
	emit := func(eventName string, data []byte) {
		out <- canonical.Event{
			Type:     canonical.EventResponsesSSE,
			SSEEvent: eventName,
			SSEData:  append([]byte(nil), data...),
		}
	}
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "event:") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(trimmed, "event:"))
			continue
		}
		if !strings.HasPrefix(trimmed, "data:") {
			if trimmed == "" {
				currentEvent = ""
			}
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
		if data == "[DONE]" {
			return
		}
		if data == "" {
			continue
		}
		eventName := currentEvent
		var raw map[string]any
		if json.Unmarshal([]byte(data), &raw) == nil {
			if eventName == "" {
				eventName = stringValue(raw["type"])
			}
			switch eventName {
			case "response.completed":
				hasCompleted = true
				emittedTerminal = true
			case "response.failed":
				hasFailed = true
				emittedTerminal = true
			case "response.incomplete":
				hasIncomplete = true
				emittedTerminal = true
			}
		}
		emit(eventName, []byte(data))
		currentEvent = ""
	}
	if err := scanner.Err(); err != nil {
		out <- canonical.Event{Type: canonical.EventError, Err: &ProviderError{Status: 502, Code: "upstream_stream_error", Err: err}}
		return
	}
	if !emittedTerminal && !hasCompleted && !hasFailed && !hasIncomplete {
		out <- canonical.Event{Type: canonical.EventError, Err: &ProviderError{Status: 502, Code: "codex_stream_missing_response_completed", Message: "Codex stream ended before response.completed"}}
	}
}
