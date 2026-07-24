package providers

import (
	"testing"

	"github.com/tproxy/tproxy/internal/store"
)

func TestExpandKiroModelVariants(t *testing.T) {
	variants := expandKiroModelVariants("claude-sonnet-5", "Kiro Claude Sonnet 5")
	if len(variants) != 4 {
		t.Fatalf("expected 4 variants, got %d", len(variants))
	}
	auto := expandKiroModelVariants("auto", "Kiro Auto")
	if len(auto) != 2 {
		t.Fatalf("expected 2 auto variants, got %d", len(auto))
	}
}

func TestKiroStaticModelEntries(t *testing.T) {
	provider := store.Provider{ID: "kiro", Type: "kiro"}
	items := kiroStaticModelEntries(NewRegistry(), provider)
	if len(items) == 0 {
		t.Fatal("expected static kiro models")
	}
	found := false
	for _, item := range items {
		if item.ID == "claude-sonnet-5-thinking" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected claude-sonnet-5-thinking in static catalog")
	}
}
