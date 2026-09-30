package providers

import (
	"context"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
)

func TestParseCodexResponsesPassthroughSSEForwardsEvents(t *testing.T) {
	body := strings.NewReader("" +
		"event: response.created\n" +
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n" +
		"event: response.output_text.delta\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n" +
		"event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n" +
		"data: [DONE]\n\n")
	out := make(chan canonical.Event, 8)
	parseCodexResponsesPassthroughSSE(context.Background(), body, out)
	close(out)

	var events []canonical.Event
	for event := range out {
		events = append(events, event)
	}
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3: %#v", len(events), events)
	}
	if events[0].Type != canonical.EventResponsesSSE || events[0].SSEEvent != "response.created" {
		t.Fatalf("first event = %#v", events[0])
	}
	if events[2].SSEEvent != "response.completed" {
		t.Fatalf("last passthrough event = %#v", events[2])
	}
}

func TestParseCodexResponsesPassthroughSSEEmitsUsage(t *testing.T) {
	body := strings.NewReader("" +
		"event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":120,\"output_tokens\":40,\"input_tokens_details\":{\"cached_tokens\":80}}}}\n\n" +
		"data: [DONE]\n\n")
	out := make(chan canonical.Event, 8)
	parseCodexResponsesPassthroughSSE(context.Background(), body, out)
	close(out)

	var events []canonical.Event
	for event := range out {
		events = append(events, event)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2: %#v", len(events), events)
	}
	if events[0].Type != canonical.EventUsage || events[0].Usage == nil {
		t.Fatalf("first event = %#v", events[0])
	}
	if events[0].Usage.InputTokens != 120 || events[0].Usage.OutputTokens != 40 || events[0].Usage.CachedTokens != 80 {
		t.Fatalf("usage = %#v", events[0].Usage)
	}
	if events[1].Type != canonical.EventResponsesSSE {
		t.Fatalf("second event = %#v", events[1])
	}
}

func TestCodexNormalizeReasoningSetsInclude(t *testing.T) {
	body := map[string]any{
		"reasoning": map[string]any{"effort": "medium"},
	}
	codexNormalizeReasoning(body)
	include, ok := body["include"].([]any)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v", body["include"])
	}
}

func TestCodexNormalizeReasoningMapsMaxAndUltra(t *testing.T) {
	maxBody := map[string]any{"reasoning_effort": "max"}
	codexNormalizeReasoning(maxBody)
	reasoning, _ := maxBody["reasoning"].(map[string]any)
	if reasoning["effort"] != "max" {
		t.Fatalf("max effort = %#v", reasoning["effort"])
	}

	ultraBody := map[string]any{"reasoning_effort": "ultra"}
	codexNormalizeReasoning(ultraBody)
	ultraReasoning, _ := ultraBody["reasoning"].(map[string]any)
	if ultraReasoning["effort"] != "low" {
		t.Fatalf("ultra should fall back to low, got %#v", ultraReasoning["effort"])
	}
}

func codexLiteRequest(raw map[string]any) canonical.Request {
	return canonical.Request{
		Source:        canonical.ProtocolResponses,
		UpstreamModel: "gpt-5.6-sol",
		Raw:           raw,
		Metadata: map[string]any{
			"client_headers": map[string]string{"x-openai-internal-codex-responses-lite": "true"},
		},
	}
}

func TestCodexResponsesLiteSetsReasoningContext(t *testing.T) {
	request := codexLiteRequest(map[string]any{
		"model":     "gpt-5.6-sol",
		"input":     "hi",
		"reasoning": map[string]any{"effort": "high"},
	})
	body := codexBody(request)
	applyCodexResponsesLiteContext(body, request)
	reasoning, _ := body["reasoning"].(map[string]any)
	if reasoning["context"] != "all_turns" {
		t.Fatalf("reasoning.context = %#v", reasoning["context"])
	}
	if reasoning["effort"] != "high" {
		t.Fatalf("effort = %#v", reasoning["effort"])
	}
}

func TestCodexNonLiteOmitsReasoningContext(t *testing.T) {
	request := canonical.Request{
		Source:        canonical.ProtocolResponses,
		UpstreamModel: "gpt-5.5",
		Raw: map[string]any{
			"model":     "gpt-5.5",
			"input":     "hi",
			"reasoning": map[string]any{"effort": "high", "context": "all_turns"},
		},
	}
	body := codexBody(request)
	applyCodexResponsesLiteContext(body, request)
	reasoning, _ := body["reasoning"].(map[string]any)
	if _, exists := reasoning["context"]; exists {
		t.Fatalf("non-lite reasoning must omit context: %#v", reasoning)
	}
}

func TestCodexBodyMergesOpenAIReasoningObject(t *testing.T) {
	body := codexBody(canonical.Request{
		Source:        canonical.ProtocolOpenAI,
		UpstreamModel: "gpt-5.5",
		Raw:           map[string]any{"model": "gpt-5.5"},
		Reasoning:     map[string]any{"effort": "high"},
	})
	reasoning, _ := body["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" {
		t.Fatalf("effort = %#v", reasoning)
	}
	if _, leaked := body["effort"]; leaked {
		t.Fatalf("reasoning fields must not leak to top level: %#v", body)
	}
}

func TestCodexBodyPicksUpRawReasoningEffort(t *testing.T) {
	body := codexBody(canonical.Request{
		Source:        canonical.ProtocolOpenAI,
		UpstreamModel: "gpt-5.5",
		Raw:           map[string]any{"model": "gpt-5.5", "reasoning_effort": "xhigh"},
	})
	reasoning, _ := body["reasoning"].(map[string]any)
	if reasoning["effort"] != "xhigh" {
		t.Fatalf("effort = %#v", reasoning)
	}
	if _, leaked := body["reasoning_effort"]; leaked {
		t.Fatalf("reasoning_effort should be consumed: %#v", body)
	}
}

func TestCodexResponsesLiteRecreatesReasoning(t *testing.T) {
	// effort=none deletes reasoning, but the lite contract still requires
	// reasoning.context on the wire.
	request := codexLiteRequest(map[string]any{
		"model":     "gpt-5.6-sol",
		"input":     "hi",
		"reasoning": map[string]any{"effort": "none"},
	})
	body := codexBody(request)
	applyCodexResponsesLiteContext(body, request)
	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok || reasoning["context"] != "all_turns" {
		t.Fatalf("reasoning = %#v", body["reasoning"])
	}
}
