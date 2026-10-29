package router_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/router"
)

// codexHandshakeThen writes a Responses handshake followed by body as SSE.
func codexHandshakeThen(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
	_, _ = w.Write([]byte("event: response.in_progress\ndata: {\"type\":\"response.in_progress\"}\n\n"))
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}
	_, _ = w.Write([]byte(body))
	if flusher != nil {
		flusher.Flush()
	}
}

const codexAtCapacityFrame = "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_is_overloaded\",\"message\":\"Selected model is at capacity\"}}}\n\n"

const codexTextThenDone = "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
	"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"

// codexBootstrapRouter builds a two-credential Codex route where credential "one"
// answers with handshake+overload and credential "two" answers with text.
func codexBootstrapRouter(t *testing.T, buffering bool) (*router.Router, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	t.Setenv("TPROXY_CODEX_ONE", "codex-one")
	t.Setenv("TPROXY_CODEX_TWO", "codex-two")

	var healthyCalls atomic.Int32
	var overloadCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "codex-one" {
			overloadCalls.Add(1)
			codexHandshakeThen(w, codexAtCapacityFrame)
			return
		}
		healthyCalls.Add(1)
		codexHandshakeThen(w, codexTextThenDone)
	}))
	t.Cleanup(upstream.Close)

	cfg := &config.Config{
		Providers: []config.ProviderConfig{{
			ID: "codex-provider", Type: "codex", Name: "Codex", BaseURL: upstream.URL, Enabled: true,
			Credentials: []config.CredentialConfig{
				{ID: "one", AuthType: "api_key", SecretEnv: "TPROXY_CODEX_ONE", Priority: 100},
				{ID: "two", AuthType: "api_key", SecretEnv: "TPROXY_CODEX_TWO", Priority: 10},
			},
		}},
		Models: []config.PublicModelConfig{{
			ID: "td-codex", Enabled: true, RewriteResponseModel: true,
			Routes: []config.RouteTargetConfig{{ID: "route", Provider: "codex-provider", UpstreamModel: "gpt-5.6-sol", Priority: 10}},
		}},
	}
	dataStore := newStore(t, cfg)
	requestRouter := router.New(dataStore, providers.NewRegistry())
	requestRouter.ConfigureRouting(config.RoutingConfig{Strategy: "round-robin", Cooldown: config.CooldownConfig{Disabled: true}})
	requestRouter.ConfigureUpstream(config.UpstreamConfig{Codex: config.CodexUpstreamConfig{StreamBootstrapBuffering: buffering}})
	return requestRouter, &healthyCalls, &overloadCalls
}

func streamCodex(t *testing.T, requestRouter *router.Router) ([]canonical.Event, error) {
	t.Helper()
	model, err := requestRouter.Resolve(context.Background(), "td-codex", nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := requestRouter.ExecuteStream(context.Background(), *model, canonical.Request{
		Source:        canonical.ProtocolResponses,
		RequestID:     "req-codex-bootstrap",
		SessionID:     "codex-session",
		UpstreamModel: "gpt-5.6-sol",
		Raw:           map[string]any{"model": "gpt-5.6-sol", "input": "hi", "stream": true},
		Stream:        true,
	})
	if err != nil {
		return nil, err
	}
	var events []canonical.Event
	for event := range stream.Events {
		events = append(events, event)
	}
	return events, nil
}

func eventSummary(events []canonical.Event) string {
	parts := make([]string, 0, len(events))
	for _, event := range events {
		if event.Type == canonical.EventResponsesSSE {
			parts = append(parts, "sse:"+event.SSEEvent)
			continue
		}
		parts = append(parts, string(event.Type))
	}
	return strings.Join(parts, ",")
}

func TestCodexBootstrapBufferingFailsOverBeforeFirstByte(t *testing.T) {
	requestRouter, healthyCalls, overloadCalls := codexBootstrapRouter(t, true)

	events, err := streamCodex(t, requestRouter)
	if err != nil {
		t.Fatalf("ExecuteStream = %v", err)
	}
	if overloadCalls.Load() != 1 {
		t.Fatalf("overload credential calls = %d, want 1", overloadCalls.Load())
	}
	if healthyCalls.Load() != 1 {
		t.Fatalf("healthy credential calls = %d, want 1", healthyCalls.Load())
	}
	// The client only ever sees the second attempt.
	for _, event := range events {
		if event.Type == canonical.EventError {
			t.Fatalf("client received an error: %v (events: %s)", event.Err, eventSummary(events))
		}
	}
	if !strings.Contains(eventSummary(events), "response.output_text.delta") {
		t.Fatalf("client never saw the text: %s", eventSummary(events))
	}
}

func TestCodexBootstrapBufferingReleasesHandshake(t *testing.T) {
	requestRouter, _, _ := codexBootstrapRouter(t, true)

	events, err := streamCodex(t, requestRouter)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(eventSummary(events), "response.created") {
		t.Fatalf("held handshake was not released: %s", eventSummary(events))
	}
	if !strings.Contains(eventSummary(events), "response.completed") {
		t.Fatalf("terminal event missing: %s", eventSummary(events))
	}
}

func TestCodexBootstrapBufferingOffPassesOverloadThrough(t *testing.T) {
	requestRouter, healthyCalls, _ := codexBootstrapRouter(t, false)

	events, err := streamCodex(t, requestRouter)
	if err != nil {
		t.Fatalf("ExecuteStream = %v", err)
	}
	if healthyCalls.Load() != 0 {
		t.Fatalf("fallback should not run with buffering off, got %d calls", healthyCalls.Load())
	}
	if !strings.Contains(eventSummary(events), "response.failed") {
		t.Fatalf("expected the raw overload frame, got %s", eventSummary(events))
	}
}
