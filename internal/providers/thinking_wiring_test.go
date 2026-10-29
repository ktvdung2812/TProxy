package providers

import (
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/thinking"
)

func TestCodexBodyUsesReasoningObjectNotFlatEffort(t *testing.T) {
	body := codexBody(canonical.Request{
		Source:        canonical.ProtocolResponses,
		UpstreamModel: "gpt-5.6-sol",
		Raw:           map[string]any{"model": "gpt-5.6-sol", "input": "hi", "reasoning": map[string]any{"effort": "high"}},
	})

	reasoning, _ := body["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" {
		t.Fatalf("reasoning.effort = %#v", reasoning["effort"])
	}
	if _, leaked := body["reasoning_effort"]; leaked {
		t.Fatalf("Codex must never receive a flat reasoning_effort: %#v", body)
	}
}

func TestCodexBodyNeverForwardsBudgetTokens(t *testing.T) {
	// A Claude client sending thinking.budget_tokens must not leak a
	// budget_tokens key into the Codex reasoning object.
	body := codexBody(canonical.Request{
		Source:        canonical.ProtocolClaude,
		UpstreamModel: "gpt-5.6-sol",
		Raw: map[string]any{
			"model":    "fable",
			"messages": []any{map[string]any{"role": "user", "content": "hi"}},
			"thinking": map[string]any{"type": "enabled", "budget_tokens": 12000},
		},
	})

	reasoning, _ := body["reasoning"].(map[string]any)
	if _, leaked := reasoning["budget_tokens"]; leaked {
		t.Fatalf("budget_tokens leaked into Codex reasoning: %#v", reasoning)
	}
	if reasoning["effort"] != "high" {
		t.Fatalf("budget 12000 should resolve to high, got %#v", reasoning["effort"])
	}
}

func TestGeminiBodyPreservesHighSuffixModel(t *testing.T) {
	// "-high" is part of the real upstream model ID here, never a reasoning level.
	body := geminiBody(canonical.Request{
		Source:        canonical.ProtocolOpenAI,
		UpstreamModel: "gemini-3.8-flash-high",
		Messages:      []canonical.Message{{Role: "user", Content: "hi"}},
		Raw:           map[string]any{"model": "gemini-3.8-flash-high"},
	})

	if _, exists := body["generationConfig"]; exists {
		t.Fatalf("no thinkingConfig when the client asked for nothing: %#v", body["generationConfig"])
	}
	if model := body["model"]; model != nil && model != "gemini-3.8-flash-high" {
		t.Fatalf("model renamed: %#v", model)
	}
}

func TestEnabledWithoutBudgetStillCountsAsThinking(t *testing.T) {
	// thinking:{type:enabled} or thinking:true carry no effort/budget value, but
	// the choice must reach the adapters that key off "did the client ask".
	for _, raw := range []map[string]any{
		{"thinking": map[string]any{"type": "enabled"}},
		{"thinking": map[string]any{"type": "adaptive"}},
		{"thinking": true},
	} {
		request := thinking.Normalize(canonical.Request{Raw: raw})
		if !kiroThinkingRequested(request) {
			t.Fatalf("kiro dropped an explicit thinking choice: %#v", raw)
		}
		if !requestWantsThinking(request) {
			t.Fatalf("perplexity dropped an explicit thinking choice: %#v", raw)
		}
	}
	disabled := thinking.Normalize(canonical.Request{Raw: map[string]any{"thinking": map[string]any{"type": "disabled"}}})
	if kiroThinkingRequested(disabled) || requestWantsThinking(disabled) {
		t.Fatal("disabled thinking must not count as a thinking request")
	}
}

func TestNormalizedRequestIsNotRewrittenByAdapter(t *testing.T) {
	// Normalize once, then let two adapters build bodies: neither may pick a
	// different effort out of the raw body on a second pass.
	request := thinking.Normalize(canonical.Request{
		Source:        canonical.ProtocolOpenAI,
		UpstreamModel: "gpt-5.6-sol",
		Raw:           map[string]any{"model": "gpt-5.6-sol", "input": "hi", "reasoning_effort": "xhigh"},
	})

	first := codexBody(request)
	second := codexBody(request)
	for name, body := range map[string]map[string]any{"first": first, "second": second} {
		reasoning, _ := body["reasoning"].(map[string]any)
		if reasoning["effort"] != "xhigh" {
			t.Fatalf("%s attempt effort = %#v, want xhigh", name, reasoning["effort"])
		}
	}
}
