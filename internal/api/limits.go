package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/router"
	"github.com/tproxy/tproxy/internal/store"
)

type limitError struct {
	Code       string
	Message    string
	RetryAfter string
}

func (e *limitError) Error() string { return e.Message }

type requestWindow struct {
	Started time.Time
	Count   int
}

type limitScope struct {
	ID     string
	Limits config.LimitPolicy
}

type requestLimiter struct {
	mu            sync.Mutex
	windows       map[string]requestWindow
	streams       map[string]int
	mediaInflight map[[2]string]chan struct{}
}

func newRequestLimiter() *requestLimiter {
	return &requestLimiter{windows: make(map[string]requestWindow), streams: make(map[string]int)}
}

func (l *requestLimiter) admitRequest(key *store.APIKey, path string, scopes ...limitScope) error {
	if key != nil && !endpointAllowed(key.Policy.Endpoints, path) {
		return &limitError{Code: "endpoint_forbidden", Message: fmt.Sprintf("endpoint %s is not allowed for this API key", path)}
	}
	if len(scopes) == 0 && key != nil {
		scopes = []limitScope{{ID: "key:" + key.ID, Limits: key.Policy.Limits}}
	}
	if len(scopes) == 0 {
		return nil
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, scope := range scopes {
		limit := scope.Limits.RequestsPerMinute
		if limit <= 0 {
			continue
		}
		window := l.windows[scope.ID]
		if window.Started.IsZero() || now.Sub(window.Started) >= time.Minute {
			window = requestWindow{Started: now}
		}
		if window.Count >= limit {
			return &limitError{Code: "rate_limit_exceeded", Message: fmt.Sprintf("requests per minute limit exceeded for %s", scope.ID), RetryAfter: fmt.Sprint(max(1, int(time.Until(window.Started.Add(time.Minute)).Seconds())+1))}
		}
	}
	for _, scope := range scopes {
		window := l.windows[scope.ID]
		if window.Started.IsZero() || now.Sub(window.Started) >= time.Minute {
			window = requestWindow{Started: now}
		}
		window.Count++
		l.windows[scope.ID] = window
	}
	return nil
}

func (l *requestLimiter) acquireStream(key *store.APIKey, scopes ...limitScope) error {
	if len(scopes) == 0 && key != nil {
		scopes = []limitScope{{ID: "key:" + key.ID, Limits: key.Policy.Limits}}
	}
	if len(scopes) == 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, scope := range scopes {
		if scope.Limits.ConcurrentStreams > 0 && l.streams[scope.ID] >= scope.Limits.ConcurrentStreams {
			return &limitError{Code: "concurrency_limit_exceeded", Message: fmt.Sprintf("concurrent stream limit exceeded for %s", scope.ID)}
		}
	}
	for _, scope := range scopes {
		l.streams[scope.ID]++
	}
	return nil
}

func (l *requestLimiter) releaseStream(key *store.APIKey, scopes ...limitScope) {
	if len(scopes) == 0 && key != nil {
		scopes = []limitScope{{ID: "key:" + key.ID, Limits: key.Policy.Limits}}
	}
	if len(scopes) == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, scope := range scopes {
		if l.streams[scope.ID] > 1 {
			l.streams[scope.ID]--
		} else {
			delete(l.streams, scope.ID)
		}
	}
}

func endpointAllowed(endpoints []string, path string) bool {
	if len(endpoints) == 0 {
		return true
	}
	for _, pattern := range endpoints {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" || pattern == "*" || pattern == path {
			return true
		}
		if strings.HasSuffix(pattern, "/*") && strings.HasPrefix(path, strings.TrimSuffix(pattern, "*")) {
			return true
		}
	}
	return false
}

func policyInputLimit(policy config.ClientKeyPolicy) int64 { return policy.Limits.MaxInputBytes }

func isBodyRequest(r *http.Request) bool {
	return r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch
}

func (s *Server) admitClientIngress(key *store.APIKey, r *http.Request) error {
	// A key must be able to inspect its own limits even when generation is
	// exhausted or its endpoint policy only permits inference endpoints.
	if route, ok := classifyProxyIngress(r.URL.Path); ok && route.CanonicalPath == "/v1/limits" {
		return nil
	}
	// The upgrade has no generation; each response.create consumes one request.
	if route, ok := classifyProxyIngress(r.URL.Path); ok && route.CanonicalPath == "/v1/responses/ws" && r.Method == http.MethodGet {
		if key != nil && !endpointAllowed(key.Policy.Endpoints, r.URL.Path) {
			return &limitError{Code: "endpoint_forbidden", Message: "endpoint is not allowed for this API key"}
		}
		return nil
	}
	return s.limiter.admitRequest(key, r.URL.Path, s.limitScopes(key)...)
}

