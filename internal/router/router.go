package router

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/tproxy/tproxy/internal/pricing"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/store"
)

type Router struct {
	store           *store.Store
	registry        *providers.Registry
	refresher       CredentialRefresher
	mu              sync.Mutex
	rotation        map[string]int
	cooldown        time.Duration
	cooldowns       CooldownSettings
	allowUpstream   bool
	strategy        string
	sessionAffinity bool
	sessionTTL      time.Duration
	sessions        map[string]sessionBinding
	providerStreams map[string]int
	pricing          *pricing.Catalog
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
	ClientAPIKeyID     string
	Team               string
	PinnedProvider     string
}

func New(dataStore *store.Store, registry *providers.Registry) *Router {
	return &Router{store: dataStore, registry: registry, rotation: make(map[string]int), cooldown: time.Minute, cooldowns: CooldownSettingsFromConfig(config.CooldownConfig{}), strategy: "round-robin", sessionTTL: time.Hour, sessions: make(map[string]sessionBinding), providerStreams: make(map[string]int)}
}

func (r *Router) SetCredentialRefresher(refresher CredentialRefresher) {
	r.mu.Lock()
	r.refresher = refresher
	r.mu.Unlock()
}

func (r *Router) SetAllowUpstreamModels(enabled bool) { r.allowUpstream = enabled }

func (r *Router) SetPricingCatalog(catalog *pricing.Catalog) {
	r.mu.Lock()
	r.pricing = catalog
	r.mu.Unlock()
}

func (r *Router) ConfigureRouting(cfg config.RoutingConfig) {
	strategy := cfg.Strategy
	if strategy == "" {
		strategy = "round-robin"
	}
	ttl, err := time.ParseDuration(cfg.SessionAffinityTTL)
	if err != nil || ttl <= 0 {
		ttl = time.Hour
	}
	r.mu.Lock()
	r.strategy = strategy
	r.sessionAffinity = cfg.SessionAffinity
	r.sessionTTL = ttl
	r.cooldowns = CooldownSettingsFromConfig(cfg.Cooldown)
	r.cooldown = r.cooldowns.Fallback
	r.mu.Unlock()
}

