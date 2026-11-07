package router

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/store"
)

// ClusterProviderID is the id and type of the cluster-managed provider whose
// credentials are the peer nodes.
const ClusterProviderID = "tproxy-cluster"

// ClusterRelayedMetadataKey marks a request another node relayed here. Such a
// request is served from local accounts only, which caps forwarding at one hop.
const ClusterRelayedMetadataKey = "cluster_relayed"

// ClusterServesModelsMetadataKey lists, on a peer credential, the public models
// that peer serves.
const ClusterServesModelsMetadataKey = "serves_models"

// ClusterRouting exposes the cluster catalog to the router. ClusterRoute is on
// the request path, so it must be cheap.
type ClusterRouting interface {
	ClusterRoute(modelID string) (mode string, pricing *config.PricingConfig, ok bool)
}

type clusterHook struct {
	mu      sync.RWMutex
	routing ClusterRouting
}

// SetClusterRouting installs the cluster catalog. It is safe for concurrent
// use; nil stops adding cluster routes.
func (r *Router) SetClusterRouting(c ClusterRouting) {
	r.cluster.mu.Lock()
	r.cluster.routing = c
	r.cluster.mu.Unlock()
}

func (r *Router) clusterRoute(modelID string) (mode string, pricing *config.PricingConfig, ok bool) {
	r.cluster.mu.RLock()
	routing := r.cluster.routing
	r.cluster.mu.RUnlock()
	if routing == nil {
		return "", nil, false
	}
	return routing.ClusterRoute(modelID)
}

// withClusterRoute adds the virtual route that borrows modelID from the
// cluster. "shared" ties it with the best local route so rotation spreads load
// across both; any other mode ("fallback") places it below every local route.
// The result keeps the priority-descending order rotateRoutes groups on.
func (r *Router) withClusterRoute(modelID string, routes []store.RouteTarget) []store.RouteTarget {
	mode, pricing, ok := r.clusterRoute(modelID)
	if !ok {
		return routes
	}
	shared := strings.EqualFold(strings.TrimSpace(mode), "shared")
	priority, local := 0, false
	for _, route := range routes {
		if !route.Enabled || route.ProviderID == ClusterProviderID {
			continue
		}
		switch {
		case !local:
			priority, local = route.Priority, true
		case shared:
			priority = max(priority, route.Priority)
		default:
			priority = min(priority, route.Priority)
		}
	}
	if local && !shared {
		priority--
	}
	route := store.RouteTarget{ID: "cluster:" + modelID, PublicModelID: modelID, ProviderID: ClusterProviderID, UpstreamModel: modelID, Priority: priority, Weight: 1, Enabled: true, Pricing: pricing}
	index := len(routes)
	for i, existing := range routes {
		if existing.Priority < priority {
			index = i
			break
		}
	}
	return slices.Insert(slices.Clone(routes), index, route)
}

// IsClusterRelayed reports whether another cluster node relayed this request.
func IsClusterRelayed(request canonical.Request) bool {
	switch value := request.Metadata[ClusterRelayedMetadataKey].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	}
	return false
}

// filterServedModels drops credentials that list the models they serve when
// upstreamModel is not among them. Credentials without the list are kept.
func filterServedModels(credentials []store.Credential, upstreamModel string) []store.Credential {
	filtered := make([]store.Credential, 0, len(credentials))
	for _, credential := range credentials {
		value := credential.Metadata[ClusterServesModelsMetadataKey]
		served := value == nil
		switch models := value.(type) {
		case []string:
			served = slices.Contains(models, upstreamModel)
		case []any:
			served = slices.Contains(models, any(upstreamModel))
		}
		if served {
			filtered = append(filtered, credential)
		}
	}
	return filtered
}

// LocalCapacity counts distinct eligible LOCAL credentials (never tproxy-cluster) across the enabled routes of the public model
// modelID (resolve by id with the store, no key/team scope; combos → 0). Applies provider enabled/status, EligibleCredentials and
// per-model cooldown filtering. Must not advance rotation state.
func (r *Router) LocalCapacity(ctx context.Context, modelID string) (int, error) {
	model, err := r.store.PublicModel(ctx, modelID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !model.Enabled || len(model.ComboItems) > 0 {
		return 0, nil
	}
	// Read the stored routes directly: modelRoutes would consult ClusterRoute,
	// which may itself be built on LocalCapacity.
	routes, err := r.store.Routes(ctx, model.ID)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	counted := map[string]bool{}
	for _, route := range routes {
		if !route.Enabled {
			continue
		}
		provider, err := r.store.Provider(ctx, route.ProviderID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if provider.Type == ClusterProviderID || !provider.Enabled || provider.Status == "disabled" {
			continue
		}
		if _, benched := r.modelCapacityUntil(provider.ID, route.UpstreamModel, now); benched {
			continue
		}
		credentials, err := r.store.Credentials(ctx, provider.ID)
		if err != nil {
			return 0, err
		}
		for _, credential := range store.EligibleCredentials(credentials, now) {
			if counted[credential.ID] {
				continue
			}
			until, err := r.store.ModelCooldownUntil(ctx, credential.ID, route.UpstreamModel, now)
			if err != nil {
				return 0, err
			}
			if !until.After(now) {
				counted[credential.ID] = true
			}
		}
	}
	return len(counted), nil
}
