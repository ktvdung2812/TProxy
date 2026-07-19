package resilience

import (
	"net/http"
	"sync"
	"time"
)

// State is the provider circuit breaker state.
type State string

const (
	StateClosed   State = "closed"
	StateDegraded State = "degraded"
	StateOpen     State = "open"
	StateHalfOpen State = "half_open"
)

// Config controls when a provider circuit opens.
type Config struct {
	DegradedThreshold int
	FailureThreshold  int
	ResetTimeout      time.Duration
}

func DefaultOAuthConfig() Config {
	return Config{DegradedThreshold: 5, FailureThreshold: 8, ResetTimeout: 60 * time.Second}
}

func DefaultAPIKeyConfig() Config {
	return Config{DegradedThreshold: 7, FailureThreshold: 12, ResetTimeout: 30 * time.Second}
}

type entry struct {
	state     State
	failures  int
	openedAt  time.Time
	config    Config
}

// Registry tracks per-provider circuit breaker state.
type Registry struct {
	mu      sync.Mutex
	entries map[string]*entry
}

func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]*entry)}
}

func (r *Registry) Configure(providerID string, cfg Config) {
	if providerID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entries[providerID]
	if e == nil {
		e = &entry{state: StateClosed, config: cfg}
		r.entries[providerID] = e
		return
	}
	e.config = cfg
}

func (r *Registry) CanExecute(providerID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entryLocked(providerID)
	r.refreshLocked(e)
	return e.state != StateOpen
}

func (r *Registry) Status(providerID string) State {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entryLocked(providerID)
	r.refreshLocked(e)
	return e.state
}

func (r *Registry) RecordSuccess(providerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entryLocked(providerID)
	e.failures = 0
	e.state = StateClosed
}

func (r *Registry) RecordFailure(providerID string, status int) {
	if !tripStatus(status) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entryLocked(providerID)
	e.failures++
	switch {
	case e.failures >= e.config.FailureThreshold:
		e.state = StateOpen
		e.openedAt = time.Now()
	case e.failures >= e.config.DegradedThreshold:
		e.state = StateDegraded
	}
}

func (r *Registry) Reset(providerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.entries[providerID]; e != nil {
		e.failures = 0
		e.state = StateClosed
	}
}

func (r *Registry) entryLocked(providerID string) *entry {
	e := r.entries[providerID]
	if e == nil {
		e = &entry{state: StateClosed, config: DefaultAPIKeyConfig()}
		r.entries[providerID] = e
	}
	return e
}

func (r *Registry) refreshLocked(e *entry) {
	if e.state != StateOpen {
		return
	}
	if time.Since(e.openedAt) >= e.config.ResetTimeout {
		e.state = StateHalfOpen
	}
}

func tripStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}
