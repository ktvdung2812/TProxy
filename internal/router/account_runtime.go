package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/store"
)

type AccountRuntime struct {
	CredentialID   string    `json:"credential_id"`
	InFlight       int       `json:"in_flight"`
	Waiting        int       `json:"waiting"`
	Samples        int       `json:"samples"`
	LatencyMS      float64   `json:"latency_ms"`
	TTFTMS         float64   `json:"ttft_ms"`
	ErrorRate      float64   `json:"error_rate"`
	QuotaRemaining *float64  `json:"quota_remaining,omitempty"`
	QuotaAt        time.Time `json:"quota_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	changed        chan struct{}
}

func (r *Router) runtimeLocked(id string) *AccountRuntime {
	if r.accounts == nil {
		r.accounts = make(map[string]*AccountRuntime)
	}
	if state := r.accounts[id]; state != nil {
		return state
	}
	// Bound inactive observations; active admissions are never evicted.
	if len(r.accounts) >= 10000 {
		var oldest string
		for key, value := range r.accounts {
			if value.InFlight == 0 && value.Waiting == 0 && (oldest == "" || value.UpdatedAt.Before(r.accounts[oldest].UpdatedAt)) {
				oldest = key
			}
		}
		delete(r.accounts, oldest)
	}
	state := &AccountRuntime{CredentialID: id, changed: make(chan struct{}), UpdatedAt: time.Now()}
	r.accounts[id] = state
	return state
}

func (r *Router) AccountRuntime() []AccountRuntime {
	r.accountMu.Lock()
	defer r.accountMu.Unlock()
	items := make([]AccountRuntime, 0, len(r.accounts))
	for _, state := range r.accounts {
		items = append(items, *state)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CredentialID < items[j].CredentialID })
	return items
}

func admissionError(code string) error {
	return &providers.ProviderError{Status: http.StatusTooManyRequests, Code: code, Message: "account capacity is busy; retry shortly", RetryAfter: "1"}
}

func (r *Router) acquireAccount(ctx context.Context, credential store.Credential, wait bool) (func(), int64, error) {
	policy := store.CredentialAdmission(credential.Metadata)
	started := time.Now()
	r.accountMu.Lock()
	state := r.runtimeLocked(credential.ID)
	queued := false
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	leave := func() {
		if queued {
			state.Waiting--
			queued = false
		}
	}
	for {
		if ctx.Err() != nil {
			leave()
			r.accountMu.Unlock()
			return nil, time.Since(started).Milliseconds(), ctx.Err()
		}
		if policy.MaxConcurrent <= 0 || state.InFlight < policy.MaxConcurrent {
			leave()
			state.InFlight++
			state.UpdatedAt = time.Now()
			r.accountMu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					r.accountMu.Lock()
					defer r.accountMu.Unlock()
					state.InFlight--
					state.UpdatedAt = time.Now()
					close(state.changed)
					state.changed = make(chan struct{})
				})
			}, time.Since(started).Milliseconds(), nil
		}
		if !queued {
			if !wait || policy.MaxQueue <= 0 || state.Waiting >= policy.MaxQueue || policy.QueueTimeoutMS <= 0 {
				r.accountMu.Unlock()
				return nil, time.Since(started).Milliseconds(), admissionError("account_queue_full")
			}
			state.Waiting++
			queued = true
			timer = time.NewTimer(time.Duration(policy.QueueTimeoutMS) * time.Millisecond)
		}
		changed := state.changed
		r.accountMu.Unlock()
		var err error
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-timer.C:
			err = admissionError("account_queue_timeout")
		case <-changed:
		}
		r.accountMu.Lock()
		if err != nil {
			leave()
			r.accountMu.Unlock()
			return nil, time.Since(started).Milliseconds(), err
		}
	}
}

func (r *Router) admitSelection(ctx context.Context, selection *Selection, remaining []Selection, noFallback bool) (func(), error) {
	wait := true
	if !noFallback {
		r.accountMu.Lock()
		for _, next := range remaining {
			policy := store.CredentialAdmission(next.Credential.Metadata)
			state := r.accounts[next.Credential.ID]
			if policy.MaxConcurrent <= 0 || state == nil || state.InFlight < policy.MaxConcurrent {
				wait = false
				break
			}
		}
		r.accountMu.Unlock()
	}
	release, queued, err := r.acquireAccount(ctx, selection.Credential, wait)
	selection.QueueMS = queued
	if err != nil {
		return nil, err
	}
	// A queued request must honor an account disabled/expired while it waited.
	credential, err := r.store.CredentialByID(ctx, selection.Credential.ID)
	if err != nil || len(store.EligibleCredentials([]store.Credential{credential}, time.Now())) == 0 {
		release()
		return nil, admissionError("account_unavailable")
	}
	selection.StartedAt = time.Now()
	return release, nil
}

func (r *Router) recordAccount(selection Selection, status int, ttft *int64) {
	if status == 499 {
		return
	}
	r.accountMu.Lock()
	defer r.accountMu.Unlock()
	state := r.runtimeLocked(selection.Credential.ID)
	latency := float64(time.Since(selection.StartedAt).Milliseconds())
	if selection.StartedAt.IsZero() {
		return
	}
	failure := 0.0
	if status == 0 || status == 429 || status >= 500 || status == 401 || status == 403 {
		failure = 1
	}
	if state.Samples == 0 {
		state.LatencyMS = latency
		state.ErrorRate = failure
	} else {
		state.LatencyMS = state.LatencyMS*.8 + latency*.2
		state.ErrorRate = state.ErrorRate*.8 + failure*.2
	}
	if ttft != nil {
		if state.TTFTMS == 0 {
			state.TTFTMS = float64(*ttft)
		} else {
			state.TTFTMS = state.TTFTMS*.8 + float64(*ttft)*.2
		}
	}
	state.Samples++
	state.UpdatedAt = time.Now()
}

func (r *Router) observeQuota(id string, quota providers.CredentialQuota) {
	var remaining *float64
	for _, entry := range quota.Quotas {
		if entry.Total <= 0 {
			continue
		}
		value := (entry.Total - entry.Used) / entry.Total
		if remaining == nil || value < *remaining {
			remaining = &value
		}
	}
	if remaining == nil {
		return
	}
	r.accountMu.Lock()
	defer r.accountMu.Unlock()
	state := r.runtimeLocked(id)
	state.QuotaRemaining = remaining
	state.QuotaAt = time.Now()
}

func (r *Router) measuredOrder(strategy string, credentials []store.Credential) []store.Credential {
	switch strategy {
	case "latency-aware", "capacity-aware", "health-first", "least-used", "quota-aware":
	default:
		return nil
	}
	r.accountMu.Lock()
	defer r.accountMu.Unlock()
	score := func(c store.Credential) float64 {
		state := r.accounts[c.ID]
		if state == nil {
			return -1
		} // Explore unseen accounts.
		if state.InFlight == 0 && time.Since(state.UpdatedAt) > 5*time.Minute {
			return -1
		}
		load := float64(state.InFlight)
		if limit := store.CredentialAdmission(c.Metadata).MaxConcurrent; limit > 0 {
			load /= float64(limit)
		}
		switch strategy {
		case "latency-aware":
			latency := state.LatencyMS
			if state.TTFTMS > 0 {
				latency = state.TTFTMS
			}
			return latency*(1+load) + state.ErrorRate*10000
		case "health-first":
			return state.ErrorRate*1000 + load
		case "least-used":
			return float64(state.Samples) + load
		case "quota-aware":
			if state.QuotaRemaining != nil && time.Since(state.QuotaAt) < 5*time.Minute {
				return 1 - *state.QuotaRemaining + load*.1
			}
			return 1 + load*.1
		default:
			return load + float64(state.Waiting) + state.ErrorRate
		}
	}
	ordered := append([]store.Credential(nil), credentials...)
	sort.SliceStable(ordered, func(i, j int) bool { return score(ordered[i]) < score(ordered[j]) })
	return ordered
}

func firstContentEvent(event canonical.Event) bool {
	switch event.Type {
	case canonical.EventTextDelta:
		return event.Text != ""
	case canonical.EventReasoningDelta:
		return event.Reasoning != "" || event.Text != ""
	case canonical.EventToolCallDelta:
		return len(event.ToolCall) > 0
	case canonical.EventImageDelta, canonical.EventAudioDelta:
		return event.Media != nil
	case canonical.EventResponsesSSE:
		var body map[string]any
		if json.Unmarshal(event.SSEData, &body) != nil || body["delta"] == nil || body["delta"] == "" {
			return false
		}
		return strings.HasSuffix(event.SSEEvent, ".delta") && (strings.Contains(event.SSEEvent, "text") || strings.Contains(event.SSEEvent, "reasoning") || strings.Contains(event.SSEEvent, "arguments") || strings.Contains(event.SSEEvent, "audio"))
	}
	return false
}

func outcomeStatus(err error) int {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return 499
	}
	if err == nil {
		return 200
	}
	return providers.Status(err)
}
