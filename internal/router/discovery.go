package router

import (
	"context"
	"sort"
	"time"

	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/store"
)

const providerDiscoveryCacheTTL = 90 * time.Second

type discoveryCacheEntry struct {
	items     []providers.DiscoveredModel
	err       error
	expiresAt time.Time
}

type discoveryFlight struct {
	done chan struct{}
	items []providers.DiscoveredModel
	err   error
}

func (r *Router) DiscoverProviderModels(ctx context.Context, providerID string) ([]providers.DiscoveredModel, error) {
	return r.discoverProviderModels(ctx, providerID, false)
}

func (r *Router) RefreshProviderModels(ctx context.Context, providerID string) ([]providers.DiscoveredModel, error) {
	return r.discoverProviderModels(ctx, providerID, true)
}

func (r *Router) discoverProviderModels(ctx context.Context, providerID string, refresh bool) ([]providers.DiscoveredModel, error) {
	if !refresh {
		if cached, ok := r.cachedProviderDiscovery(providerID); ok {
			return cached.items, cached.err
		}
	}
	if items, err, shared := r.awaitProviderDiscoveryFlight(providerID, refresh); shared {
		return items, err
	}
	items, err := r.discoverProviderModelsFromUpstream(ctx, providerID, refresh)
	r.finishProviderDiscoveryFlight(providerID, items, err)
	if !refresh {
		r.storeProviderDiscoveryCache(providerID, items, err)
	}
	return items, err
}

func (r *Router) cachedProviderDiscovery(providerID string) (discoveryCacheEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.discoveryCache == nil {
		return discoveryCacheEntry{}, false
	}
	entry, ok := r.discoveryCache[providerID]
	if !ok || time.Now().After(entry.expiresAt) {
		return discoveryCacheEntry{}, false
	}
	return entry, true
}

func (r *Router) storeProviderDiscoveryCache(providerID string, items []providers.DiscoveredModel, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.discoveryCache == nil {
		r.discoveryCache = make(map[string]discoveryCacheEntry)
	}
	copied := append([]providers.DiscoveredModel(nil), items...)
	r.discoveryCache[providerID] = discoveryCacheEntry{
		items:     copied,
		err:       err,
		expiresAt: time.Now().Add(providerDiscoveryCacheTTL),
	}
}

func (r *Router) awaitProviderDiscoveryFlight(providerID string, refresh bool) ([]providers.DiscoveredModel, error, bool) {
	r.discoveryMu.Lock()
	if r.discoveryInflight == nil {
		r.discoveryInflight = make(map[string]*discoveryFlight)
	}
	if refresh {
		delete(r.discoveryInflight, providerID)
	}
	flight, exists := r.discoveryInflight[providerID]
	if exists {
		r.discoveryMu.Unlock()
		<-flight.done
		return flight.items, flight.err, true
	}
	flight = &discoveryFlight{done: make(chan struct{})}
	r.discoveryInflight[providerID] = flight
	r.discoveryMu.Unlock()
	return nil, nil, false
}

func (r *Router) finishProviderDiscoveryFlight(providerID string, items []providers.DiscoveredModel, err error) {
	r.discoveryMu.Lock()
	flight := r.discoveryInflight[providerID]
	if flight != nil {
		flight.items = append([]providers.DiscoveredModel(nil), items...)
		flight.err = err
		close(flight.done)
		delete(r.discoveryInflight, providerID)
	}
	r.discoveryMu.Unlock()
}

func (r *Router) discoverProviderModelsFromUpstream(ctx context.Context, providerID string, fullScan bool) ([]providers.DiscoveredModel, error) {
	provider, err := r.store.Provider(ctx, providerID)
	if err != nil {
		return nil, err
	}
	credentials, err := r.store.Credentials(ctx, providerID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	candidates := store.EligibleCredentials(credentials, now)
	if len(candidates) == 0 {
		for _, credential := range credentials {
			if credential.Enabled {
				candidates = append(candidates, credential)
			}
		}
	}
	if len(candidates) == 0 && len(credentials) > 0 {
		candidates = append(candidates, credentials[0])
	}
	ordered, errOrder := r.orderCredentials(ctx, providerID, "discovery:"+providerID, 0, candidates)
	if errOrder != nil {
		return nil, errOrder
	}
	candidates = ordered

	merged := make(map[string]providers.DiscoveredModel)
	credentialIDsByModel := make(map[string]map[string]struct{})
	var lastErr error
	healthyCount := 0
	fastPath := !fullScan && len(candidates) > 5

	for _, credential := range candidates {
		items, discoverErr := r.registry.DiscoverModels(ctx, *provider, credential)
		if discoverErr != nil {
			lastErr = discoverErr
			if credential.ID != "" {
				r.setCredentialCooldown(ctx, credential.ID, "", discoverErr)
			}
			continue
		}
		healthyCount++
		if credential.ID != "" {
			_ = r.store.ClearCooldown(ctx, credential.ID)
		}
		for _, item := range items {
			if item.ID == "" {
				continue
			}
			if _, ok := merged[item.ID]; !ok {
				merged[item.ID] = item
			}
			if credentialIDsByModel[item.ID] == nil {
				credentialIDsByModel[item.ID] = make(map[string]struct{})
			}
			if credential.ID != "" {
				credentialIDsByModel[item.ID][credential.ID] = struct{}{}
			}
		}
		if fastPath && len(merged) > 0 {
			break
		}
	}

	result := make([]providers.DiscoveredModel, 0, len(merged))
	for id, item := range merged {
		ids := make([]string, 0, len(credentialIDsByModel[id]))
		for credentialID := range credentialIDsByModel[id] {
			ids = append(ids, credentialID)
		}
		sort.Strings(ids)
		item.CredentialIDs = ids
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })

	status := "healthy"
	message := ""
	if healthyCount == 0 && lastErr != nil {
		status = "degraded"
		message = lastErr.Error()
	} else if healthyCount > 0 && healthyCount < len(candidates) && lastErr != nil {
		status = "degraded"
		message = lastErr.Error()
	}
	_ = r.store.SetProviderHealth(ctx, providerID, status, message, now)
	if len(result) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return result, nil
}
