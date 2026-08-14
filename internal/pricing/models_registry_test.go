package pricing

import (
	"testing"

	"github.com/tproxy/tproxy/internal/store"
)

func TestBuildModelsRegistryIndex(t *testing.T) {
	payload := map[string]any{
		"openai/gpt-5.6-luna":         map[string]any{"id": "openai/gpt-5.6-luna", "name": "GPT-5.6 Luna"},
		"anthropic/claude-sonnet-4-5": map[string]any{"id": "anthropic/claude-sonnet-4-5"},
	}
	full, bare, names := buildModelsRegistryIndex(payload)
	if _, ok := full["openai/gpt-5.6-luna"]; !ok {
		t.Fatalf("full index missing openai/gpt-5.6-luna: %#v", full)
	}
	if _, ok := bare["gpt-5.6-luna"]; !ok {
		t.Fatalf("bare index missing gpt-5.6-luna: %#v", bare)
	}
	if names["gpt-5.6-luna"] != "GPT-5.6 Luna" {
		t.Fatalf("names index missing display name: %#v", names)
	}
}

func TestModelsRegistryKnownRoute(t *testing.T) {
	registry := NewModelsRegistry(ModelsRegistryOptions{})
	full, bare, names := buildModelsRegistryIndex(map[string]any{
		"openai/gpt-5.6-luna": map[string]any{"id": "openai/gpt-5.6-luna"},
	})
	registry.full = full
	registry.bare = bare
	registry.names = names

	provider := store.Provider{Type: "codex", ID: "codex-main"}
	if !registry.KnownRoute(provider, "gpt-5.6-luna") {
		t.Fatal("expected codex upstream gpt-5.6-luna to be known")
	}
	if registry.KnownRoute(provider, "totally-unknown-model") {
		t.Fatal("unexpected unknown model match")
	}
	if !registry.KnownModelRef("openai/gpt-5.6-luna") {
		t.Fatal("expected full model ref to match")
	}
}
