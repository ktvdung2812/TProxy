package router

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/intelligence"
	"github.com/tproxy/tproxy/internal/pricing"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/resilience"
	"github.com/tproxy/tproxy/internal/store"
)

type Router struct {
	store                 *store.Store
	registry              *providers.Registry
	refresher             CredentialRefresher
	mu                    sync.Mutex
	selectionMu           sync.Mutex
	discoveryMu           sync.Mutex
	rotation              map[string]int
	discoveryCache        map[string]discoveryCacheEntry
	discoveryInflight     map[string]*discoveryFlight
	cooldown              time.Duration
	cooldowns             CooldownSettings
	allowUpstream         bool
	strategy              string
	stickyRoundRobinLimit int
	providerStrategies    map[string]store.ProviderRotationStrategy
	sessionAffinity       bool
	sessionTTL            time.Duration
	sessions              map[string]sessionBinding
	providerStreams       map[string]int
	pricing               *pricing.Catalog
	modelsRegistry        *pricing.ModelsRegistry
	circuitBreakers       *resilience.Registry
	arena                 *intelligence.Arena
}

type CredentialRefresher interface {
	EnsureValid(context.Context, store.Provider, store.Credential, bool) (store.Credential, error)
}

type sessionBinding struct {
	CredentialID string
	ExpiresAt    time.Time
}

type Selection struct {
	Model      store.PublicModel
	Route      store.RouteTarget
	Provider   store.Provider
	Credential store.Credential
	Attempt    int
}

type Result struct {
	Selection Selection
	Response  *canonical.Response
	CostUSD   float64
}

type StreamResult struct {
	Selection Selection
	Events    <-chan canonical.Event
}

type RawResult struct {
	Selection Selection
	Response  *providers.RawResponse
}

type RawProxyOptions struct {
	Method             string
	Headers            http.Header
	RetryNetworkErrors bool
	DisableFallback    bool
	ClientAPIKeyID     string
	Team               string
	PinnedProvider     string
}

func New(dataStore *store.Store, registry *providers.Registry) *Router {
	return &Router{
		store: dataStore, registry: registry, rotation: make(map[string]int), cooldown: time.Minute,
		cooldowns: CooldownSettingsFromConfig(config.CooldownConfig{}), strategy: StrategyRoundRobin,
		stickyRoundRobinLimit: defaultStickyRoundRobinLimit, providerStrategies: map[string]store.ProviderRotationStrategy{},
		sessionTTL: time.Hour, sessions: make(map[string]sessionBinding), providerStreams: make(map[string]int),
		circuitBreakers: resilience.NewRegistry(),
		arena:           intelligence.NewArena(),
	}
}

func (r *Router) CircuitBreakers() *resilience.Registry {
	if r.circuitBreakers == nil {
		r.circuitBreakers = resilience.NewRegistry()
	}
	return r.circuitBreakers
}

func (r *Router) SetCredentialRefresher(refresher CredentialRefresher) {
	r.mu.Lock()
	r.refresher = refresher
	r.mu.Unlock()
}

func (r *Router) SetAllowUpstreamModels(enabled bool) { r.allowUpstream = enabled }

func (r *Router) allowUpstreamModelResolution() bool {
	if r.allowUpstream {
		return true
	}
	registry := r.modelsRegistry
	return registry != nil && registry.FilteringEnabled()
}

func (r *Router) SetPricingCatalog(catalog *pricing.Catalog) {
	r.mu.Lock()
	r.pricing = catalog
	r.mu.Unlock()
}

func (r *Router) SetModelsRegistry(registry *pricing.ModelsRegistry) {
	r.mu.Lock()
	r.modelsRegistry = registry
	r.mu.Unlock()
}

type CatalogDisplayEntry struct {
	ID   string
	Name string
}

func (r *Router) CatalogDisplayEntry(ctx context.Context, model store.PublicModel) CatalogDisplayEntry {
	if len(model.ComboItems) > 0 {
		return CatalogDisplayEntry{ID: model.ID, Name: catalogDisplayName(model)}
	}
	registry := r.modelsRegistry
	upstream, known := r.catalogUpstreamModel(ctx, model)
	useUpstream := model.ExposeUpstreamName
	if registry != nil && registry.FilteringEnabled() && known {
		useUpstream = true
	}
	if useUpstream && upstream != "" {
		name := catalogDisplayName(model)
		if registry != nil {
			if catalogName, ok := registry.ModelDisplayName(upstream); ok {
				name = catalogName
			}
		}
		return CatalogDisplayEntry{ID: upstream, Name: name}
	}
	return CatalogDisplayEntry{ID: model.ID, Name: catalogDisplayName(model)}
}

func catalogDisplayName(model store.PublicModel) string {
	if strings.TrimSpace(model.DisplayName) != "" {
		return model.DisplayName
	}
	return model.ID
}

func (r *Router) catalogUpstreamModel(ctx context.Context, model store.PublicModel) (upstream string, known bool) {
	routes, err := r.store.Routes(ctx, model.ID)
	if err != nil {
		return "", false
	}
	registry := r.modelsRegistry
	for _, route := range routes {
		if !route.Enabled {
			continue
		}
		if registry == nil || !registry.FilteringEnabled() {
			return route.UpstreamModel, true
		}
		provider, err := r.store.Provider(ctx, route.ProviderID)
		if err != nil {
			continue
		}
		if registry.KnownRoute(*provider, route.UpstreamModel) {
			return route.UpstreamModel, true
		}
	}
	return "", false
}

func (r *Router) IsModelCatalogVisible(ctx context.Context, model store.PublicModel) bool {
	registry := r.modelsRegistry
	if registry == nil || !registry.FilteringEnabled() {
		return true
	}
	if len(model.ComboItems) > 0 {
		return true
	}
	if registry.KnownModelRef(model.ID) {
		return true
	}
	for _, alias := range model.Aliases {
		if registry.KnownModelRef(alias) {
			return true
		}
	}
	routes, err := r.store.Routes(ctx, model.ID)
	if err != nil {
		return false
	}
	for _, route := range routes {
		if !route.Enabled {
			continue
		}
		provider, err := r.store.Provider(ctx, route.ProviderID)
		if err != nil {
			continue
		}
		if registry.KnownRoute(*provider, route.UpstreamModel) {
			return true
		}
	}
	return false
}

func (r *Router) ConfigureRouting(cfg config.RoutingConfig) {
	strategy := cfg.Strategy
	if strategy == "" {
		strategy = StrategyRoundRobin
	}
	ttl, err := time.ParseDuration(cfg.SessionAffinityTTL)
	if err != nil || ttl <= 0 {
		ttl = time.Hour
	}
	stickyLimit := cfg.StickyRoundRobinLimit
	if stickyLimit <= 0 {
		stickyLimit = defaultStickyRoundRobinLimit
	}
	providerStrategies := map[string]store.ProviderRotationStrategy{}
	for providerID, providerCfg := range cfg.ProviderStrategies {
		providerStrategies[providerID] = store.ProviderRotationStrategy{
			Strategy:              providerCfg.Strategy,
			StickyRoundRobinLimit: providerCfg.StickyRoundRobinLimit,
		}
	}
	r.mu.Lock()
	r.strategy = strategy
	r.stickyRoundRobinLimit = stickyLimit
	r.providerStrategies = providerStrategies
	r.sessionAffinity = cfg.SessionAffinity
	r.sessionTTL = ttl
	r.cooldowns = CooldownSettingsFromConfig(cfg.Cooldown)
	r.cooldown = r.cooldowns.Fallback
	r.mu.Unlock()
}

