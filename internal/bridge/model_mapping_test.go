package bridge

import "testing"

func TestNormalizeModelMappings(t *testing.T) {
	got := NormalizeModelMappings(map[string]string{
		"GPT-5.6-Sol": "deepseek-v4-pro",
		"  fable  ":   "claude-sonnet-4-5",
		"identity":    "identity",
		"":            "target",
		"orphan":      "",
	})
	if len(got) != 2 {
		t.Fatalf("expected 2 normalized mappings, got %d: %#v", len(got), got)
	}
	if got["gpt-5.6-sol"] != "deepseek-v4-pro" {
		t.Fatalf("gpt-5.6-sol = %q", got["gpt-5.6-sol"])
	}
	if got["fable"] != "claude-sonnet-4-5" {
		t.Fatalf("fable = %q", got["fable"])
	}
}

func TestModelMappingResolveExact(t *testing.T) {
	resolver := NewModelMappingResolver()
	resolver.SetMappings(ModelMappings{"gpt-5.6-sol": "deepseek-v4-pro"})

	if got := resolver.Resolve("GPT-5.6-Sol"); got != "deepseek-v4-pro" {
		t.Fatalf("case-insensitive resolve failed: %q", got)
	}
	if got := resolver.Resolve("other-model"); got != "other-model" {
		t.Fatalf("unknown model should pass through: %q", got)
	}
	if got := resolver.Resolve(""); got != "" {
		t.Fatalf("empty model should stay empty: %q", got)
	}
}

func TestModelMappingResolveChain(t *testing.T) {
	resolver := NewModelMappingResolver()
	resolver.SetMappings(ModelMappings{
		"a": "b",
		"b": "c",
		"c": "final-model",
	})
	if got := resolver.ResolveChain("a"); got != "final-model" {
		t.Fatalf("chain resolution = %q, want final-model", got)
	}
}

func TestModelMappingResolveCycle(t *testing.T) {
	resolver := NewModelMappingResolver()
	resolver.SetMappings(ModelMappings{
		"x": "y",
		"y": "x",
	})
	if got := resolver.ResolveChain("x"); got == "" {
		t.Fatal("cycle should stop at a non-empty name")
	}
}

func TestModelMappingIdentityNoOp(t *testing.T) {
	resolver := NewModelMappingResolver()
	resolver.SetMappings(ModelMappings{"same": "same"})
	if _, ok := resolver.EffectiveMapping()["same"]; ok {
		t.Fatal("identity mapping should be dropped by normalization")
	}
	if got := resolver.Resolve("same"); got != "same" {
		t.Fatalf("identity mapping changed the model: %q", got)
	}
}
