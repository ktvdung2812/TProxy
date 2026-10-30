package thinking

import (
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
)

func TestNormalizeClientBudgetBeatsAliasEffort(t *testing.T) {
	request := canonical.Request{
		Source:        canonical.ProtocolClaude,
		UpstreamModel: "gpt-5.6-sol",
		Raw: map[string]any{
			"model": "fable",
			"thinking": map[string]any{
				"type":          "enabled",
				"budget_tokens": 12000,
			},
		},
		Metadata: map[string]any{MetadataAliasEffort: "low"},
	}

	normalized := Normalize(request)
	if got := normalized.Reasoning["effort"]; got != "high" {
		t.Fatalf("client budget must win over the alias: effort = %#v", got)
	}
	if got := normalized.Reasoning["budget_tokens"]; got != 12000 {
		t.Fatalf("budget_tokens = %#v", got)
	}
}

func TestNormalizeAliasFillsEmptyBody(t *testing.T) {
	request := canonical.Request{
		Source:        canonical.ProtocolClaude,
		UpstreamModel: "gpt-5.6-sol",
		Raw:           map[string]any{"model": "fable"},
		Metadata:      map[string]any{MetadataAliasEffort: "high"},
	}

	normalized := Normalize(request)
	if got := normalized.Reasoning["effort"]; got != "high" {
		t.Fatalf("effort = %#v, want high", got)
	}
	if normalized.Metadata[MetadataNormalized] != true {
		t.Fatal("expected Metadata[thinking_normalized]")
	}
}

func TestNormalizeLeavesRawUntouched(t *testing.T) {
	raw := map[string]any{"model": "fable", "reasoning_effort": "low"}
	normalized := Normalize(canonical.Request{Source: canonical.ProtocolClaude, Raw: raw})
	if raw["reasoning_effort"] != "low" {
		t.Fatalf("Normalize must not rewrite Raw: %#v", raw)
	}
	if _, exists := raw["reasoning"]; exists {
		t.Fatalf("Normalize must not write a reasoning object into Raw: %#v", raw)
	}
	if got := normalized.Reasoning["effort"]; got != "low" {
		t.Fatalf("effort = %#v", got)
	}
}

func TestNormalizeIsIdempotentAcrossFallback(t *testing.T) {
	// A second attempt must reuse the first decision instead of re-deriving one
	// from Raw, so a fallback adapter cannot be handed a different effort.
	first := Normalize(canonical.Request{
		Raw:      map[string]any{"reasoning_effort": "high"},
		Metadata: map[string]any{MetadataAliasEffort: "low"},
	})
	second := Normalize(canonical.Request{
		Reasoning: first.Reasoning,
		// Raw deliberately disagrees; it must be ignored on the second pass.
		Raw:      map[string]any{"reasoning_effort": "low"},
		Metadata: first.Metadata,
	})
	if got := second.Reasoning["effort"]; got != "high" {
		t.Fatalf("fallback changed the effort: %#v", got)
	}
}

func TestNormalizePreservesEnabledChoiceWithoutBudget(t *testing.T) {
	first := Normalize(canonical.Request{
		Raw: map[string]any{"thinking": map[string]any{"type": "adaptive"}},
	})
	if cfg := FromSources(first); !cfg.Set {
		t.Fatal("adaptive thinking choice was lost after normalization")
	}
	second := Normalize(canonical.Request{
		Reasoning: first.Reasoning,
		Raw:       map[string]any{"reasoning_effort": "low"},
		Metadata:  first.Metadata,
	})
	if cfg := FromSources(second); !cfg.Set {
		t.Fatal("adaptive thinking choice was lost on fallback")
	}
}

func TestNormalizePrefersReasoningObjectOverRaw(t *testing.T) {
	normalized := Normalize(canonical.Request{
		Reasoning: map[string]any{"effort": "xhigh"},
		Raw:       map[string]any{"reasoning_effort": "low"},
	})
	if got := normalized.Reasoning["effort"]; got != "xhigh" {
		t.Fatalf("effort = %#v, want xhigh", got)
	}
}

func TestNormalizeEmptyRequestSetsNothing(t *testing.T) {
	normalized := Normalize(canonical.Request{Raw: map[string]any{"model": "gpt-5.6-sol"}})
	if len(normalized.Reasoning) != 0 {
		t.Fatalf("reasoning = %#v, want empty", normalized.Reasoning)
	}
	if !Resolved(normalized) {
		t.Fatal("expected the request to be marked normalized")
	}
}

func TestNormalizeReadsGeminiThinkingConfig(t *testing.T) {
	normalized := Normalize(canonical.Request{
		Source: canonical.ProtocolGemini,
		Raw: map[string]any{
			"generationConfig": map[string]any{
				"thinkingConfig": map[string]any{"thinkingBudget": float64(2000)},
			},
		},
	})
	if got := normalized.Reasoning["budget_tokens"]; got != 2000 {
		t.Fatalf("budget_tokens = %#v", got)
	}
	if got := normalized.Reasoning["effort"]; got != "medium" {
		t.Fatalf("effort = %#v", got)
	}
}

func TestNormalizeEffortRejectsUnknownLabels(t *testing.T) {
	for _, invalid := range []string{"", "default", "inherit", "ultra", "turbo"} {
		if got := NormalizeEffort(invalid); got != "" {
			t.Fatalf("NormalizeEffort(%q) = %q, want empty", invalid, got)
		}
	}
	if got := NormalizeEffort("  XHIGH "); got != "xhigh" {
		t.Fatalf("NormalizeEffort trimmed = %q", got)
	}
}

func TestEffortForBudget(t *testing.T) {
	cases := map[int]string{0: "", 1000: "medium", 7999: "medium", 8000: "high", 23999: "high", 24000: "xhigh"}
	for budget, want := range cases {
		if got := EffortForBudget(budget); got != want {
			t.Fatalf("EffortForBudget(%d) = %q, want %q", budget, got, want)
		}
	}
}