func (r *Router) Resolve(ctx context.Context, requested string, apiKey *store.APIKey) (*store.PublicModel, error) {
	apiKeyID := ""
	teamID := ""
	if apiKey != nil {
		apiKeyID = apiKey.ID
		teamID = apiKey.Policy.Team
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
		if errors.Is(err, sql.ErrNoRows) && r.allowUpstream {
			model, err = r.store.ResolveUpstreamModel(ctx, requested)
		}
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
	return r.registry.CredentialQuota(ctx, provider, credential)
}

// CodexResetCredits fetches Codex reset-credit inventory for a credential.
func (r *Router) CodexResetCredits(ctx context.Context, provider store.Provider, credential store.Credential) (providers.CodexResetCredits, error) {
	return r.registry.CodexResetCredits(ctx, provider, credential)
}

// ConsumeCodexResetCredit spends one Codex reset credit for a credential.
func (r *Router) ConsumeCodexResetCredit(ctx context.Context, provider store.Provider, credential store.Credential) (providers.CodexResetConsumeResult, error) {
	return r.registry.ConsumeCodexResetCredit(ctx, provider, credential, "")
}

func (r *Router) DiscoverProviderModels(ctx context.Context, providerID string) ([]providers.DiscoveredModel, error) {
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

	merged := make(map[string]providers.DiscoveredModel)
	credentialIDsByModel := make(map[string]map[string]struct{})
	var lastErr error
	healthyCount := 0

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
	}

	items := make([]providers.DiscoveredModel, 0, len(merged))
	for id, item := range merged {
		ids := make([]string, 0, len(credentialIDsByModel[id]))
		for credentialID := range credentialIDsByModel[id] {
			ids = append(ids, credentialID)
		}
		sort.Strings(ids)
		item.CredentialIDs = ids
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })

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
	if len(items) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return items, nil
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
			continue
		}
		selection.Credential = prepared
		adapter, errAdapter := r.registry.Adapter(selection.Provider.Type)
		if errAdapter != nil {
			lastErr = errAdapter
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
			_ = r.store.ClearCooldown(ctx, selection.Credential.ID)
			_ = r.store.AddUsage(ctx, store.UsageEvent{RequestID: request.RequestID, ClientAPIKeyID: requestClientAPIKeyID(request), PublicModelID: model.ID, ProviderID: selection.Provider.ID, UpstreamModel: selection.Route.UpstreamModel, CredentialID: selection.Credential.ID, Attempt: selection.Attempt, Status: 200, InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens, ReasoningTokens: response.Usage.ReasoningTokens, TokensSaved: requestTokensSaved(request), EstimatedCostUSD: r.estimateCost(response.Usage, selection), LatencyMS: time.Since(start).Milliseconds(), CreatedAt: time.Now()})
			if model.RewriteResponseModel {
				response.Model = model.ID
			}
			return &Result{Selection: selection, Response: response}, nil
		}
		lastErr = errExecute
		status := providers.Status(errExecute)
		code := providers.Code(errExecute)
		_ = r.store.AddUsage(ctx, store.UsageEvent{RequestID: request.RequestID, ClientAPIKeyID: requestClientAPIKeyID(request), PublicModelID: model.ID, ProviderID: selection.Provider.ID, UpstreamModel: selection.Route.UpstreamModel, CredentialID: selection.Credential.ID, Attempt: selection.Attempt, Status: status, LatencyMS: time.Since(start).Milliseconds(), ErrorCode: code, CreatedAt: time.Now()})
		if status == 0 || store.IsRetryableStatus(status) || status == 401 || status == 403 {
			r.setCredentialCooldown(ctx, selection.Credential.ID, model.ID, errExecute)
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
			continue
		}
		selection.Credential = prepared
		adapter, errAdapter := r.registry.Adapter(selection.Provider.Type)
		if errAdapter != nil {
			lastErr = errAdapter
			continue
		}
		if !r.acquireProviderStream(selection.Provider) {
			lastErr = &providers.ProviderError{Status: http.StatusTooManyRequests, Code: "provider_concurrency_limit", Message: fmt.Sprintf("provider %s concurrent stream limit is reached", selection.Provider.ID)}
			_ = r.store.AddUsage(ctx, store.UsageEvent{RequestID: request.RequestID, ClientAPIKeyID: requestClientAPIKeyID(request), PublicModelID: model.ID, ProviderID: selection.Provider.ID, UpstreamModel: selection.Route.UpstreamModel, CredentialID: selection.Credential.ID, Attempt: selection.Attempt, Status: http.StatusTooManyRequests, LatencyMS: time.Since(start).Milliseconds(), ErrorCode: providers.Code(lastErr), CreatedAt: time.Now()})
			continue
		}
		events, errExecute := adapter.ExecuteStream(ctx, selection.Provider, selection.Credential, request)
		if errExecute == nil && events == nil {
			errExecute = &providers.ProviderError{Status: http.StatusBadGateway, Code: "provider_protocol_error", Message: "provider returned no stream"}
		}
		if errExecute != nil && (providers.Status(errExecute) == 401 || providers.Status(errExecute) == 403) {
			if refreshed, refreshErr, ok := r.refreshAfterAuthError(ctx, selection); ok {
				selection.Credential = refreshed
				events, errExecute = adapter.ExecuteStream(ctx, selection.Provider, selection.Credential, request)
			} else if refreshErr != nil {
				errExecute = asCredentialError(refreshErr)
			}
		}
		if errExecute != nil {
			r.releaseProviderStream(selection.Provider)
			lastErr = errExecute
			status, code := providers.Status(errExecute), providers.Code(errExecute)
			_ = r.store.AddUsage(ctx, store.UsageEvent{RequestID: request.RequestID, ClientAPIKeyID: requestClientAPIKeyID(request), PublicModelID: model.ID, ProviderID: selection.Provider.ID, UpstreamModel: selection.Route.UpstreamModel, CredentialID: selection.Credential.ID, Attempt: selection.Attempt, Status: status, LatencyMS: time.Since(start).Milliseconds(), ErrorCode: code, CreatedAt: time.Now()})
			if status == 0 || store.IsRetryableStatus(status) || status == 401 || status == 403 {
				r.setCredentialCooldown(ctx, selection.Credential.ID, model.ID, errExecute)
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
			continue
		}
		selection.Credential = prepared
		adapter, errAdapter := r.registry.Adapter(selection.Provider.Type)
		if errAdapter != nil {
			lastErr = errAdapter
			continue
		}
		rawAdapter, ok := adapter.(providers.RawProxyAdapter)
		if !ok {
			lastErr = fmt.Errorf("provider %s does not support raw endpoint %s", selection.Provider.ID, path)
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
			_ = r.store.ClearCooldown(ctx, selection.Credential.ID)
			_ = r.store.AddUsage(ctx, store.UsageEvent{RequestID: requestID, ClientAPIKeyID: options.ClientAPIKeyID, PublicModelID: model.ID, ProviderID: selection.Provider.ID, UpstreamModel: selection.Route.UpstreamModel, CredentialID: selection.Credential.ID, Attempt: selection.Attempt, Status: raw.Status, EstimatedCostUSD: r.estimateCost(canonical.Usage{}, selection), LatencyMS: time.Since(start).Milliseconds(), CreatedAt: time.Now()})
			return &RawResult{Selection: selection, Response: raw}, nil
		}
		lastErr = errProxy
		status, code := providers.Status(errProxy), providers.Code(errProxy)
		_ = r.store.AddUsage(ctx, store.UsageEvent{RequestID: requestID, ClientAPIKeyID: options.ClientAPIKeyID, PublicModelID: model.ID, ProviderID: selection.Provider.ID, UpstreamModel: selection.Route.UpstreamModel, CredentialID: selection.Credential.ID, Attempt: selection.Attempt, Status: status, LatencyMS: time.Since(start).Milliseconds(), ErrorCode: code, CreatedAt: time.Now()})
		if status == 0 && !options.RetryNetworkErrors {
			return nil, &providers.ProviderError{Status: http.StatusBadGateway, Code: "ambiguous_upstream_failure", Message: "upstream connection failed after dispatch; request was not retried without an idempotency key", Err: errProxy}
		}
		if status == 0 || store.IsRetryableStatus(status) || status == 401 || status == 403 {
			r.setCredentialCooldown(ctx, selection.Credential.ID, model.ID, errProxy)
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
		return value
	case float64:
		return int(value)
	default:
		return 0
	}
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
		var usage canonical.Usage
		for event := range input {
			if model.RewriteResponseModel {
				event.Model = model.ID
			}
			if event.Usage != nil {
				usage = *event.Usage
			}
			if event.Type == canonical.EventError {
				status = 502
			}
			select {
			case out <- event:
			case <-ctx.Done():
				status = 499
				return
			}
		}
		if status == 200 {
			_ = r.store.ClearCooldown(context.Background(), selection.Credential.ID)
		}
		_ = r.store.AddUsage(context.Background(), store.UsageEvent{RequestID: request.RequestID, ClientAPIKeyID: requestClientAPIKeyID(request), PublicModelID: model.ID, ProviderID: selection.Provider.ID, UpstreamModel: selection.Route.UpstreamModel, CredentialID: selection.Credential.ID, Attempt: selection.Attempt, Status: status, InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, ReasoningTokens: usage.ReasoningTokens, TokensSaved: requestTokensSaved(request), EstimatedCostUSD: r.estimateCost(usage, selection), LatencyMS: time.Since(start).Milliseconds(), CreatedAt: time.Now()})
	}()
	return out
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
	routes, err := r.routesForModel(ctx, model)
	if err != nil {
		return nil, err
	}
	var selections []Selection
	policyLimited := false
	now := time.Now()
	for _, route := range routes {
		if !routeMatches(route, request) {
			continue
		}
		provider, errProvider := r.store.Provider(ctx, route.ProviderID)
		if errProvider != nil || !provider.Enabled || provider.Status == "disabled" || provider.Status == "auth_required" || provider.Status == "cooldown" {
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
		eligible = r.filterModelCooldowns(ctx, eligible, model.ID, now)
		eligible = r.rotate(route.ID, route.Priority, eligible)
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

func (r *Router) routesForModel(ctx context.Context, model store.PublicModel) ([]store.RouteTarget, error) {
	if len(model.ComboItems) == 0 {
		routes, err := r.store.Routes(ctx, model.ID)
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

func (r *Router) rotate(providerID string, priority int, credentials []store.Credential) []store.Credential {
	if len(credentials) < 2 {
		return credentials
	}
	r.mu.Lock()
	strategy := r.strategy
	r.mu.Unlock()
	if strategy == "fill-first" {
		return credentials
	}
	key := fmt.Sprintf("%s:%d", providerID, priority)
	r.mu.Lock()
	totalWeight := 0
	for _, credential := range credentials {
		weight := credential.Weight
		if weight <= 0 {
			weight = 1
		}
		totalWeight += weight
	}
	slot := r.rotation[key] % totalWeight
	r.rotation[key] = (slot + 1) % totalWeight
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

func (r *Router) rotateRoutes(modelID string, routes []store.RouteTarget) []store.RouteTarget {
	if len(routes) < 2 {
		return routes
	}
	r.mu.Lock()
	strategy := r.strategy
	r.mu.Unlock()
	if strategy == "fill-first" {
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
	return strings.EqualFold(provider.ID, pin) || strings.EqualFold(provider.Name, pin) || strings.EqualFold(provider.Type, pin)
}

func (r *Router) setCredentialCooldown(ctx context.Context, credentialID, modelID string, err error) {
	if credentialID == "" || err == nil {
		return
	}
	status := providers.Status(err)
	count := 0
	if modelID != "" {
		count = r.store.ModelCooldownCount(ctx, credentialID, modelID)
	}
	until := credentialCooldownUntil(time.Now(), r.cooldowns, err, count)
	code := providers.Code(err)
	message := err.Error()
	if r.cooldowns.accountLevel(status) || modelID == "" {
		_ = r.store.SetCooldown(ctx, credentialID, code, message, until)
		return
	}
	_ = r.store.SetModelCooldown(ctx, credentialID, modelID, code, message, until, status, count+1)
}

func (r *Router) filterModelCooldowns(ctx context.Context, credentials []store.Credential, modelID string, now time.Time) []store.Credential {
	if modelID == "" {
		return credentials
	}
	filtered := make([]store.Credential, 0, len(credentials))
	for _, credential := range credentials {
		until, err := r.store.ModelCooldownUntil(ctx, credential.ID, modelID, now)
		if err != nil || until.IsZero() {
			filtered = append(filtered, credential)
		}
	}
	return filtered
}
