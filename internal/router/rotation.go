package router

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/tproxy/tproxy/internal/store"
)

const (
	StrategyFillFirst           = "fill-first"
	StrategyRoundRobin          = "round-robin"
	StrategyWeightedRoundRobin  = "weighted-round-robin"
	defaultStickyRoundRobinLimit = 3
)

type rotationPolicy struct {
	strategy              string
	stickyRoundRobinLimit int
}

func (r *Router) rotationPolicyForProvider(providerID string) rotationPolicy {
	r.mu.Lock()
	defer r.mu.Unlock()
	policy := rotationPolicy{
		strategy:              r.strategy,
		stickyRoundRobinLimit: r.stickyRoundRobinLimit,
	}
	if override, ok := r.providerStrategies[providerID]; ok {
		if override.Strategy != "" {
			policy.strategy = override.Strategy
		}
		if override.StickyRoundRobinLimit > 0 {
			policy.stickyRoundRobinLimit = override.StickyRoundRobinLimit
		}
	}
	if policy.stickyRoundRobinLimit <= 0 {
		policy.stickyRoundRobinLimit = defaultStickyRoundRobinLimit
	}
	return policy
}

func (r *Router) orderCredentials(ctx context.Context, providerID, routeKey string, priority int, credentials []store.Credential) ([]store.Credential, error) {
	if len(credentials) == 0 {
		return credentials, nil
	}
	policy := r.rotationPolicyForProvider(providerID)
	switch policy.strategy {
	case StrategyFillFirst:
		return credentials, nil
	case StrategyWeightedRoundRobin:
		if len(credentials) < 2 {
			return credentials, nil
		}
		return r.rotateWeighted(routeKey, priority, credentials), nil
	case StrategyRoundRobin:
		r.selectionMu.Lock()
		defer r.selectionMu.Unlock()
		ordered, touch := stickyRoundRobinOrder(credentials, policy.stickyRoundRobinLimit, time.Now().UTC())
		if touch != nil {
			if err := r.store.TouchCredentialRotation(ctx, touch.ID, touch.ConsecutiveUseCount, touch.LastUsedAt.Format(time.RFC3339Nano)); err != nil {
				return nil, err
			}
		}
		return ordered, nil
	default:
		if len(credentials) < 2 {
			return credentials, nil
		}
		return r.rotateWeighted(routeKey, priority, credentials), nil
	}
}

type rotationTouch struct {
	ID                    string
	ConsecutiveUseCount   int
	LastUsedAt            time.Time
}

func stickyRoundRobinOrder(credentials []store.Credential, stickyLimit int, now time.Time) ([]store.Credential, *rotationTouch) {
	if stickyLimit <= 0 {
		stickyLimit = defaultStickyRoundRobinLimit
	}
	byRecency := append([]store.Credential(nil), credentials...)
	sort.SliceStable(byRecency, func(i, j int) bool {
		return compareCredentialRecency(byRecency[i], byRecency[j], true)
	})
	current := byRecency[0]
	currentCount := current.ConsecutiveUseCount
	if !current.LastUsedAt.IsZero() && currentCount < stickyLimit {
		touch := rotationTouch{
			ID:                  current.ID,
			ConsecutiveUseCount: current.ConsecutiveUseCount + 1,
			LastUsedAt:          now,
		}
		current.LastUsedAt = now
		current.ConsecutiveUseCount = touch.ConsecutiveUseCount
		return prependCredential(current, withoutCredential(credentials, current.ID)), &touch
	}
	byOldest := append([]store.Credential(nil), credentials...)
	sort.SliceStable(byOldest, func(i, j int) bool {
		return compareCredentialRecency(byOldest[i], byOldest[j], false)
	})
	next := byOldest[0]
	touch := rotationTouch{
		ID:                  next.ID,
		ConsecutiveUseCount: 1,
		LastUsedAt:          now,
	}
	next.LastUsedAt = now
	next.ConsecutiveUseCount = 1
	return prependCredential(next, withoutCredential(credentials, next.ID)), &touch
}

func compareCredentialRecency(left, right store.Credential, mostRecentFirst bool) bool {
	if left.LastUsedAt.IsZero() && right.LastUsedAt.IsZero() {
		if left.Priority == right.Priority {
			return left.ID < right.ID
		}
		return left.Priority > right.Priority
	}
	if left.LastUsedAt.IsZero() {
		if mostRecentFirst {
			return false
		}
		return true
	}
	if right.LastUsedAt.IsZero() {
		if mostRecentFirst {
			return true
		}
		return false
	}
	if mostRecentFirst {
		if left.LastUsedAt.Equal(right.LastUsedAt) {
			if left.Priority == right.Priority {
				return left.ID < right.ID
			}
			return left.Priority > right.Priority
		}
		return left.LastUsedAt.After(right.LastUsedAt)
	}
	if left.LastUsedAt.Equal(right.LastUsedAt) {
		if left.Priority == right.Priority {
			return left.ID < right.ID
		}
		return left.Priority > right.Priority
	}
	return left.LastUsedAt.Before(right.LastUsedAt)
}

func prependCredential(primary store.Credential, rest []store.Credential) []store.Credential {
	result := make([]store.Credential, 0, len(rest)+1)
	result = append(result, primary)
	result = append(result, rest...)
	return result
}

func withoutCredential(credentials []store.Credential, credentialID string) []store.Credential {
	result := make([]store.Credential, 0, len(credentials))
	for _, credential := range credentials {
		if credential.ID != credentialID {
			result = append(result, credential)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		return compareCredentialRecency(result[i], result[j], false)
	})
	return result
}

func (r *Router) rotateWeighted(routeKey string, priority int, credentials []store.Credential) []store.Credential {
	key := fmt.Sprintf("%s:%d", routeKey, priority)
	r.mu.Lock()
	totalWeight := 0
	for _, credential := range credentials {
		weight := credential.Weight
		if weight <= 0 {
			weight = 1
		}
		totalWeight += weight
	}
	slot := 0
	if totalWeight > 0 {
		slot = r.rotation[key] % totalWeight
		r.rotation[key] = (slot + 1) % totalWeight
	}
	r.mu.Unlock()
	index := 0
	for candidateIndex, credential := range credentials {
		weight := credential.Weight
		if weight <= 0 {
			weight = 1
		}
		if slot < weight {
			index = candidateIndex
			break
		}
		slot -= weight
	}
	result := make([]store.Credential, 0, len(credentials))
	result = append(result, credentials[index:]...)
	result = append(result, credentials[:index]...)
	return result
}
