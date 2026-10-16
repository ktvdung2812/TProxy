package router

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/store"
)

type EffectiveLimit struct {
	State     string   `json:"state"`
	Value     *float64 `json:"value,omitempty"`
	Remaining *float64 `json:"remaining,omitempty"`
	Sources   []string `json:"sources"`
}

type CapacitySnapshot struct {
	EffectiveLimits      map[string]EffectiveLimit `json:"effective_limits"`
	SharedLimits         map[string]EffectiveLimit `json:"shared_limits,omitempty"`
	EligibleAccountCount int                       `json:"eligible_account_count"`
	QuotaWindows         []QuotaSnapshot           `json:"quota_windows"`
	ComputedAt           time.Time                 `json:"computed_at"`
}

func LimitValues(limits config.LimitPolicy) map[string]float64 {
	return map[string]float64{
		"requests_per_minute": float64(limits.RequestsPerMinute),
		"concurrent_streams":  float64(limits.ConcurrentStreams),
		"max_input_bytes":     float64(limits.MaxInputBytes),
		"max_output_tokens":   float64(limits.MaxOutputTokens),
		"media_jobs":          float64(limits.MediaJobs),
		"budget_usd_per_day":  limits.BudgetUSDPerDay,
	}
}

func NewEffectiveLimit(value, used float64, source string) EffectiveLimit {
	result := EffectiveLimit{State: "unbounded", Sources: []string{source}}
	if value > 0 {
		remaining := max(0, value-used)
		result.State, result.Value, result.Remaining = "finite", &value, &remaining
	}
	return result
}

func limitSources(a, b []string) []string {
	seen := map[string]bool{}
	for _, source := range append(append([]string(nil), a...), b...) {
		seen[source] = true
	}
	result := make([]string, 0, len(seen))
	for source := range seen {
		result = append(result, source)
	}
	sort.Strings(result)
	return result
}

// TightenLimit combines independent gates. Remaining is shared consumption at
// each source, never a fresh allowance granted to each API key.
func TightenLimit(base, overlay EffectiveLimit) EffectiveLimit {
	if base.State == "unavailable" || overlay.State == "unbounded" {
		return base
	}
	sources := limitSources(base.Sources, overlay.Sources)
	if base.State != "finite" {
		overlay.Sources = sources
		return overlay
	}
	if overlay.State != "finite" {
		return base
	}
	value := min(*base.Value, *overlay.Value)
	base.Value, base.Sources = &value, sources
	if base.Remaining != nil && overlay.Remaining != nil {
		remaining := min(*base.Remaining, *overlay.Remaining)
		base.Remaining = &remaining
	}
	return base
}

func poolLimit(items []EffectiveLimit, perRequest bool) EffectiveLimit {
	result := EffectiveLimit{State: "unavailable", Sources: []string{}}
	if len(items) == 0 {
		return result
	}
	result.State = "finite"
	value, remaining := 0.0, 0.0
	for _, item := range items {
		result.Sources = limitSources(result.Sources, item.Sources)
		if item.State == "unbounded" {
			result.State = "unbounded"
		}
		if item.State == "unknown" && result.State != "unbounded" {
			result.State = "unknown"
		}
		if item.Value != nil {
			if perRequest {
				value = max(value, *item.Value)
			} else {
				value += *item.Value
			}
		}
		if item.Remaining != nil {
			remaining += *item.Remaining
		}
	}
	if result.State == "finite" {
		result.Value = &value
		if !perRequest {
			result.Remaining = &remaining
		}
	}
	return result
}

type capacityProvider struct {
	provider    store.Provider
	credentials map[string]store.Credential
	media       bool
}