func (r *Router) SyncAccountRotationSettings(ctx context.Context) error {
	settings, err := r.store.AccountRotationSettings(ctx)
	if err != nil {
		return err
	}
	r.mu.Lock()
	if settings.Strategy != "" {
		r.strategy = settings.Strategy
	}
	if settings.StickyRoundRobinLimit > 0 {
		r.stickyRoundRobinLimit = settings.StickyRoundRobinLimit
	}
	if len(settings.ProviderStrategies) > 0 {
		merged := map[string]store.ProviderRotationStrategy{}
		for key, value := range r.providerStrategies {
			merged[key] = value
		}
		for key, value := range settings.ProviderStrategies {
			merged[key] = value
		}
		r.providerStrategies = merged
	}
	r.mu.Unlock()
	return nil
}

func (r *Router) ResetRotationRuntimeState() {
	r.mu.Lock()
	r.rotation = make(map[string]int)
	r.mu.Unlock()
}

func (r *Router) Resolve(ctx context.Context, requested string, apiKey *store.APIKey) (*store.PublicModel, error) {
	apiKeyID := ""
	teamID := ""
	if apiKey != nil {
		apiKeyID = apiKey.ID
		teamID = apiKey.Policy.Team
	}
	if variant, isAuto := ParseAutoModel(requested); isAuto {
		model, err := r.resolveAutoModel(ctx, variant, apiKey)
		if err != nil {
			return nil, err
		}
		return model, nil
	}
	if providerPrefix, alias, ok := splitProviderModelSelector(requested); ok {
		model, err := r.store.ResolveProviderModelScoped(ctx, providerPrefix, alias, apiKeyID, teamID)
		if errors.Is(err, sql.ErrNoRows) {
			model, err = r.resolveDirectProviderModel(ctx, providerPrefix, alias)
		}
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("model_not_found: %s", requested)
			}
			return nil, err
		}
		if !r.store.PublicModelAllowed(apiKey, model.ID) {
			return nil, fmt.Errorf("model_forbidden: %s", model.ID)
		}
		return model, nil
	}
	model, err := r.store.ResolveModelScoped(ctx, requested, apiKeyID, teamID)
	if errors.Is(err, sql.ErrNoRows) {
		model, err = r.store.ResolveCombo(ctx, requested)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) && r.allowUpstreamModelResolution() {
			model, err = r.store.ResolveUpstreamModel(ctx, requested)
		}
		if errors.Is(err, sql.ErrNoRows) {
			model, err = r.resolveCodexBareModel(ctx, requested)
		}
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("model_not_found: %s", requested)
		}
		if err != nil {
			return nil, err
		}
	}
	if model == nil {
		if model, err = r.resolveCodexBareModel(ctx, requested); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("model_not_found: %s", requested)
			}
			return nil, err
		}
	}
	if !r.store.PublicModelAllowed(apiKey, model.ID) {
		return nil, fmt.Errorf("model_forbidden: %s", model.ID)
	}
	return model, nil
}

// resolveCodexBareModel routes Codex-internal upstream slugs (e.g. codex-auto-review)
// that the CLI calls without a provider prefix when approvals_reviewer=auto_review.
func (r *Router) resolveCodexBareModel(ctx context.Context, requested string) (*store.PublicModel, error) {
	if !isCodexBareUpstreamModel(requested) {
		return nil, sql.ErrNoRows
	}
	return r.resolveDirectProviderModel(ctx, "codex", strings.TrimSpace(requested))
}

func isCodexBareUpstreamModel(requested string) bool {
	trimmed := strings.TrimSpace(requested)
	if trimmed == "" || strings.Contains(trimmed, ":") {
		return false
	}
	return strings.HasPrefix(trimmed, "codex-")
}

func splitProviderModelSelector(requested string) (providerPrefix, alias string, ok bool) {
	trimmed := strings.TrimSpace(requested)
	if trimmed == "" {
		return "", "", false
	}
	if providerPrefix, alias, ok := strings.Cut(trimmed, "::"); ok && providerPrefix != "" && alias != "" {
		return strings.TrimSpace(providerPrefix), strings.TrimSpace(alias), true
	}
	if providerPrefix, alias, ok := strings.Cut(trimmed, ":"); ok && providerPrefix != "" && alias != "" {
		return strings.TrimSpace(providerPrefix), strings.TrimSpace(alias), true
	}
	return "", "", false
}

func (r *Router) resolveDirectProviderModel(ctx context.Context, providerPrefix, upstreamModel string) (*store.PublicModel, error) {
	provider, err := r.store.ProviderByPrefix(ctx, providerPrefix)
	if err != nil {
		return nil, err
	}
	if !provider.Enabled {
		return nil, sql.ErrNoRows
	}
	credentials, err := r.store.Credentials(ctx, provider.ID)
	if err != nil {
		return nil, err
	}
	hasEnabled := false
	for _, credential := range credentials {
		if credential.Enabled {
			hasEnabled = true
			break
		}
	}
	if !hasEnabled {
		return nil, sql.ErrNoRows
	}
	requested := formatProviderModelSelector(provider.ID, upstreamModel)
	return &store.PublicModel{
		ID:           requested,
		DisplayName:  upstreamModel,
		Enabled:      true,
		Capabilities: r.registry.Capabilities(provider.Type),
	}, nil
}

func formatProviderModelSelector(providerID, upstreamModel string) string {
	return strings.TrimSpace(providerID) + ":" + strings.TrimSpace(upstreamModel)
}

