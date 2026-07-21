package intelligence

import (
	"testing"

	"github.com/tproxy/tproxy/internal/store"
)

func TestArenaOrdersByRating(t *testing.T) {
	arena := NewArena()
	high := store.Credential{ID: "high", ProviderID: "p1"}
	low := store.Credential{ID: "low", ProviderID: "p1"}
	arena.RecordOutcome(high, true)
	arena.RecordOutcome(high, true)
	arena.RecordOutcome(low, false)
	ordered := arena.OrderCredentials([]store.Credential{low, high})
	if ordered[0].ID != "high" {
		t.Fatalf("expected high-rated credential first, got %+v", ordered)
	}
}
