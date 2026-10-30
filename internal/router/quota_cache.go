package router

import (
	"context"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/store"
)

type QuotaSnapshot struct {
	CredentialID string                     `json:"credential_id"`
	State        string                     `json:"state"`
	Quota        *providers.CredentialQuota `json:"quota,omitempty"`
	CheckedAt    time.Time                  `json:"checked_at"`
	AttemptedAt  time.Time                  `json:"attempted_at"`
	Stale        bool                       `json:"stale"`
	Error        string                     `json:"error,omitempty"`
}

type quotaFlight struct {
	done  chan struct{}
	quota providers.CredentialQuota
	err   error
}

// quotaBlockedError uses the last snapshot only. Recovery follows the same
// independent-window rules as quota admission, without converting quota units.
func (r *Router) quotaBlockedError(credentials []store.Credential, providerType string, now time.Time) error {
	blocked := false
	var earliest time.Time
	for _, credential := range credentials {
		if !store.QuotaAutoDisabled(credential.Metadata) || store.AccountExpired(credential, now) || credential.Status == "auth_required" {
			continue
		}
		blocked = true
		snapshot := r.CachedQuota(credential.ID, providerType)
		if snapshot.Quota == nil {
			continue
		}
		quota := *snapshot.Quota
		var resets []time.Time
		for _, entry := range quota.Quotas {
			if reset, err := time.Parse(time.RFC3339, entry.ResetAt); err == nil && reset.After(now) {
				resets = append(resets, reset)
			}
		}
		sort.Slice(resets, func(i, j int) bool { return resets[i].Before(resets[j]) })
		for _, reset := range resets {
			for key, entry := range quota.Quotas {
				at, err := time.Parse(time.RFC3339, entry.ResetAt)
				if err == nil && !at.After(reset) {
					entry.Used, entry.Remaining = 0, entry.Total
					quota.Quotas[key] = entry
				}
			}
			if !providers.QuotaAtZero(quota) {
				if earliest.IsZero() || reset.Before(earliest) {
					earliest = reset
				}
				break
			}
		}
	}
	if !blocked {
		return nil
	}
	retry := ""
	if !earliest.IsZero() {
		retry = strconv.Itoa(max(1, int(math.Ceil(earliest.Sub(now).Seconds()))))
	}
	return &providers.ProviderError{Status: http.StatusTooManyRequests, Code: "upstream_rate_limited", Message: "eligible accounts have exhausted upstream quota; waiting for quota recovery", RetryAfter: retry}
}

func QuotaRefreshInterval(providerType string) time.Duration {
	if providerType == "claude" {
		return 3 * time.Minute
	}
	return time.Minute
}

func cloneQuota(quota providers.CredentialQuota) providers.CredentialQuota {
	copy := quota
	copy.Quotas = make(map[string]providers.QuotaEntry, len(quota.Quotas))
	for key, entry := range quota.Quotas {
		copy.Quotas[key] = entry
	}
	return copy
}

func quotaSnapshotState(quota providers.CredentialQuota) string {
	if len(quota.Quotas) == 0 {
		return "unknown"
	}
	state := "unbounded"
	for _, entry := range quota.Quotas {
		if !entry.Unlimited {
			if entry.Total <= 0 {
				return "unknown"
			}
			state = "finite"
		}
	}
	return state
}

// CachedQuota never performs IO, changes rotation, or waits for a running probe.
func (r *Router) CachedQuota(credentialID, providerType string) QuotaSnapshot {
	r.quotaMu.Lock()
	defer r.quotaMu.Unlock()
	snapshot, ok := r.quotaCache[credentialID]
	if !ok {
		return QuotaSnapshot{CredentialID: credentialID, State: "unknown"}
	}
	if snapshot.Quota != nil {
		quota := cloneQuota(*snapshot.Quota)
		snapshot.Quota = &quota
	}
	snapshot.Stale = snapshot.State == "unknown" || snapshot.CheckedAt.IsZero() || time.Since(snapshot.CheckedAt) >= QuotaRefreshInterval(providerType)
	return snapshot
}

func (r *Router) CredentialQuota(ctx context.Context, provider store.Provider, credential store.Credential) (providers.CredentialQuota, error) {
	return r.refreshCredentialQuota(ctx, provider, credential, true)
}

func (r *Router) RefreshQuotaIfDue(ctx context.Context, provider store.Provider, credential store.Credential) (providers.CredentialQuota, error) {
	quota, err := r.refreshCredentialQuota(ctx, provider, credential, false)
	if err == nil {
		r.warmQuotaWindow(ctx, provider, credential, quota)
	}
	return quota, err
}

func (r *Router) refreshCredentialQuota(ctx context.Context, provider store.Provider, credential store.Credential, force bool) (providers.CredentialQuota, error) {
	r.quotaMu.Lock()
	if r.quotaCache == nil {
		r.quotaCache = make(map[string]QuotaSnapshot)
		r.quotaInflight = make(map[string]*quotaFlight)
		r.quotaSlots = make(chan struct{}, 4)
	}
	if flight := r.quotaInflight[credential.ID]; flight != nil {
		r.quotaMu.Unlock()
		select {
		case <-ctx.Done():
			return providers.CredentialQuota{}, ctx.Err()
		case <-flight.done:
			return cloneQuota(flight.quota), flight.err
		}
	}
	previous := r.quotaCache[credential.ID]
	if !force && !previous.AttemptedAt.IsZero() && time.Since(previous.AttemptedAt) < QuotaRefreshInterval(provider.Type) {
		r.quotaMu.Unlock()
		if previous.Quota != nil {
			return cloneQuota(*previous.Quota), nil
		}
		return providers.CredentialQuota{CredentialID: credential.ID, Quotas: map[string]providers.QuotaEntry{}}, nil
	}
	flight := &quotaFlight{done: make(chan struct{})}
	r.quotaInflight[credential.ID] = flight
	slots := r.quotaSlots
	r.quotaMu.Unlock()

	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var quota providers.CredentialQuota
	var err error
	select {
	case slots <- struct{}{}:
		r.mu.Lock()
		refresher := r.refresher
		r.mu.Unlock()
		if refresher != nil && (credential.AuthType == "oauth" || credential.AuthType == "service_account") {
			if refreshed, refreshErr := refresher.EnsureValid(probeCtx, provider, credential, false); refreshErr == nil {
				credential = refreshed
			}
		}
		quota, err = r.fetchCredentialQuota(probeCtx, provider, credential)
		<-slots
	case <-probeCtx.Done():
		err = probeCtx.Err()
	}

	r.quotaMu.Lock()
	previous.CredentialID = credential.ID
	previous.AttemptedAt = time.Now().UTC()
	previous.Error = ""
	if err == nil && len(quota.Quotas) > 0 {
		copy := cloneQuota(quota)
		previous.Quota = &copy
		previous.CheckedAt = previous.AttemptedAt
		previous.State = quotaSnapshotState(quota)
	} else {
		previous.State = "unknown"
		previous.Error = "quota_unavailable"
	}
	r.quotaCache[credential.ID] = previous
	flight.quota, flight.err = cloneQuota(quota), err
	delete(r.quotaInflight, credential.ID)
	close(flight.done)
	r.quotaMu.Unlock()
	return quota, err
}