// Capacity resolves only routing topology and eligibility. It never dispatches,
// probes, waits for cooldowns, or invokes the scheduler's rotation functions.
func (r *Router) Capacity(ctx context.Context, key *store.APIKey, modelID string) (CapacitySnapshot, error) {
	now := time.Now().UTC()
	result := CapacitySnapshot{EffectiveLimits: map[string]EffectiveLimit{}, QuotaWindows: []QuotaSnapshot{}, ComputedAt: now}
	for name := range LimitValues(config.LimitPolicy{}) {
		result.EffectiveLimits[name] = poolLimit(nil, false)
	}
	result.EffectiveLimits["concurrent_requests"] = poolLimit(nil, false)
	models := []store.PublicModel{}
	allModels := key == nil
	if key != nil {
		for _, name := range key.Models {
			if name == "*" {
				allModels = true
			}
		}
	}
	if modelID != "" {
		model, err := r.Resolve(ctx, modelID, key)
		if err != nil {
			if strings.Contains(err.Error(), "model_forbidden") || strings.Contains(err.Error(), "model_not_found") {
				return result, nil
			}
			return result, err
		}
		models = append(models, *model)
	} else if allModels {
		var err error
		models, err = r.store.CatalogModels(ctx)
		if err != nil {
			return result, err
		}
		// Wildcard keys can also use direct provider:model selectors without PPM.
		providers, err := r.store.Providers(ctx)
		if err != nil {
			return result, err
		}
		for _, provider := range providers {
			models = append(models, store.PublicModel{ID: provider.ID + ":*", Enabled: provider.Enabled, Capabilities: r.registry.Capabilities(provider.Type)})
		}
	} else {
		for _, name := range key.Models {
			model, err := r.Resolve(ctx, name, key)
			if err != nil {
				if strings.Contains(err.Error(), "model_forbidden") || strings.Contains(err.Error(), "model_not_found") {
					continue
				}
				return result, err
			}
			models = append(models, *model)
		}
	}
	metadata := map[string]any{}
	if key != nil {
		metadata["client_api_key_id"], metadata["team"] = key.ID, key.Policy.Team
		if len(key.Policy.CredentialIDs) > 0 {
			metadata[AllowedCredentialIDsMetadataKey] = key.Policy.CredentialIDs
		}
	}
	groups := map[string]*capacityProvider{}
	seenQuota := map[string]bool{}
	for _, model := range models {
		if !model.Enabled {
			continue
		}
		routes, err := r.modelRoutes(ctx, model, false)
		if err != nil {
			return result, err
		}
		for _, route := range routes {
			if !route.Enabled || !routeIdentityMatches(route, metadata) {
				continue
			}
			provider, err := r.store.Provider(ctx, route.ProviderID)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return result, err
			}
			if !provider.Enabled || provider.Status == "disabled" || !r.capacitySupportsModel(*provider, model, route) {
				continue
			}
			if _, benched := r.modelCapacityUntil(provider.ID, route.UpstreamModel, now); benched {
				continue
			}
			credentials, err := r.store.Credentials(ctx, provider.ID)
			if err != nil {
				return result, err
			}
			credentials = filterAllowedCredentials(credentials, metadata)
			for _, credential := range credentials {
				if !seenQuota[credential.ID] && (credential.Enabled || store.QuotaAutoDisabled(credential.Metadata)) && !store.AccountExpired(credential, now) && credential.Status != "auth_required" {
					result.QuotaWindows = append(result.QuotaWindows, r.CachedQuota(credential.ID, provider.Type))
					seenQuota[credential.ID] = true
				}
			}
			for _, credential := range store.EligibleCredentials(credentials, now) {
				until, err := r.store.ModelCooldownUntil(ctx, credential.ID, route.UpstreamModel, now)
				if err != nil {
					return result, err
				}
				if until.After(now) {
					continue
				}
				available, err := r.hasProxy(ctx, *provider, credential)
				if err != nil {
					return result, err
				}
				if !available {
					continue
				}
				group := groups[provider.ID]
				if group == nil {
					group = &capacityProvider{provider: *provider, credentials: map[string]store.Credential{}}
					groups[provider.ID] = group
				}
				group.credentials[credential.ID] = credential
				for _, capability := range r.registry.Capabilities(provider.Type) {
					if (capability == "*" || capability == "video-output") && (slices.Contains(model.Capabilities, "*") || slices.Contains(model.Capabilities, "video-output")) && (route.Conditions["capability"] == nil || conditionContains(route.Conditions["capability"], "video-output")) {
						group.media = true
					}
				}
			}
		}
	}
	byLimit := map[string][]EffectiveLimit{}
	accounts := map[string]AccountRuntime{}
	for _, account := range r.AccountRuntime() {
		accounts[account.CredentialID] = account
	}
	for id, group := range groups {
		source := "provider:" + id
		limits := group.provider.Limits
		usedCost, err := r.store.ProviderEstimatedCostSince(ctx, id, time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC))
		if err != nil {
			return result, err
		}
		usedJobs, err := r.store.ActiveMediaJobsInScope(ctx, store.MediaJobScope{Kind: "provider", ID: id})
		if err != nil {
			return result, err
		}
		var concurrent []EffectiveLimit
		for cid, credential := range group.credentials {
			concurrent = append(concurrent, NewEffectiveLimit(float64(store.CredentialAdmission(credential.Metadata).MaxConcurrent), float64(accounts[cid].InFlight), "account:"+cid))
		}
		result.EligibleAccountCount += len(group.credentials)
		capacity := poolLimit(concurrent, false)
		byLimit["concurrent_requests"] = append(byLimit["concurrent_requests"], capacity)
		r.mu.Lock()
		streams := r.providerStreams[id]
		r.mu.Unlock()
		used := map[string]float64{"requests_per_minute": float64(r.providerRequestCount(id, now)), "concurrent_streams": float64(streams), "budget_usd_per_day": usedCost, "media_jobs": float64(usedJobs)}
		for name, value := range LimitValues(limits) {
			if name == "media_jobs" && !group.media {
				continue
			}
			limit := NewEffectiveLimit(value, used[name], source)
			if name == "concurrent_streams" {
				limit = TightenLimit(capacity, limit)
			}
			byLimit[name] = append(byLimit[name], limit)
		}
	}
	for name := range result.EffectiveLimits {
		result.EffectiveLimits[name] = poolLimit(byLimit[name], name == "max_input_bytes" || name == "max_output_tokens")
	}
	sort.Slice(result.QuotaWindows, func(i, j int) bool { return result.QuotaWindows[i].CredentialID < result.QuotaWindows[j].CredentialID })
	return result, nil
}

