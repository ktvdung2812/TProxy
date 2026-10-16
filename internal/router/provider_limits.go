package router

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/store"
)

// DefaultOutputLimitMetadataKey distinguishes a default from an explicit client request.
const DefaultOutputLimitMetadataKey = "default_output_limit"
const InputBytesMetadataKey = "client_input_bytes"

func providerLimited(message, retryAfter string) error {
	return &providers.ProviderError{Status: http.StatusTooManyRequests, Code: "provider_limit_exceeded", Message: message, RetryAfter: retryAfter}
}

func localPolicyError(err error) bool {
	switch providers.Code(err) {
	case "provider_limit_exceeded", "provider_concurrency_limit", "media_job_limit_exceeded":
		return true
	}
	return false
}

func (r *Router) providerRequestCount(id string, now time.Time) int {
	r.limitsMu.Lock()
	defer r.limitsMu.Unlock()
	count := 0
	for _, at := range r.providerRequests[id] {
		if at.After(now.Add(-time.Minute)) {
			count++
		}
	}
	return count
}

// admitProviderRequest counts dispatches (including retries), not completed usage rows.
func (r *Router) admitProviderRequest(ctx context.Context, provider store.Provider, request canonical.Request) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	within, err := r.providerWithinPolicy(ctx, provider, request)
	if err != nil {
		return err
	}
	if !within {
		return providerLimited("provider resource or daily budget limit exceeded for "+provider.ID, "")
	}
	return r.admitProviderRPM(ctx, provider)
}

// Polling existing jobs consumes RPM, but must not be blocked by a generation budget.
func (r *Router) admitProviderRPM(ctx context.Context, provider store.Provider) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	now := time.Now()
	r.limitsMu.Lock()
	defer r.limitsMu.Unlock()
	if r.providerRequests == nil {
		r.providerRequests = make(map[string][]time.Time)
	}
	window := r.providerRequests[provider.ID]
	first := 0
	for first < len(window) && !window[first].After(now.Add(-time.Minute)) {
		first++
	}
	window = window[first:]
	if limit := provider.Limits.RequestsPerMinute; limit > 0 && len(window) >= limit {
		r.providerRequests[provider.ID] = window
		retry := strconv.Itoa(max(1, int(math.Ceil(window[0].Add(time.Minute).Sub(now).Seconds()))))
		return providerLimited(fmt.Sprintf("requests per minute limit exceeded for provider:%s", provider.ID), retry)
	}
	r.providerRequests[provider.ID] = append(window, now)
	return nil
}

func requestForProvider(request canonical.Request, provider store.Provider) canonical.Request {
	limit := provider.Limits.MaxOutputTokens
	defaulted, _ := request.Metadata[DefaultOutputLimitMetadataKey].(bool)
	if limit > 0 && (request.MaxTokens <= 0 || (defaulted && request.MaxTokens > limit)) {
		request.SetMaxTokens(limit)
	}
	return request
}

func isMediaCreation(path, method string) bool {
	if method != "" && method != http.MethodPost {
		return false
	}
	switch path {
	case "/v1/videos", "/v1/videos/generations", "/v1/videos/edits", "/v1/videos/extensions":
		return true
	}
	return false
}
