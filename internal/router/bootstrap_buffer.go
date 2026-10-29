package router

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/providers"
)

// Bootstrap buffering limits. A handshake that runs longer than this is not a
// handshake any more, so the buffer releases what it holds and the stream
// proceeds without failover. These are sizes, not timeouts: a model thinking for
// two minutes before its first token must never be cut off.
const (
	bootstrapMaxEvents = 48
	bootstrapMaxBytes  = 1 << 20
)

// streamBootstrap holds the leading events of a Codex Responses stream until it
// knows whether the attempt will produce real output.
//
// Codex can answer HTTP 200, emit response.created/response.in_progress, and only
// then report that the model is at capacity. By that point the response headers
// are committed, so the credential loop can no longer fail over. This gate runs
// before ExecuteStream returns and turns that case into a ProviderError the loop
// can retry, so the client never receives a byte from the failed attempt.
type streamBootstrap struct {
	events <-chan canonical.Event
	held   []canonical.Event
	bytes  int
}

// readBootstrap consumes leading events from events until it either sees real
// output or exhausts the budget.
//
// It returns a non-nil error when the attempt must be retried on another
// credential, and the held events otherwise (which the caller must emit before
// continuing to drain events).
func readBootstrap(events <-chan canonical.Event) ([]canonical.Event, error) {
	buffer := &streamBootstrap{events: events}
	return buffer.read()
}

func drainBootstrap(events <-chan canonical.Event) {
	for range events {
	}
}

func (b *streamBootstrap) read() ([]canonical.Event, error) {
	for event := range b.events {
		if event.Type == canonical.EventError {
			return nil, event.Err
		}
		if err, overloaded := overloadFromBootstrapEvent(event); overloaded {
			return nil, err
		}
		if bootstrapIsContent(event) {
			b.held = append(b.held, event)
			return b.held, nil
		}
		b.held = append(b.held, event)
		b.bytes += bootstrapEventBytes(event)
		if len(b.held) >= bootstrapMaxEvents || b.bytes >= bootstrapMaxBytes {
			// Out of budget: stop holding and let the stream run normally.
			return b.held, nil
		}
	}
	// The adapter closed before producing anything. Releasing the held handshake
	// events lets wrapEvents report the usual incomplete-stream error.
	return b.held, nil
}

// replayBootstrap returns a channel that emits the held events first and then
// whatever the adapter still has to send.
func replayBootstrap(held []canonical.Event, rest <-chan canonical.Event) <-chan canonical.Event {
	if len(held) == 0 {
		return rest
	}
	out := make(chan canonical.Event, len(held)+16)
	go func() {
		defer close(out)
		for _, event := range held {
			out <- event
		}
		for event := range rest {
			out <- event
		}
	}()
	return out
}

// bootstrapIsContent reports whether an event is output the client must see, as
// opposed to handshake chatter that is safe to hold back.
func bootstrapIsContent(event canonical.Event) bool {
	switch event.Type {
	case canonical.EventTextDelta:
		return event.Text != ""
	case canonical.EventReasoningDelta:
		return event.Reasoning != "" || event.ReasoningEncrypted != "" || event.Text != ""
	case canonical.EventToolCallDelta:
		return len(event.ToolCall) > 0
	case canonical.EventImageDelta, canonical.EventAudioDelta:
		return event.Media != nil
	case canonical.EventResponsesSSE:
		return responseSSECarriesOutput(event)
	default:
		return false
	}
}

// responseSSECarriesOutput reports whether a raw Responses SSE frame carries
// model output rather than lifecycle bookkeeping.
func responseSSECarriesOutput(event canonical.Event) bool {
	var payload map[string]any
	if json.Unmarshal(event.SSEData, &payload) != nil {
		return false
	}
	switch event.SSEEvent {
	case "response.output_text.delta", "response.reasoning_summary_text.delta",
		"response.reasoning_text.delta", "response.function_call_arguments.delta":
		// A named delta is content only when it carries a non-empty delta.
		value := firstSSEValue(payload, "delta", "text", "arguments")
		return strings.TrimSpace(fmt.Sprint(value)) != "" && fmt.Sprint(value) != "<nil>"
	case "response.output_item.added", "response.output_item.done":
		return sseObjectHasOutput(payload["item"])
	case "response.content_part.added", "response.content_part.done":
		return sseObjectHasOutput(firstSSEValue(payload, "part", "content_part"))
	}
	delta, _ := payload["delta"].(string)
	return strings.TrimSpace(delta) != "" && strings.HasSuffix(event.SSEEvent, ".delta")
}