// ProviderHealth runs a lightweight adapter health check for every enabled
// credential on the provider, then aggregates provider status from the results.
func (r *Router) ProviderHealth(ctx context.Context, providerID string) error {
	provider, err := r.store.Provider(ctx, providerID)
	if err != nil {
		return err
	}
	credentials, err := r.store.Credentials(ctx, providerID)
	if err != nil {
		return err
	}
	var firstErr error
	checked := 0
	for _, credential := range credentials {
		if !credential.Enabled {
			continue
		}
		checked++
		if err := r.checkCredentialHealth(ctx, *provider, credential); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if checked == 0 {
		err = r.registry.HealthCheck(ctx, *provider, store.Credential{})
		status := "healthy"
		message := ""
		if err != nil {
			status = "degraded"
			message = err.Error()
			if providers.Status(err) == 401 || providers.Status(err) == 403 {
				status = "auth_required"
			}
			firstErr = err
		}
		_ = r.store.SetProviderHealth(ctx, providerID, status, message, time.Now())
		return firstErr
	}
	_ = r.store.SyncProviderHealth(ctx, providerID)
	return firstErr
}

// CredentialHealth runs the same adapter health check used by ProviderHealth for
// one credential and refreshes the parent provider status.
func (r *Router) CredentialHealth(ctx context.Context, credentialID string) error {
	credential, err := r.store.CredentialByID(ctx, credentialID)
	if err != nil {
		return err
	}
	if !credential.Enabled {
		return fmt.Errorf("credential %s is disabled", credentialID)
	}
	provider, err := r.store.Provider(ctx, credential.ProviderID)
	if err != nil {
		return err
	}
	err = r.checkCredentialHealth(ctx, *provider, credential)
	_ = r.store.SyncProviderHealth(ctx, credential.ProviderID)
	return err
}

// SyncProviderHealth recomputes provider status from its enabled credentials.
func (r *Router) SyncProviderHealth(ctx context.Context, providerID string) error {
	return r.store.SyncProviderHealth(ctx, providerID)
}

func (r *Router) checkCredentialHealth(ctx context.Context, provider store.Provider, credential store.Credential) error {
	err := r.registry.HealthCheck(ctx, provider, credential)
	if credential.ID == "" {
		return err
	}
	if err != nil {
		status := providers.Status(err)
		if status == 401 || status == 403 {
			_ = r.store.MarkCredentialAuthRequired(ctx, credential.ID, providers.Code(err))
		} else {
			r.setCredentialCooldown(ctx, credential.ID, "", err)
		}
		return err
	}
	_ = r.store.ClearCooldown(ctx, credential.ID)
	return nil
}

// CredentialQuota fetches upstream quota windows for a credential.
func (r *Router) CredentialQuota(ctx context.Context, provider store.Provider, credential store.Credential) (providers.CredentialQuota, error) {
	quota, err := r.registry.CredentialQuota(ctx, provider, credential)
	if err != nil {
		return quota, err
	}
	if len(quota.Quotas) > 0 {
		depleted := providers.QuotaAtZero(quota)
		if changed, syncErr := r.store.SyncCredentialQuotaState(ctx, credential, depleted); syncErr == nil && changed {
			_ = r.store.SyncProviderHealth(ctx, provider.ID)
			if refreshed, refreshErr := r.store.CredentialByID(ctx, credential.ID); refreshErr == nil {
				enabled := refreshed.Enabled
				autoDisabled := store.QuotaAutoDisabled(refreshed.Metadata)
				quota.CredentialEnabled = &enabled
				quota.QuotaAutoDisabled = &autoDisabled
			}
		} else if len(quota.Quotas) > 0 {
			enabled := credential.Enabled
			autoDisabled := store.QuotaAutoDisabled(credential.Metadata)
			quota.CredentialEnabled = &enabled
			quota.QuotaAutoDisabled = &autoDisabled
		}
	}
	return quota, nil
}

// CodexResetCredits fetches Codex reset-credit inventory for a credential.
func (r *Router) CodexResetCredits(ctx context.Context, provider store.Provider, credential store.Credential) (providers.CodexResetCredits, error) {
	return r.registry.CodexResetCredits(ctx, provider, credential)
}

// ConsumeCodexResetCredit spends one Codex reset credit for a credential.
func (r *Router) ConsumeCodexResetCredit(ctx context.Context, provider store.Provider, credential store.Credential) (providers.CodexResetConsumeResult, error) {
	return r.registry.ConsumeCodexResetCredit(ctx, provider, credential, "")
}

func (r *Router) DiscoverCredentialModels(ctx context.Context, provider store.Provider, credential store.Credential) ([]providers.DiscoveredModel, error) {
	items, err := r.registry.DiscoverModels(ctx, provider, credential)
	if err != nil {
		if credential.ID != "" {
			r.setCredentialCooldown(ctx, credential.ID, "", err)
		}
		_ = r.store.SyncProviderHealth(ctx, provider.ID)
		return nil, err
	}
	if credential.ID != "" {
		_ = r.store.ClearCooldown(ctx, credential.ID)
	}
	for i := range items {
		if credential.ID != "" {
			items[i].CredentialIDs = []string{credential.ID}
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	_ = r.store.SyncProviderHealth(ctx, provider.ID)
	return items, nil
}

func (r *Router) ProviderDescriptor(ctx context.Context, providerID string) (providers.AdapterDescriptor, error) {
	provider, err := r.store.Provider(ctx, providerID)
	if err != nil {
		return providers.AdapterDescriptor{}, err
	}
	return r.registry.Describe(provider.Type)
}

func (r *Router) Execute(ctx context.Context, model store.PublicModel, request canonical.Request) (*Result, error) {
	if r.shouldExecuteFusion(model) && !fusionChildRequest(request) {
		return r.executeFusion(ctx, model, request)
	}
	start := time.Now()
	selections, err := r.selections(ctx, model, request)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for index, selection := range selections {
		selection.Attempt = index + 1
		request.PublicModelID = model.ID
		request.UpstreamModel = selection.Route.UpstreamModel
		prepared, prepareErr := r.prepareCredential(ctx, selection, false)
		if prepareErr != nil {
			lastErr = asCredentialError(prepareErr)
			_ = r.store.AddUsage(ctx, store.UsageEvent{RequestID: request.RequestID, ClientAPIKeyID: requestClientAPIKeyID(request), PublicModelID: model.ID, ProviderID: selection.Provider.ID, UpstreamModel: selection.Route.UpstreamModel, CredentialID: selection.Credential.ID, Attempt: selection.Attempt, Status: 401, LatencyMS: time.Since(start).Milliseconds(), ErrorCode: providers.Code(lastErr), CreatedAt: time.Now()})
			if disableFallback(request) {
				return nil, lastErr
			}
			continue
		}
		selection.Credential = prepared
		adapter, errAdapter := r.registry.Adapter(selection.Provider.Type)
		if errAdapter != nil {
			lastErr = errAdapter
			if disableFallback(request) {
				return nil, lastErr
			}
			continue
		}
		response, errExecute := adapter.Execute(ctx, selection.Provider, selection.Credential, request)
		if errExecute != nil && (providers.Status(errExecute) == 401 || providers.Status(errExecute) == 403) {
			if refreshed, refreshErr, ok := r.refreshAfterAuthError(ctx, selection); ok {
				selection.Credential = refreshed
				response, errExecute = adapter.Execute(ctx, selection.Provider, selection.Credential, request)
			} else if refreshErr != nil {
				errExecute = asCredentialError(refreshErr)
			}
		}
		if errExecute == nil {
			r.bindSession(model.ID, request.SessionID, selection.Credential.ID)
			r.clearSuccessfulCooldown(ctx, selection)
			if r.circuitBreakers != nil {
				r.circuitBreakers.RecordSuccess(selection.Provider.ID)
			}
			if r.arena != nil {
				r.arena.RecordOutcome(selection.Credential, true)
			}
			_ = r.store.AddUsage(ctx, store.UsageEvent{RequestID: request.RequestID, ClientAPIKeyID: requestClientAPIKeyID(request), PublicModelID: model.ID, ProviderID: selection.Provider.ID, UpstreamModel: selection.Route.UpstreamModel, CredentialID: selection.Credential.ID, Attempt: selection.Attempt, Status: 200, InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens, ReasoningTokens: response.Usage.ReasoningTokens, CachedTokens: response.Usage.CachedTokens, TokensSaved: requestTokensSaved(request), EstimatedCostUSD: r.estimateCost(response.Usage, selection), LatencyMS: time.Since(start).Milliseconds(), CreatedAt: time.Now()})
			if model.RewriteResponseModel {
				response.Model = model.ID
			}
			return &Result{Selection: selection, Response: response, CostUSD: r.estimateCost(response.Usage, selection)}, nil
		}
		lastErr = errExecute
		status := providers.Status(errExecute)
		code := providers.Code(errExecute)
		_ = r.store.AddUsage(ctx, store.UsageEvent{RequestID: request.RequestID, ClientAPIKeyID: requestClientAPIKeyID(request), PublicModelID: model.ID, ProviderID: selection.Provider.ID, UpstreamModel: selection.Route.UpstreamModel, CredentialID: selection.Credential.ID, Attempt: selection.Attempt, Status: status, LatencyMS: time.Since(start).Milliseconds(), ErrorCode: code, CreatedAt: time.Now()})
		fallback := shouldFallbackStatus(status)
		if fallback {
			r.setCredentialCooldown(ctx, selection.Credential.ID, selection.Route.UpstreamModel, errExecute)
		}
		if r.circuitBreakers != nil {
			r.circuitBreakers.RecordFailure(selection.Provider.ID, status)
		}
		if r.arena != nil {
			r.arena.RecordOutcome(selection.Credential, false)
		}
		if disableFallback(request) {
			return nil, errExecute
		}
		if fallback {
			continue
		}
		return nil, errExecute
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no available route for %s", model.ID)
	}
	return nil, lastErr
}

func (r *Router) ExecuteStream(ctx context.Context, model store.PublicModel, request canonical.Request) (*StreamResult, error) {
	start := time.Now()
	selections, err := r.selections(ctx, model, request)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for index, selection := range selections {
		selection.Attempt = index + 1
		request.PublicModelID = model.ID
		request.UpstreamModel = selection.Route.UpstreamModel
		request.Stream = true
		prepared, prepareErr := r.prepareCredential(ctx, selection, false)
		if prepareErr != nil {
			lastErr = asCredentialError(prepareErr)
			_ = r.store.AddUsage(ctx, store.UsageEvent{RequestID: request.RequestID, ClientAPIKeyID: requestClientAPIKeyID(request), PublicModelID: model.ID, ProviderID: selection.Provider.ID, UpstreamModel: selection.Route.UpstreamModel, CredentialID: selection.Credential.ID, Attempt: selection.Attempt, Status: 401, LatencyMS: time.Since(start).Milliseconds(), ErrorCode: providers.Code(lastErr), CreatedAt: time.Now()})
			if disableFallback(request) {
				return nil, lastErr
			}
			continue
		}
		selection.Credential = prepared
		adapter, errAdapter := r.registry.Adapter(selection.Provider.Type)
		if errAdapter != nil {
			lastErr = errAdapter
			if disableFallback(request) {
				return nil, lastErr
			}
			continue
		}
		if !r.acquireProviderStream(selection.Provider) {
			lastErr = &providers.ProviderError{Status: http.StatusTooManyRequests, Code: "provider_concurrency_limit", Message: fmt.Sprintf("provider %s concurrent stream limit is reached", selection.Provider.ID)}
			_ = r.store.AddUsage(ctx, store.UsageEvent{RequestID: request.RequestID, ClientAPIKeyID: requestClientAPIKeyID(request), PublicModelID: model.ID, ProviderID: selection.Provider.ID, UpstreamModel: selection.Route.UpstreamModel, CredentialID: selection.Credential.ID, Attempt: selection.Attempt, Status: http.StatusTooManyRequests, LatencyMS: time.Since(start).Milliseconds(), ErrorCode: providers.Code(lastErr), CreatedAt: time.Now()})
			if disableFallback(request) {
				return nil, lastErr
			}
			continue
		}
		events, errExecute := adapter.ExecuteStream(ctx, selection.Provider, selection.Credential, request)
		if errExecute != nil && (providers.Status(errExecute) == 401 || providers.Status(errExecute) == 403) {
			if refreshed, refreshErr, ok := r.refreshAfterAuthError(ctx, selection); ok {
				selection.Credential = refreshed
				events, errExecute = adapter.ExecuteStream(ctx, selection.Provider, selection.Credential, request)
			} else if refreshErr != nil {
				errExecute = asCredentialError(refreshErr)
			}
		}
		errExecute = validateStreamResult(events, errExecute)
		if errExecute != nil {
			r.releaseProviderStream(selection.Provider)
			lastErr = errExecute
			status, code := providers.Status(errExecute), providers.Code(errExecute)
			_ = r.store.AddUsage(ctx, store.UsageEvent{RequestID: request.RequestID, ClientAPIKeyID: requestClientAPIKeyID(request), PublicModelID: model.ID, ProviderID: selection.Provider.ID, UpstreamModel: selection.Route.UpstreamModel, CredentialID: selection.Credential.ID, Attempt: selection.Attempt, Status: status, LatencyMS: time.Since(start).Milliseconds(), ErrorCode: code, CreatedAt: time.Now()})
			fallback := shouldFallbackStatus(status)
			if fallback {
				r.setCredentialCooldown(ctx, selection.Credential.ID, selection.Route.UpstreamModel, errExecute)
			}
			if disableFallback(request) {
				return nil, errExecute
			}
			if fallback {
				continue
			}
			return nil, errExecute
		}
		r.bindSession(model.ID, request.SessionID, selection.Credential.ID)
		wrapped := r.wrapEvents(ctx, model, selection, request, start, events, func() { r.releaseProviderStream(selection.Provider) })
		return &StreamResult{Selection: selection, Events: wrapped}, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no available route for %s", model.ID)
	}
	return nil, lastErr
}

func validateStreamResult(events <-chan canonical.Event, err error) error {
	if err == nil && events == nil {
		return &providers.ProviderError{Status: http.StatusBadGateway, Code: "provider_protocol_error", Message: "provider returned no stream"}
	}
	return err
}

func (r *Router) Proxy(ctx context.Context, model store.PublicModel, requestID, path string, body []byte, contentType string) (*RawResult, error) {
	return r.ProxyWithOptions(ctx, model, requestID, path, body, contentType, RawProxyOptions{RetryNetworkErrors: true})
}

// RefreshMediaJob asks the originating provider for the current status of an
// asynchronous media job. It never creates a new job and therefore does not
// require an idempotency key.
func (r *Router) RefreshMediaJob(ctx context.Context, job store.MediaJob) (*RawResult, error) {
	provider, err := r.store.Provider(ctx, job.ProviderID)
	if err != nil {
		return nil, err
	}
	credentials, err := r.store.Credentials(ctx, job.ProviderID)
	if err != nil {
		return nil, err
	}
	var credential store.Credential
	for _, candidate := range credentials {
		if candidate.ID == job.CredentialID {
			credential = candidate
			break
		}
	}
	if credential.ID == "" {
		return nil, fmt.Errorf("media job credential %q is unavailable", job.CredentialID)
	}
	model, err := r.store.ResolveModel(ctx, job.PublicModelID, "")
	if err != nil {
		return nil, err
	}
	selection := Selection{Model: *model, Provider: *provider, Credential: credential, Route: store.RouteTarget{PublicModelID: model.ID, ProviderID: provider.ID, UpstreamModel: job.UpstreamID}, Attempt: 1}
	if _, err = r.assignProxy(ctx, &selection); err != nil {
		return nil, err
	}
	adapter, err := r.registry.Adapter(provider.Type)
	if err != nil {
		return nil, err
	}
	rawAdapter, ok := adapter.(providers.RawProxyAdapter)
	if !ok {
		return nil, fmt.Errorf("provider %s does not support media status polling", provider.ID)
	}
	id := job.UpstreamID
	if id == "" {
		id = job.ID
	}
	raw, err := rawAdapter.Proxy(ctx, *provider, selection.Credential, providers.RawRequest{Method: http.MethodGet, Path: "/v1/videos/" + url.PathEscape(id), Headers: http.Header{"X-Request-ID": []string{job.ID}}})
	if err != nil {
		return nil, err
	}
	if model.RewriteResponseModel && strings.Contains(strings.ToLower(raw.ContentType), "json") {
		raw.Body = rewriteResponseModel(raw.Body, model.ID)
	}
	return &RawResult{Selection: selection, Response: raw}, nil
}

func (r *Router) ProxyWithOptions(ctx context.Context, model store.PublicModel, requestID, path string, body []byte, contentType string, options RawProxyOptions) (*RawResult, error) {
	start := time.Now()
	rawRequestContext := canonical.Request{RequestID: requestID, PublicModelID: model.ID, Source: canonical.ProtocolOpenAI, Metadata: map[string]any{"capability": rawCapability(path), "path": path, "client_api_key_id": options.ClientAPIKeyID, "team": options.Team}}
	if options.PinnedProvider != "" {
		rawRequestContext.Metadata["pinned_provider"] = options.PinnedProvider
	}
	if options.DisableFallback {
		rawRequestContext.Metadata["disable_fallback"] = true
	}
	selections, err := r.selections(ctx, model, rawRequestContext)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for index, selection := range selections {
		selection.Attempt = index + 1
		prepared, prepareErr := r.prepareCredential(ctx, selection, false)
		if prepareErr != nil {
			lastErr = asCredentialError(prepareErr)
			_ = r.store.AddUsage(ctx, store.UsageEvent{RequestID: requestID, ClientAPIKeyID: options.ClientAPIKeyID, PublicModelID: model.ID, ProviderID: selection.Provider.ID, UpstreamModel: selection.Route.UpstreamModel, CredentialID: selection.Credential.ID, Attempt: selection.Attempt, Status: 401, LatencyMS: time.Since(start).Milliseconds(), ErrorCode: providers.Code(lastErr), CreatedAt: time.Now()})
			if options.DisableFallback {
				return nil, lastErr
			}
			continue
		}
		selection.Credential = prepared
		adapter, errAdapter := r.registry.Adapter(selection.Provider.Type)
		if errAdapter != nil {
			lastErr = errAdapter
			if options.DisableFallback {
				return nil, lastErr
			}
			continue
		}
		rawAdapter, ok := adapter.(providers.RawProxyAdapter)
		if !ok {
			lastErr = fmt.Errorf("provider %s does not support raw endpoint %s", selection.Provider.ID, path)
			if options.DisableFallback {
				return nil, lastErr
			}
			continue
		}
		requestBody := rewriteRequestModel(body, contentType, selection.Route.UpstreamModel)
		rawRequest := providers.RawRequest{Method: options.Method, Path: path, Body: requestBody, ContentType: contentType, Headers: options.Headers.Clone()}
		raw, errProxy := rawAdapter.Proxy(ctx, selection.Provider, selection.Credential, rawRequest)
		if errProxy != nil && (providers.Status(errProxy) == 401 || providers.Status(errProxy) == 403) {
			if refreshed, refreshErr, ok := r.refreshAfterAuthError(ctx, selection); ok {
				selection.Credential = refreshed
				requestBody = rewriteRequestModel(body, contentType, selection.Route.UpstreamModel)
				rawRequest.Body = requestBody
				raw, errProxy = rawAdapter.Proxy(ctx, selection.Provider, selection.Credential, rawRequest)
			} else if refreshErr != nil {
				errProxy = asCredentialError(refreshErr)
			}
		}
		if errProxy == nil {
			if model.RewriteResponseModel && strings.Contains(strings.ToLower(raw.ContentType), "json") {
				raw.Body = rewriteResponseModel(raw.Body, model.ID)
			}
			r.clearSuccessfulCooldown(ctx, selection)
			_ = r.store.AddUsage(ctx, store.UsageEvent{RequestID: requestID, ClientAPIKeyID: options.ClientAPIKeyID, PublicModelID: model.ID, ProviderID: selection.Provider.ID, UpstreamModel: selection.Route.UpstreamModel, CredentialID: selection.Credential.ID, Attempt: selection.Attempt, Status: raw.Status, EstimatedCostUSD: r.estimateCost(canonical.Usage{}, selection), LatencyMS: time.Since(start).Milliseconds(), CreatedAt: time.Now()})
			return &RawResult{Selection: selection, Response: raw}, nil
		}
		lastErr = errProxy
		status, code := providers.Status(errProxy), providers.Code(errProxy)
		_ = r.store.AddUsage(ctx, store.UsageEvent{RequestID: requestID, ClientAPIKeyID: options.ClientAPIKeyID, PublicModelID: model.ID, ProviderID: selection.Provider.ID, UpstreamModel: selection.Route.UpstreamModel, CredentialID: selection.Credential.ID, Attempt: selection.Attempt, Status: status, LatencyMS: time.Since(start).Milliseconds(), ErrorCode: code, CreatedAt: time.Now()})
		if status == 0 && !options.RetryNetworkErrors {
			if options.DisableFallback {
				return nil, errProxy
			}
			return nil, &providers.ProviderError{Status: http.StatusBadGateway, Code: "ambiguous_upstream_failure", Message: "upstream connection failed after dispatch; request was not retried without an idempotency key", Err: errProxy}
		}
		fallback := shouldFallbackStatus(status)
		if fallback {
			r.setCredentialCooldown(ctx, selection.Credential.ID, selection.Route.UpstreamModel, errProxy)
		}
		if options.DisableFallback {
			return nil, errProxy
		}
		if fallback {
			continue
		}
		return nil, errProxy
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no available route for %s", model.ID)
	}
	return nil, lastErr
}

func rewriteRequestModel(body []byte, contentType, upstreamModel string) []byte {
	mediaType, params, _ := mime.ParseMediaType(contentType)
	if strings.HasPrefix(mediaType, "multipart/") {
		return rewriteMultipartModel(body, params["boundary"], upstreamModel)
	}
	if !strings.Contains(strings.ToLower(contentType), "json") {
		return body
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return body
	}
	payload["model"] = upstreamModel
	data, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return data
}

func rewriteMultipartModel(body []byte, boundary, upstreamModel string) []byte {
	if boundary == "" {
		return body
	}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	var output bytes.Buffer
	writer := multipart.NewWriter(&output)
	if writer.SetBoundary(boundary) != nil {
		return body
	}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return body
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return body
		}
		target, err := writer.CreatePart(part.Header)
		if err != nil {
			return body
		}
		if part.FormName() == "model" {
			data = []byte(upstreamModel)
		}
		if _, err = target.Write(data); err != nil {
			return body
		}
	}
	if writer.Close() != nil {
		return body
	}
	return output.Bytes()
}

func rewriteResponseModel(body []byte, publicModelID string) []byte {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return body
	}
	if _, ok := payload["model"]; ok {
		payload["model"] = publicModelID
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return data
}

func requestTokensSaved(request canonical.Request) int {
	if request.Metadata == nil {
		return 0
	}
	switch value := request.Metadata["tokens_saved"].(type) {
	case int:
		if value > 0 {
			return value
		}
	case int8:
		return clampSignedTokenCount(int64(value))
	case int16:
		return clampSignedTokenCount(int64(value))
	case int32:
		return clampSignedTokenCount(int64(value))
	case int64:
		return clampSignedTokenCount(value)
	case uint:
		return clampUnsignedTokenCount(uint64(value))
	case uint8:
		return clampUnsignedTokenCount(uint64(value))
	case uint16:
		return clampUnsignedTokenCount(uint64(value))
	case uint32:
		return clampUnsignedTokenCount(uint64(value))
	case uint64:
		return clampUnsignedTokenCount(value)
	case float32:
		return clampFloatTokenCount(float64(value))
	case float64:
		return clampFloatTokenCount(value)
	case json.Number:
		if parsed, err := value.Int64(); err == nil {
			return clampSignedTokenCount(parsed)
		}
		if parsed, err := value.Float64(); err == nil {
			return clampFloatTokenCount(parsed)
		}
	}
	return 0
}

func clampSignedTokenCount(value int64) int {
	if value <= 0 {
		return 0
	}
	return clampUnsignedTokenCount(uint64(value))
}

func clampUnsignedTokenCount(value uint64) int {
	maximum := uint64(^uint(0) >> 1)
	if value > maximum {
		return int(maximum)
	}
	return int(value)
}

func clampFloatTokenCount(value float64) int {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return 0
	}
	maximum := int(^uint(0) >> 1)
	if value >= float64(maximum) {
		return maximum
	}
	return int(value)
}