func (s *Server) lockMediaRequest(ctx context.Context, keyID, idempotencyKey string) (func(), error) {
	id := [2]string{keyID, idempotencyKey}
	for {
		s.limiter.mu.Lock()
		if s.limiter.mediaInflight == nil {
			s.limiter.mediaInflight = make(map[[2]string]chan struct{})
		}
		pending := s.limiter.mediaInflight[id]
		if pending == nil {
			pending = make(chan struct{})
			s.limiter.mediaInflight[id] = pending
			s.limiter.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					s.limiter.mu.Lock()
					delete(s.limiter.mediaInflight, id)
					close(pending)
					s.limiter.mu.Unlock()
				})
			}, nil
		}
		s.limiter.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-pending:
		}
	}
}

func (s *Server) acquireClientStream(key *store.APIKey) (func(), error) {
	scopes := s.limitScopes(key)
	if err := s.limiter.acquireStream(key, scopes...); err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { s.limiter.releaseStream(key, scopes...) }) }, nil
}

func (s *Server) prepareClientRequest(r *http.Request, request *canonical.Request, key *store.APIKey) error {
	attachClientPolicyMetadata(request, key)
	if request.Metadata == nil {
		request.Metadata = map[string]any{}
	}
	encoded, _ := json.Marshal(request.Raw)
	inputBytes := int64(len(encoded))
	if r.ContentLength > 0 {
		inputBytes = r.ContentLength
	}
	request.Metadata[router.InputBytesMetadataKey] = inputBytes
	limits := s.effectiveLimits(key)
	if limits.MaxInputBytes > 0 && inputBytes > limits.MaxInputBytes {
		return &providers.ProviderError{Status: http.StatusRequestEntityTooLarge, Code: "request_too_large", Message: "request body exceeds API key max_input_bytes"}
	}
	if err := s.enforceClientBudget(r.Context(), key); err != nil {
		return &providers.ProviderError{Status: http.StatusPaymentRequired, Code: "budget_exceeded", Message: err.Error(), RetryAfter: fmt.Sprint(max(1, int(time.Until(startOfDayUTC(time.Now()).Add(24*time.Hour)).Seconds())+1))}
	}
	if limits.MaxOutputTokens > 0 && request.MaxTokens > limits.MaxOutputTokens {
		return &providers.ProviderError{Status: http.StatusBadRequest, Code: "max_output_tokens_exceeded", Message: "requested output tokens exceed API key policy"}
	}
	if request.MaxTokens <= 0 {
		request.Metadata[router.DefaultOutputLimitMetadataKey] = true
		if limits.MaxOutputTokens > 0 {
			request.SetMaxTokens(limits.MaxOutputTokens)
		}
	}
	return nil
}

func (s *Server) mediaScopes(key *store.APIKey) []store.MediaJobScope {
	var result []store.MediaJobScope
	for _, scope := range s.limitScopes(key) {
		kind, id, _ := strings.Cut(scope.ID, ":")
		result = append(result, store.MediaJobScope{Kind: kind, ID: id, Limit: scope.Limits.MediaJobs})
	}
	return result
}

func (s *Server) keyCapacity(ctx context.Context, key *store.APIKey, model string) (router.CapacitySnapshot, error) {
	snapshot, err := s.router.Capacity(ctx, key, model)
	if err != nil {
		return snapshot, err
	}
	snapshot.SharedLimits = make(map[string]router.EffectiveLimit, len(snapshot.EffectiveLimits))
	for name, limit := range snapshot.EffectiveLimits {
		snapshot.SharedLimits[name] = limit
	}
	for _, scope := range s.limitScopes(key) {
		used := map[string]float64{}
		s.limiter.mu.Lock()
		window := s.limiter.windows[scope.ID]
		if time.Since(window.Started) < time.Minute {
			used["requests_per_minute"] = float64(window.Count)
		}
		used["concurrent_streams"] = float64(s.limiter.streams[scope.ID])
		s.limiter.mu.Unlock()
		kind, id, _ := strings.Cut(scope.ID, ":")
		if scope.Limits.MediaJobs > 0 {
			count, err := s.store.ActiveMediaJobsInScope(ctx, store.MediaJobScope{Kind: kind, ID: id})
			if err != nil {
				return snapshot, err
			}
			used["media_jobs"] = float64(count)
		}
		if scope.Limits.BudgetUSDPerDay > 0 {
			var cost float64
			switch kind {
			case "global":
				cost, err = s.store.TotalEstimatedCostSince(ctx, startOfDayUTC(snapshot.ComputedAt))
			case "team":
				cost, err = s.store.TeamEstimatedCostSince(ctx, id, startOfDayUTC(snapshot.ComputedAt))
			case "key":
				cost, err = s.store.EstimatedCostSince(ctx, id, startOfDayUTC(snapshot.ComputedAt))
			}
			if err != nil {
				return snapshot, err
			}
			used["budget_usd_per_day"] = cost
		}
		for name, value := range router.LimitValues(scope.Limits) {
			limit := router.NewEffectiveLimit(value, used[name], scope.ID)
			if name == "max_input_bytes" || name == "max_output_tokens" {
				limit.Remaining = nil
			}
			snapshot.EffectiveLimits[name] = router.TightenLimit(snapshot.EffectiveLimits[name], limit)
		}
	}
	return snapshot, nil
}