func routeIdentityMatches(route store.RouteTarget, metadata map[string]any) bool {
	conditions := map[string]any{}
	for _, name := range []string{"api_key_id", "team"} {
		if value, ok := route.Conditions[name]; ok {
			conditions[name] = value
		}
	}
	route.Conditions = conditions
	return routeMatches(route, canonical.Request{Metadata: metadata})
}

func (r *Router) hasProxy(ctx context.Context, provider store.Provider, credential store.Credential) (bool, error) {
	ids := credential.ProxyPoolIDs
	if len(ids) == 0 {
		ids = provider.ProxyPoolIDs
	}
	if len(ids) == 0 {
		return true, nil
	}
	for _, id := range ids {
		pool, err := r.store.ProxyPool(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, err
		}
		if pool.Enabled {
			return true, nil
		}
	}
	return false, nil
}

func (r *Router) capacitySupportsModel(provider store.Provider, model store.PublicModel, route store.RouteTarget) bool {
	capabilities := model.Capabilities
	if len(capabilities) == 0 {
		capabilities = []string{"text"}
	}
	for _, supported := range r.registry.Capabilities(provider.Type) {
		if supported == "*" {
			return true
		}
		if (slices.Contains(capabilities, "*") || slices.Contains(capabilities, supported)) && (route.Conditions["capability"] == nil || conditionContains(route.Conditions["capability"], supported)) {
			return true
		}
	}
	return false
}