func requestClientAPIKeyID(request canonical.Request) string {
	if request.Metadata == nil {
		return ""
	}
	value, exists := request.Metadata["client_api_key_id"]
	if !exists || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func (r *Router) wrapEvents(ctx context.Context, model store.PublicModel, selection Selection, request canonical.Request, start time.Time, input <-chan canonical.Event, release func()) <-chan canonical.Event {
	out := make(chan canonical.Event, 16)
	go func() {
		defer close(out)
		if release != nil {
			defer release()
		}
		status := 200
		terminal := false
		errorCode := ""
		var usage canonical.Usage
	stream:
		for event := range input {
			if ctx.Err() != nil {
				status = 499
				errorCode = "client_canceled"
				break
			}
			if model.RewriteResponseModel {
				event.Model = model.ID
			}
			if event.Type == canonical.EventResponsesSSE {
				switch event.SSEEvent {
				case "response.completed", "response.incomplete":
					terminal = true
					usage = mergeCanonicalUsage(usage, providers.UsageFromResponsesSSEData(event.SSEData))
				case "response.failed":
					terminal = true
					status = http.StatusBadGateway
					errorCode = "upstream_response_failed"
				}
			}
			if event.Usage != nil {
				usage = mergeCanonicalUsage(usage, *event.Usage)
			}
			if event.Type == canonical.EventError {
				status = providers.Status(event.Err)
				if status < http.StatusBadRequest {
					status = http.StatusBadGateway
				}
				errorCode = providers.Code(event.Err)
				terminal = true
			} else if event.Type == canonical.EventMessageEnd {
				terminal = true
			}
			select {
			case out <- event:
			case <-ctx.Done():
				status = 499
				errorCode = "client_canceled"
				break stream
			}
			if terminal {
				break
			}
		}
		if status == 200 && !terminal {
			if ctx.Err() != nil {
				status = 499
				errorCode = "client_canceled"
			} else {
				status = http.StatusBadGateway
				errorCode = "upstream_stream_incomplete"
				streamErr := &providers.ProviderError{Status: http.StatusBadGateway, Code: "upstream_stream_incomplete", Message: "upstream stream ended before a terminal event"}
				select {
				case out <- canonical.Event{Type: canonical.EventError, Err: streamErr}:
				case <-ctx.Done():
					status = 499
				}
			}
		}
		if status == 200 {
			r.clearSuccessfulCooldown(context.Background(), selection)
		}
		_ = r.store.AddUsage(context.Background(), store.UsageEvent{RequestID: request.RequestID, ClientAPIKeyID: requestClientAPIKeyID(request), PublicModelID: model.ID, ProviderID: selection.Provider.ID, UpstreamModel: selection.Route.UpstreamModel, CredentialID: selection.Credential.ID, Attempt: selection.Attempt, Status: status, InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, ReasoningTokens: usage.ReasoningTokens, CachedTokens: usage.CachedTokens, TokensSaved: requestTokensSaved(request), EstimatedCostUSD: r.estimateCost(usage, selection), LatencyMS: time.Since(start).Milliseconds(), ErrorCode: errorCode, CreatedAt: time.Now()})
	}()
	return out
}

func mergeCanonicalUsage(current, update canonical.Usage) canonical.Usage {
	if update.InputTokens != 0 {
		current.InputTokens = update.InputTokens
	}
	if update.OutputTokens != 0 {
		current.OutputTokens = update.OutputTokens
	}
	if update.ReasoningTokens != 0 {
		current.ReasoningTokens = update.ReasoningTokens
	}
	if update.CachedTokens != 0 {
		current.CachedTokens = update.CachedTokens
	}
	return current
}

func (r *Router) acquireProviderStream(provider store.Provider) bool {
	limit := provider.Limits.ConcurrentStreams
	if limit <= 0 {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.providerStreams[provider.ID] >= limit {
		return false
	}
	r.providerStreams[provider.ID]++
	return true
}

func (r *Router) releaseProviderStream(provider store.Provider) {
	if provider.Limits.ConcurrentStreams <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.providerStreams[provider.ID] > 1 {
		r.providerStreams[provider.ID]--
	} else {
		delete(r.providerStreams, provider.ID)
	}
}

func (r *Router) estimateCost(usage canonical.Usage, selection Selection) float64 {
	if pricing.Configured(selection.Route.Pricing) {
		return pricing.EstimateFromConfig(usage, selection.Route.Pricing)
	}
	r.mu.Lock()
	catalog := r.pricing
	r.mu.Unlock()
	if catalog != nil {
		if rates, ok := catalog.Lookup(selection.Provider, selection.Route.UpstreamModel); ok {
			return pricing.EstimateCost(usage, rates)
		}
	}
	return 0
}

func (r *Router) prepareCredential(ctx context.Context, selection Selection, force bool) (store.Credential, error) {
	r.mu.Lock()
	refresher := r.refresher
	r.mu.Unlock()
	credential := selection.Credential
	if refresher == nil {
		return credential, nil
	}
	switch {
	case credential.AuthType == "oauth", credential.AuthType == "service_account", selection.Provider.Type == "copilot":
		updated, err := refresher.EnsureValid(ctx, selection.Provider, credential, force)
		if err == nil {
			r.recordActivity(refresher, selection.Provider, updated)
		}
		return updated, err
	default:
		return credential, nil
	}
}

func (r *Router) recordActivity(refresher CredentialRefresher, provider store.Provider, credential store.Credential) {
	if recorder, ok := refresher.(interface {
		RecordActivity(store.Provider, store.Credential)
	}); ok {
		recorder.RecordActivity(provider, credential)
	}
}

func (r *Router) refreshAfterAuthError(ctx context.Context, selection Selection) (store.Credential, error, bool) {
	if selection.Credential.AuthType != "oauth" {
		return selection.Credential, nil, false
	}
	credential, err := r.prepareCredential(ctx, selection, true)
	if err != nil {
		return selection.Credential, err, false
	}
	return credential, nil, true
}

func asCredentialError(err error) error {
	if err == nil {
		return nil
	}
	code := "oauth_error"
	var coded interface{ Code() string }
	if errors.As(err, &coded) && coded.Code() != "" {
		code = coded.Code()
	}
	return &providers.ProviderError{Status: http.StatusUnauthorized, Code: code, Message: err.Error(), Err: err}
}

func (r *Router) selections(ctx context.Context, model store.PublicModel, request canonical.Request) ([]Selection, error) {
	// Provider selection for a public model ID is driven by route_targets configured in PPM
	// (Provider Priority Manager). Order, enablement, and fallback all come from that table.
	routes, err := r.routesForModel(ctx, model)
	if err != nil {
		return nil, err
	}
	var selections []Selection
	policyLimited := false
	modelCooldownLimited := false
	now := time.Now()
	for _, route := range routes {
		if !routeMatches(route, request) {
			continue
		}
		provider, errProvider := r.store.Provider(ctx, route.ProviderID)
		if errProvider != nil || !provider.Enabled || provider.Status == "disabled" || provider.Status == "auth_required" || provider.Status == "cooldown" {
			continue
		}
		if r.circuitBreakers != nil && !r.circuitBreakers.CanExecute(provider.ID) {
			continue
		}
		if pin := pinnedProvider(request); pin != "" && !providerMatchesPin(*provider, pin) {
			continue
		}
		if !r.providerSupports(*provider, request) {
			continue
		}
		withinPolicy, policyErr := r.providerWithinPolicy(ctx, *provider, request)
		if policyErr != nil {
			return nil, policyErr
		}
		if !withinPolicy {
			policyLimited = true
			continue
		}
		credentials, errCredentials := r.store.Credentials(ctx, provider.ID)
		if errCredentials != nil {
			return nil, errCredentials
		}
		eligible := store.EligibleCredentials(credentials, now)
		beforeModelCooldown := len(eligible)
		eligible = r.filterModelCooldowns(ctx, eligible, route.UpstreamModel, now)
		if beforeModelCooldown > 0 && len(eligible) == 0 {
			modelCooldownLimited = true
		}
		eligible, errOrder := r.orderCredentials(ctx, provider.ID, route.ID, route.Priority, eligible, taskHintFromRequest(request.Metadata))
		if errOrder != nil {
			return nil, errOrder
		}
		for _, credential := range eligible {
			selection := Selection{Model: model, Route: route, Provider: *provider, Credential: credential}
			proxyAvailable, proxyErr := r.assignProxy(ctx, &selection)
			if proxyErr != nil {
				return nil, proxyErr
			}
			if !proxyAvailable {
				continue
			}
			selections = append(selections, selection)
		}
	}
	if len(selections) == 0 {
		if pin := pinnedProvider(request); pin != "" {
			return nil, &providers.ProviderError{Status: http.StatusBadGateway, Code: "provider_unavailable", Message: fmt.Sprintf("no available credential for provider %s on %s", pin, model.ID)}
		}
		if policyLimited {
			return nil, &providers.ProviderError{Status: http.StatusTooManyRequests, Code: "provider_limit_exceeded", Message: "all eligible providers are outside configured resource or budget limits"}
		}
		if modelCooldownLimited {
			return nil, &providers.ProviderError{
				Status:  http.StatusTooManyRequests,
				Code:    "upstream_rate_limited",
				Message: fmt.Sprintf("all credentials are temporarily rate-limited for %s; retry shortly or use another model", model.ID),
			}
		}
		return nil, fmt.Errorf("no_available_credential: %s", model.ID)
	}
	selections = r.preferSession(model.ID, request.SessionID, selections)
	return selections, nil
}

func (r *Router) providerWithinPolicy(ctx context.Context, provider store.Provider, request canonical.Request) (bool, error) {
	limits := provider.Limits
	if limits.MaxOutputTokens > 0 && request.MaxTokens > limits.MaxOutputTokens {
		return false, nil
	}
	if limits.MaxInputBytes > 0 {
		encoded, _ := json.Marshal(request.Raw)
		if int64(len(encoded)) > limits.MaxInputBytes {
			return false, nil
		}
	}
	now := time.Now().UTC()
	if limits.RequestsPerMinute > 0 {
		count, err := r.store.ProviderRequestCountSince(ctx, provider.ID, now.Add(-time.Minute))
		if err != nil {
			return false, err
		}
		if count >= limits.RequestsPerMinute {
			return false, nil
		}
	}
	if limits.BudgetUSDPerDay > 0 {
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		cost, err := r.store.ProviderEstimatedCostSince(ctx, provider.ID, start)
		if err != nil {
			return false, err
		}
		if cost >= limits.BudgetUSDPerDay {
			return false, nil
		}
	}
	return true, nil
}

func (r *Router) providerSupports(provider store.Provider, request canonical.Request) bool {
	capability := "text"
	if request.Metadata != nil {
		if value, exists := request.Metadata["capability"]; exists && strings.TrimSpace(fmt.Sprint(value)) != "" {
			capability = strings.TrimSpace(fmt.Sprint(value))
		}
	}
	for _, supported := range r.registry.Capabilities(provider.Type) {
		if supported == "*" || supported == capability {
			return true
		}
	}
	return false
}

// routesForModel loads the provider priority chain saved by PPM (route_targets) for a public model ID.
// This is the runtime source of truth for which provider handles a model request.
func (r *Router) routesForModel(ctx context.Context, model store.PublicModel) ([]store.RouteTarget, error) {
	routeModelID := model.ID
	if model.Limits != nil {
		if source, ok := model.Limits["_auto_source_model"].(string); ok && strings.TrimSpace(source) != "" {
			routeModelID = source
		}
	}
	if len(model.ComboItems) == 0 {
		routes, err := r.store.Routes(ctx, routeModelID)
		if err != nil {
			return nil, err
		}
		if len(routes) > 0 {
			return r.rotateRoutes(model.ID, routes), nil
		}
		if providerPrefix, upstreamModel, ok := splitProviderModelSelector(model.ID); ok {
			provider, providerErr := r.store.ProviderByPrefix(ctx, providerPrefix)
			if providerErr == nil && provider.Enabled {
				return []store.RouteTarget{{
					ID:            "direct:" + model.ID,
					PublicModelID: model.ID,
					ProviderID:    provider.ID,
					UpstreamModel: upstreamModel,
					Priority:      100,
					Weight:        1,
					Enabled:       true,
				}}, nil
			}
		}
		return nil, nil
	}
	var result []store.RouteTarget
	for index, comboItem := range model.ComboItems {
		routes, err := r.store.Routes(ctx, comboItem.PublicModelID)
		if err != nil {
			return nil, err
		}
		filtered := make([]store.RouteTarget, 0, len(routes))
		for _, route := range routes {
			if comboItem.RouteTargetID == "" || route.ID == comboItem.RouteTargetID {
				filtered = append(filtered, route)
			}
		}
		if len(filtered) == 0 {
			if providerPrefix, upstreamModel, ok := splitProviderModelSelector(comboItem.PublicModelID); ok {
				provider, providerErr := r.store.ProviderByPrefix(ctx, providerPrefix)
				if providerErr == nil && provider.Enabled {
					filtered = []store.RouteTarget{{
						ID:            "direct:" + comboItem.PublicModelID,
						PublicModelID: comboItem.PublicModelID,
						ProviderID:    provider.ID,
						UpstreamModel: upstreamModel,
						Priority:      100,
						Weight:        1,
						Enabled:       true,
					}}
				}
			}
		}
		filtered = r.rotateRoutes(comboItem.PublicModelID, filtered)
		basePriority := (len(model.ComboItems) - index) * 1_000_000
		for _, route := range filtered {
			route.Priority = basePriority + route.Priority
			result = append(result, route)
		}
	}
	return result, nil
}

func (r *Router) assignProxy(ctx context.Context, selection *Selection) (bool, error) {
	poolIDs := selection.Credential.ProxyPoolIDs
	if len(poolIDs) == 0 {
		poolIDs = selection.Provider.ProxyPoolIDs
	}
	if len(poolIDs) == 0 {
		return true, nil
	}
	key := "proxy:" + selection.Provider.ID + ":" + selection.Credential.ID
	r.mu.Lock()
	start := r.rotation[key] % len(poolIDs)
	r.rotation[key] = (start + 1) % len(poolIDs)
	r.mu.Unlock()
	for offset := 0; offset < len(poolIDs); offset++ {
		poolID := poolIDs[(start+offset)%len(poolIDs)]
		pool, err := r.store.ProxyPool(ctx, poolID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, err
		}
		if !pool.Enabled {
			continue
		}
		selection.Credential.ProxyURL = pool.URL
		return true, nil
	}
	return false, nil
}

func (r *Router) rotateRoutes(modelID string, routes []store.RouteTarget) []store.RouteTarget {
	if len(routes) < 2 {
		return routes
	}
	r.mu.Lock()
	strategy := r.strategy
	r.mu.Unlock()
	if strategy == StrategyFillFirst {
		return routes
	}
	result := make([]store.RouteTarget, 0, len(routes))
	for start := 0; start < len(routes); {
		end := start + 1
		for end < len(routes) && routes[end].Priority == routes[start].Priority {
			end++
		}
		group := append([]store.RouteTarget(nil), routes[start:end]...)
		if len(group) > 1 {
			key := fmt.Sprintf("model:%s:%d", modelID, group[0].Priority)
			r.mu.Lock()
			total := 0
			for _, route := range group {
				weight := route.Weight
				if weight <= 0 {
					weight = 1
				}
				total += weight
			}
			slot := r.rotation[key] % total
			r.rotation[key] = (slot + 1) % total
			r.mu.Unlock()
			index := 0
			for candidateIndex, route := range group {
				weight := route.Weight
				if weight <= 0 {
					weight = 1
				}
				if slot < weight {
					index = candidateIndex
					break
				}
				slot -= weight
			}
			group = append(group[index:], group[:index]...)
		}
		result = append(result, group...)
		start = end
	}
	return result
}

func (r *Router) preferSession(modelID, sessionID string, selections []Selection) []Selection {
	if sessionID == "" || len(selections) < 2 {
		return selections
	}
	key := modelID + ":" + sessionID
	r.mu.Lock()
	enabled := r.sessionAffinity
	binding, exists := r.sessions[key]
	if exists && time.Now().After(binding.ExpiresAt) {
		delete(r.sessions, key)
		exists = false
	}
	r.mu.Unlock()
	if !enabled || !exists {
		return selections
	}
	for index, selection := range selections {
		if selection.Credential.ID == binding.CredentialID {
			result := make([]Selection, 0, len(selections))
			result = append(result, selection)
			result = append(result, selections[:index]...)
			result = append(result, selections[index+1:]...)
			return result
		}
	}
	return selections
}

func (r *Router) bindSession(modelID, sessionID, credentialID string) {
	if sessionID == "" || credentialID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.sessionAffinity {
		return
	}
	r.sessions[modelID+":"+sessionID] = sessionBinding{CredentialID: credentialID, ExpiresAt: time.Now().Add(r.sessionTTL)}
}

func routeMatches(route store.RouteTarget, request canonical.Request) bool {
	conditions := route.Conditions
	if len(conditions) == 0 {
		return true
	}
	if expected, exists := conditions["protocol"]; exists && !conditionContains(expected, string(request.Source)) {
		return false
	}
	if expected, exists := conditions["capability"]; exists {
		capability := ""
		if request.Metadata != nil {
			capability = fmt.Sprint(request.Metadata["capability"])
		}
		if !conditionContains(expected, capability) {
			return false
		}
	}
	if expected, exists := conditions["path"]; exists {
		path := ""
		if request.Metadata != nil {
			path = fmt.Sprint(request.Metadata["path"])
		}
		if !conditionContains(expected, path) {
			return false
		}
	}
	if expected, exists := conditions["stream"]; exists {
		if value, ok := expected.(bool); ok && value != request.Stream {
			return false
		}
	}
	if expected, exists := conditions["session_required"]; exists {
		if value, ok := expected.(bool); ok && value != (request.SessionID != "") {
			return false
		}
	}
	if maximum := conditionInt(conditions["max_tokens"]); maximum > 0 && request.MaxTokens > maximum {
		return false
	}
	if minimum := conditionInt(conditions["min_tokens"]); minimum > 0 && request.MaxTokens < minimum {
		return false
	}
	if expected, exists := conditions["api_key_id"]; exists {
		apiKeyID := ""
		if request.Metadata != nil {
			apiKeyID = fmt.Sprint(request.Metadata["client_api_key_id"])
		}
		if !conditionContains(expected, apiKeyID) {
			return false
		}
	}
	if expected, exists := conditions["team"]; exists {
		team := ""
		if request.Metadata != nil {
			team = fmt.Sprint(request.Metadata["team"])
		}
		if !conditionContains(expected, team) {
			return false
		}
	}
	return true
}

func conditionContains(expected any, actual string) bool {
	switch value := expected.(type) {
	case string:
		return value == "*" || value == actual
	case []string:
		for _, item := range value {
			if item == "*" || item == actual {
				return true
			}
		}
	case []any:
		for _, item := range value {
			if text := fmt.Sprint(item); text == "*" || text == actual {
				return true
			}
		}
	}
	return false
}

func conditionInt(value any) int {
	switch item := value.(type) {
	case int:
		return item
	case float64:
		return int(item)
	case json.Number:
		parsed, _ := item.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func rawCapability(path string) string {
	switch {
	case path == "/v1/embeddings":
		return "embedding"
	case strings.HasPrefix(path, "/v1/images/"):
		return "image-output"
	case path == "/v1/audio/speech":
		return "tts"
	case path == "/v1/audio/transcriptions":
		return "stt"
	case strings.HasPrefix(path, "/v1/videos"):
		return "video-output"
	case path == "/v1/search":
		return "web-search"
	default:
		return ""
	}
}

func pinnedProvider(request canonical.Request) string {
	if request.Metadata == nil {
		return ""
	}
	value, ok := request.Metadata["pinned_provider"].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func providerMatchesPin(provider store.Provider, pin string) bool {
	pin = strings.TrimSpace(pin)
	if pin == "" {
		return true
	}
	if strings.EqualFold(provider.ID, pin) || strings.EqualFold(provider.Name, pin) || strings.EqualFold(provider.Type, pin) {
		return true
	}
	if strings.EqualFold(pin, "chatgpt") && provider.Type == "codex" {
		return true
	}
	return false
}

func disableFallback(request canonical.Request) bool {
	if request.Metadata == nil {
		return false
	}
	value, ok := request.Metadata["disable_fallback"].(bool)
	return ok && value
}

func shouldFallbackStatus(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusPaymentRequired || status == http.StatusForbidden || status == http.StatusNotFound || store.IsRetryableStatus(status)
}

func (r *Router) clearSuccessfulCooldown(ctx context.Context, selection Selection) {
	if selection.Credential.ID == "" {
		return
	}
	// Successful dispatch restores account health and clears only the attempted
	// upstream model; unrelated model backoffs on this account remain active.
	_ = r.store.ClearCredentialCooldown(ctx, selection.Credential.ID)
	_ = r.store.ClearModelCooldown(ctx, selection.Credential.ID, selection.Route.UpstreamModel)
}

func (r *Router) setCredentialCooldown(ctx context.Context, credentialID, upstreamModel string, err error) {
	if credentialID == "" || err == nil {
		return
	}
	status := providers.Status(err)
	count := 0
	if upstreamModel != "" {
		count = r.store.ModelCooldownCount(ctx, credentialID, upstreamModel)
	}
	until := credentialCooldownUntil(time.Now(), r.cooldowns, err, count)
	code := providers.Code(err)
	message := err.Error()
	if r.cooldowns.accountLevel(status) || upstreamModel == "" {
		_ = r.store.SetCooldown(ctx, credentialID, code, message, until)
		return
	}
	_ = r.store.SetModelCooldown(ctx, credentialID, upstreamModel, code, message, until, status, count+1)
}

func (r *Router) filterModelCooldowns(ctx context.Context, credentials []store.Credential, upstreamModel string, now time.Time) []store.Credential {
	if upstreamModel == "" {
		return credentials
	}
	filtered := make([]store.Credential, 0, len(credentials))
	for _, credential := range credentials {
		until, err := r.store.ModelCooldownUntil(ctx, credential.ID, upstreamModel, now)
		if err != nil || until.IsZero() {
			filtered = append(filtered, credential)
		}
	}
	return filtered
}
