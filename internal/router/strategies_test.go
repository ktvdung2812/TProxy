package router

import (
	"testing"

	"github.com/tproxy/tproxy/internal/store"
)

func TestTaskAwarePrefersCodingCredential(t *testing.T) {
	credentials := []store.Credential{
		{ID: "a", Label: "general"},
		{ID: "b", Label: "codex coding"},
	}
	ordered := orderTaskAware(credentials, "coding refactor")
	if ordered[0].ID != "b" {
		t.Fatalf("expected coding credential first, got %+v", ordered)
	}
}

func TestAllStrategiesCount(t *testing.T) {
	if len(AllStrategies) != 18 {
		t.Fatalf("expected 18 strategies, got %d", len(AllStrategies))
	}
}

func TestIsValidStrategy(t *testing.T) {
	if !IsValidStrategy(StrategyArenaELO) || IsValidStrategy("not-a-strategy") {
		t.Fatalf("strategy validation mismatch")
	}
}