func firstSSEValue(payload map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := payload[key]; ok {
			return value
		}
	}
	return nil
}

func sseObjectHasOutput(value any) bool {
	object, _ := value.(map[string]any)
	if len(object) == 0 {
		return false
	}
	for _, key := range []string{"text", "delta", "arguments", "content", "summary", "name", "call_id"} {
		if strings.TrimSpace(fmt.Sprint(object[key])) != "" && fmt.Sprint(object[key]) != "<nil>" {
			return true
		}
	}
	return false
}

// overloadFromBootstrapEvent turns an in-stream overload or rate-limit report
// into the ProviderError the credential loop understands. The status matters: a
// 429 triggers failover and the model-capacity cooldown, which is exactly what a
// buffered handshake should cause.
func overloadFromBootstrapEvent(event canonical.Event) (error, bool) {
	if event.Type != canonical.EventResponsesSSE {
		return nil, false
	}
	// response.failed carrying a capacity error is the shape Codex uses.
	if message, atCapacity := providers.ModelAtCapacitySSE(event.SSEData); atCapacity {
		return &providers.ProviderError{Status: 429, Code: providers.CodeUpstreamModelAtCapacity, Message: message}, true
	}
	if event.SSEEvent != "response.failed" && event.SSEEvent != "error" {
		return nil, false
	}
	var payload map[string]any
	if json.Unmarshal(event.SSEData, &payload) != nil {
		return nil, false
	}
	code, message := sseErrorFields(payload)
	if !isOverloadCode(code, message) {
		return nil, false
	}
	if strings.TrimSpace(message) == "" {
		message = "upstream reported overload before any output"
	}
	if strings.TrimSpace(code) == "" {
		code = "upstream_overloaded"
	}
	return &providers.ProviderError{Status: 429, Code: code, Message: message}, true
}

// sseErrorFields pulls the error code and message out of an SSE payload, looking
// both at the top level and under the nested response object.
func sseErrorFields(payload map[string]any) (code, message string) {
	containers := []map[string]any{payload}
	if nested, ok := payload["response"].(map[string]any); ok {
		containers = append(containers, nested)
	}
	for _, container := range containers {
		if errorPayload, ok := container["error"].(map[string]any); ok {
			if code == "" {
				code = fmt.Sprint(errorPayload["code"])
			}
			if message == "" {
				message = fmt.Sprint(errorPayload["message"])
			}
		}
		if code == "" {
			code = fmt.Sprint(container["code"])
		}
		if message == "" {
			message = fmt.Sprint(container["message"])
		}
	}
	return cleanSSEField(code), cleanSSEField(message)
}

// cleanSSEField drops the "<nil>" that fmt.Sprint produces for a missing key.
func cleanSSEField(value string) string {
	if value == "<nil>" {
		return ""
	}
	return strings.TrimSpace(value)
}

// isOverloadCode recognises the capacity, overload and rate-limit reports that
// are worth another credential.
func isOverloadCode(code, message string) bool {
	lowerCode := strings.ToLower(code)
	lowerMessage := strings.ToLower(message)
	for _, needle := range []string{"at_capacity", "at capacity", "overloaded", "rate_limit", "slow_down", "capacity"} {
		if strings.Contains(lowerCode, needle) || strings.Contains(lowerMessage, needle) {
			return true
		}
	}
	return false
}

func bootstrapEventBytes(event canonical.Event) int {
	size := len(event.Text) + len(event.Reasoning) + len(event.SSEData) + 64
	if len(event.ToolCall) > 0 {
		if encoded, err := json.Marshal(event.ToolCall); err == nil {
			size += len(encoded)
		}
	}
	return size
}
