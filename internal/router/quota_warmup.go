package router

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/store"
)

const quotaWarmupWindow = 5 * time.Hour

// warmQuotaWindow sends one minimal probe when a fresh rolling quota window is
// still untouched. The request starts the provider's timer without going
// through normal routing or consuming a user's request context.
func (r *Router) warmQuotaWindow(ctx context.Context, provider store.Provider, credential store.Credential, quota providers.CredentialQuota) {
	providerType := strings.ToLower(strings.TrimSpace(provider.Type))
	if providerType != "codex" && providerType != "antigravity" {
		return
	}
	resetAt, ok := quotaWarmupResetAt(providerType, quota, time.Now())
	if !ok {
		return
	}
	modelID, ok := r.quotaWarmupModel(ctx, provider, quota)
	if !ok || !r.claimQuotaWarmup(credential.ID, resetAt) {
		return
	}
	warmupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// PingModel uses "hi" and the smallest adapter-specific output limit. Its
	// result is intentionally ignored: the upstream request itself is the goal.
	_, _, _ = r.TestUpstreamModel(warmupCtx, provider.ID, modelID, "llm", credential.ID, nil)
}

func (r *Router) claimQuotaWarmup(credentialID string, resetAt time.Time) bool {
	if strings.TrimSpace(credentialID) == "" {
		return false
	}
	r.quotaWarmupMu.Lock()
	defer r.quotaWarmupMu.Unlock()
	if r.quotaWarmups == nil {
		r.quotaWarmups = make(map[string]time.Time)
	}
	if previous, ok := r.quotaWarmups[credentialID]; ok && previous.Equal(resetAt) {
		return false
	}
	r.quotaWarmups[credentialID] = resetAt
	return true
}

// quotaWarmupResetAt finds a full window whose reset is still at its maximum.
// The API probe and the local clock are a few seconds apart, so a small
// tolerance avoids missing the first background pass.
func quotaWarmupResetAt(providerType string, quota providers.CredentialQuota, now time.Time) (time.Time, bool) {
	for _, entry := range quota.Quotas {
		if entry.Total <= 0 || entry.Used > 0.0001 || entry.ResetAt == "" {
			continue
		}
		resetAt, err := time.Parse(time.RFC3339Nano, entry.ResetAt)
		if err != nil {
			continue
		}
		window := time.Duration(entry.WindowMinutes * float64(time.Minute))
		if window <= 0 && providerType == "antigravity" {
			window = quotaWarmupWindow
		}
		if window <= 0 {
			continue
		}
		tolerance := 2 * time.Minute
		if candidate := window / 100; candidate > tolerance {
			tolerance = candidate
		}
		remaining := resetAt.Sub(now)
		if remaining >= window-tolerance && remaining <= window+tolerance {
			return resetAt, true
		}
	}
	return time.Time{}, false
}

func (r *Router) quotaWarmupModel(ctx context.Context, provider store.Provider, quota providers.CredentialQuota) (string, bool) {
	// Prefer a model already configured for this provider. This keeps the probe
	// on the same model family the operator actually routes.
	if r.store != nil {
		if models, err := r.store.PublicModels(ctx); err == nil {
			for _, model := range models {
				if !model.Enabled {
					continue
				}
				routes, routeErr := r.store.Routes(ctx, model.ID)
				if routeErr != nil {
					continue
				}
				for _, route := range routes {
					if route.Enabled && route.ProviderID == provider.ID && strings.TrimSpace(route.UpstreamModel) != "" {
						return route.UpstreamModel, true
					}
				}
			}
		}
	}

	if strings.EqualFold(provider.Type, "antigravity") {
		keys := make([]string, 0, len(quota.Quotas))
		for key := range quota.Quotas {
			if key != "" {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			return keys[0], true
		}
	}
	if strings.EqualFold(provider.Type, "codex") {
		return "gpt-5.4", true
	}
	return "", false
}
