package router

import (
	"fmt"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/store"
)

func sseEvent(name, data string) canonical.Event {
	return canonical.Event{Type: canonical.EventResponsesSSE, SSEEvent: name, SSEData: []byte(data)}
}

func sendAll(events ...canonical.Event) <-chan canonical.Event {
	ch := make(chan canonical.Event, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	return ch
}

func drainAll(t *testing.T, ch <-chan canonical.Event) []canonical.Event {
	t.Helper()
	var out []canonical.Event
	for event := range ch {
		out = append(out, event)
	}
	return out
}

func TestReadBootstrapReleasesHandshakeOnOverload(t *testing.T) {
	events := sendAll(
		sseEvent("response.created", `{"type":"response.created"}`),
		sseEvent("response.failed", `{"type":"response.failed","response":{"error":{"code":"server_is_overloaded","message":"Selected model is at capacity"}}}`),
	)

	held, err := readBootstrap(events)
	if err == nil {
		t.Fatal("expected a retryable error for the overloaded handshake")
	}
	if len(held) != 0 {
		t.Fatalf("held = %d events, want none on failure", len(held))
	}
	if status := providers.Status(err); status != 429 {
		t.Fatalf("status = %d, want 429", status)
	}
	if code := providers.Code(err); code != providers.CodeUpstreamModelAtCapacity {
		t.Fatalf("code = %q", code)
	}
}

func TestReadBootstrapHandlesRateLimitCodes(t *testing.T) {
	for _, code := range []string{"rate_limit_exceeded", "slow_down", "server_is_overloaded"} {
		events := sendAll(sseEvent("response.failed", fmt.Sprintf(`{"type":"response.failed","error":{"code":%q,"message":"try again later"}}`, code)))
		if _, err := readBootstrap(events); err == nil {
			t.Fatalf("code %q should trigger a retry", code)
		}
	}
}

func TestReadBootstrapIgnoresNonOverloadFailure(t *testing.T) {
	// A context-length error is the client's problem: the attempt must proceed so
	// the caller sees the real reason instead of a silent account switch.
	events := sendAll(sseEvent("response.failed", `{"type":"response.failed","error":{"code":"context_length_exceeded","message":"too long"}}`))

	held, err := readBootstrap(events)
	if err != nil {
		t.Fatalf("unexpected retry: %v", err)
	}
	if len(held) != 1 {
		t.Fatalf("held = %d, want the failure frame released", len(held))
	}
}

func TestReadBootstrapStopsAtContent(t *testing.T) {
	events := sendAll(
		sseEvent("response.created", `{"type":"response.created"}`),
		sseEvent("response.output_text.delta", `{"type":"response.output_text.delta","delta":"hi"}`),
		sseEvent("response.completed", `{"type":"response.completed"}`),
	)

	held, err := readBootstrap(events)
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 2 {
		t.Fatalf("held = %d, want the handshake plus the first content event", len(held))
	}
	if held[1].SSEEvent != "response.output_text.delta" {
		t.Fatalf("last held event = %q, want the text delta", held[1].SSEEvent)
	}
}

func TestReadBootstrapHoldsEmptyOutputItem(t *testing.T) {
	events := sendAll(
		sseEvent("response.output_item.added", `{"type":"response.output_item.added","item":{}}`),
		sseEvent("response.output_text.delta", `{"type":"response.output_text.delta","delta":"hi"}`),
	)

	held, err := readBootstrap(events)
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 2 || held[0].SSEEvent != "response.output_item.added" || held[1].SSEEvent != "response.output_text.delta" {
		t.Fatalf("held = %#v", held)
	}
}

func TestReadBootstrapReleasesAfterEventBudget(t *testing.T) {
	events := make([]canonical.Event, 0, bootstrapMaxEvents+5)
	for index := 0; index < bootstrapMaxEvents+5; index++ {
		events = append(events, sseEvent("response.in_progress", `{"type":"response.in_progress"}`))
	}

	held, err := readBootstrap(sendAll(events...))
	if err != nil {
		t.Fatalf("budget exhaustion must not fail over: %v", err)
	}
	if len(held) != bootstrapMaxEvents {
		t.Fatalf("held = %d, want the whole budget (%d)", len(held), bootstrapMaxEvents)
	}
}

func TestReadBootstrapReleasesAfterByteBudget(t *testing.T) {
	// One oversized handshake frame exhausts the byte budget immediately.
	padding := make([]byte, bootstrapMaxBytes)
	for index := range padding {
		padding[index] = 'x'
	}
	events := sendAll(
		sseEvent("response.in_progress", fmt.Sprintf(`{"type":"response.in_progress","pad":%q}`, string(padding))),
		sseEvent("response.failed", `{"type":"response.failed","response":{"error":{"code":"server_is_overloaded"}}}`),
	)

	held, err := readBootstrap(events)
	if err != nil {
		t.Fatalf("byte budget exhaustion must not fail over: %v", err)
	}
	if len(held) != 1 {
		t.Fatalf("held = %d, want the oversized frame released", len(held))
	}
}

func TestReadBootstrapPropagatesAdapterError(t *testing.T) {
	upstreamErr := &providers.ProviderError{Status: 503, Code: "upstream_unavailable"}
	events := sendAll(canonical.Event{Type: canonical.EventError, Err: upstreamErr})

	if _, err := readBootstrap(events); err != upstreamErr {
		t.Fatalf("err = %v, want the adapter's error", err)
	}
}

func TestReplayBootstrapEmitsHeldThenRest(t *testing.T) {
	rest := sendAll(canonical.Event{Type: canonical.EventMessageEnd})
	held := []canonical.Event{{Type: canonical.EventMessageStart, ID: "start"}}

	events := drainAll(t, replayBootstrap(held, rest))
	if len(events) != 2 || events[0].ID != "start" || events[1].Type != canonical.EventMessageEnd {
		t.Fatalf("replayed order = %#v", events)
	}
}

func TestReplayBootstrapPassesThroughWhenNothingHeld(t *testing.T) {
	rest := sendAll(canonical.Event{Type: canonical.EventMessageEnd})
	if events := replayBootstrap(nil, rest); len(drainAll(t, events)) != 1 {
		t.Fatal("expected the original stream")
	}
}

func TestBootstrapBufferingOnlyAppliesToResponses(t *testing.T) {
	r := New(nil, nil)
	r.ConfigureUpstream(config.UpstreamConfig{Codex: config.CodexUpstreamConfig{StreamBootstrapBuffering: true}})
	provider := store.Provider{Type: "codex"}
	if !r.bootstrapBufferingEnabled(provider, canonical.Request{Source: canonical.ProtocolResponses}) {
		t.Fatal("Responses SSE should be buffered when enabled")
	}
	if r.bootstrapBufferingEnabled(provider, canonical.Request{Source: canonical.ProtocolOpenAI}) {
		t.Fatal("Chat Completions should bypass the Responses-only buffer")
	}
}
