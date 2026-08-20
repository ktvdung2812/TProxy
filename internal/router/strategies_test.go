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
	if len(AllStrategies) != 19 {
		t.Fatalf("expected 19 strategies, got %d", len(AllStrategies))
	}
}

func TestSortExpiringFirstOrdersByRenewalDate(t *testing.T) {
	credentials := []store.Credential{
		{ID: "no-date", Priority: 10},
		{ID: "later", Metadata: map[string]any{"quota_renews_at": "2026-10-01T00:00:00Z"}},
		{ID: "sooner", Metadata: map[string]any{"quota_renews_at": "2026-09-14T04:43:46Z"}},
	}
	ordered := sortExpiringFirst(credentials)
	if ordered[0].ID != "sooner" || ordered[1].ID != "later" || ordered[2].ID != "no-date" {
		t.Fatalf("order = %+v", ordered)
	}
}

func TestSortExpiringFirstFallsBackToPriorityThenID(t *testing.T) {
	credentials := []store.Credential{
		{ID: "b", Priority: 1},
		{ID: "a", Priority: 1},
		{ID: "c", Priority: 5},
	}
	ordered := sortExpiringFirst(credentials)
	if ordered[0].ID != "c" || ordered[1].ID != "a" || ordered[2].ID != "b" {
		t.Fatalf("order = %+v", ordered)
	}
}

func TestExpiringFirstViaStrategy(t *testing.T) {
	if !IsValidStrategy(StrategyExpiringFirst) {
		t.Fatal("expiring-first should be a valid strategy")
	}
	credentials := []store.Credential{
		{ID: "later", Metadata: map[string]any{"quota_renews_at": "2026-10-01T00:00:00Z"}},
		{ID: "sooner", Metadata: map[string]any{"quota_renews_at": "2026-09-14T04:43:46Z"}},
	}
	ordered, _ := orderCredentialsByStrategy(StrategyExpiringFirst, credentials, credentialOrderContext{})
	if ordered[0].ID != "sooner" {
		t.Fatalf("order = %+v", ordered)
	}
}

func TestIsValidStrategy(t *testing.T) {
	if !IsValidStrategy(StrategyArenaELO) || IsValidStrategy("not-a-strategy") {
		t.Fatalf("strategy validation mismatch")
	}
}
