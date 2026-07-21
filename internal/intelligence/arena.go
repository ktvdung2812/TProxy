package intelligence

import (
	"sort"
	"sync"

	"github.com/tproxy/tproxy/internal/store"
)

const defaultRating = 1500.0

// Arena tracks per-credential ELO ratings for arena-elo routing.
type Arena struct {
	mu      sync.RWMutex
	ratings map[string]float64
}

func NewArena() *Arena {
	return &Arena{ratings: map[string]float64{}}
}

func credentialKey(credential store.Credential) string {
	return credential.ProviderID + ":" + credential.ID
}

func (a *Arena) Rating(credential store.Credential) float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if rating, ok := a.ratings[credentialKey(credential)]; ok {
		return rating
	}
	return defaultRating
}

func (a *Arena) RecordOutcome(credential store.Credential, won bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := credentialKey(credential)
	current := defaultRating
	if rating, ok := a.ratings[key]; ok {
		current = rating
	}
	delta := 16.0
	if !won {
		delta = -12.0
	}
	next := current + delta
	if next < 800 {
		next = 800
	}
	if next > 2400 {
		next = 2400
	}
	a.ratings[key] = next
}

func (a *Arena) OrderCredentials(credentials []store.Credential) []store.Credential {
	ordered := append([]store.Credential(nil), credentials...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left := a.Rating(ordered[i])
		right := a.Rating(ordered[j])
		if left == right {
			return ordered[i].ID < ordered[j].ID
		}
		return left > right
	})
	return ordered
}

func (a *Arena) Snapshot() map[string]float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make(map[string]float64, len(a.ratings))
	for key, rating := range a.ratings {
		out[key] = rating
	}
	return out
}
