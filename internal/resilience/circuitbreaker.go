package resilience

import (
	"net/http"
	"sort"
	"strings"
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
	// DegradedThreshold is the consecutive failure count that flags a provider
	// as unhealthy without taking it out of rotation yet.
	DegradedThreshold int
	// FailureThreshold is the consecutive failure count that opens the circuit,
	// removing the provider from the priority chain until it recovers.
	FailureThreshold int
	// ResetTimeout is how long the circuit stays open before a probe request is
	// allowed through.
	ResetTimeout time.Duration
	// MaxResetTimeout caps the exponential backoff applied when a provider keeps
	// tripping. Zero disables the backoff and every trip waits ResetTimeout.
	MaxResetTimeout time.Duration
}

// DefaultConfig is the failover policy applied to any provider that has no
// explicit override. Three consecutive failures take a provider out of the
// chain, and it is probed again five minutes later.
func DefaultConfig() Config {
	return Config{DegradedThreshold: 2, FailureThreshold: 3, ResetTimeout: 5 * time.Minute, MaxResetTimeout: 30 * time.Minute}
}

func DefaultOAuthConfig() Config {
	return Config{DegradedThreshold: 5, FailureThreshold: 8, ResetTimeout: 60 * time.Second, MaxResetTimeout: 30 * time.Minute}
}

func DefaultAPIKeyConfig() Config {
	return Config{DegradedThreshold: 7, FailureThreshold: 12, ResetTimeout: 30 * time.Second, MaxResetTimeout: 30 * time.Minute}
}

func (c Config) normalized() Config {
	if c.FailureThreshold <= 0 {
		c.FailureThreshold = DefaultConfig().FailureThreshold
	}
	if c.DegradedThreshold <= 0 || c.DegradedThreshold > c.FailureThreshold {
		c.DegradedThreshold = c.FailureThreshold - 1
	}
	if c.DegradedThreshold <= 0 {
		c.DegradedThreshold = c.FailureThreshold
	}
	if c.ResetTimeout <= 0 {
		c.ResetTimeout = DefaultConfig().ResetTimeout
	}
	if c.MaxResetTimeout > 0 && c.MaxResetTimeout < c.ResetTimeout {
		c.MaxResetTimeout = c.ResetTimeout
	}
	return c
}

type entry struct {
	modelID    string
	providerID string
	state      State
	failures   int
	trips      int
	openedAt   time.Time
	retryAt    time.Time
	lastStatus int
	lastError  string
	lastFailAt time.Time
	config     Config
}

// Snapshot is the externally visible failover state of one model/provider pair.
type Snapshot struct {
	ModelID     string    `json:"model_id"`
	ProviderID  string    `json:"provider_id"`
	State       State     `json:"state"`
	Failures    int       `json:"failures"`
	Threshold   int       `json:"threshold"`
	Trips       int       `json:"trips"`
	OpenedAt    time.Time `json:"opened_at,omitempty"`
	RetryAt     time.Time `json:"retry_at,omitempty"`
	LastStatus  int       `json:"last_status,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	LastFailure time.Time `json:"last_failure_at,omitempty"`
}

// Registry tracks failover state per model/provider pair. Scoping to the model
// keeps one bad upstream model from pulling a provider out of rotation for every
// other model it serves.
type Registry struct {
	mu           sync.Mutex
	entries      map[string]*entry
	defaultCfg   Config
	providerCfg  map[string]Config
	disabled     bool
	countAccount bool
}

func NewRegistry() *Registry {
	return &Registry{
		entries:     make(map[string]*entry),
		defaultCfg:  DefaultConfig(),
		providerCfg: make(map[string]Config),
	}
}

func key(modelID, providerID string) string {
	return modelID + "\x00" + providerID
}

// SetDefaultConfig replaces the policy used by providers without an override.
func (r *Registry) SetDefaultConfig(cfg Config) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defaultCfg = cfg.normalized()
	for _, e := range r.entries {
		if _, overridden := r.providerCfg[e.providerID]; !overridden {
			e.config = r.defaultCfg
		}
	}
}

// SetEnabled turns automatic provider failover on or off. When disabled the
// registry keeps counting failures but never blocks traffic, so the state is
// still visible in the dashboard.
func (r *Registry) SetEnabled(enabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.disabled = !enabled
}

// SetCountAccountErrors decides whether per-account failures (429 rate limits and
// 401/403 auth errors) count toward the provider circuit. They are excluded by
// default because accounts already have their own cooldown and token refresh.
func (r *Registry) SetCountAccountErrors(enabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.countAccount = enabled
}

// Configure overrides the policy for a single provider across all models.
func (r *Registry) Configure(providerID string, cfg Config) {
	if providerID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	normalized := cfg.normalized()
	r.providerCfg[providerID] = normalized
	for _, e := range r.entries {
		if e.providerID == providerID {
			e.config = normalized
		}
	}
}

// CanExecute reports whether the provider is still in the priority chain for
// this model. An open circuit that has waited out its backoff flips to half-open
// and lets a probe request through.
func (r *Registry) CanExecute(modelID, providerID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.disabled {
		return true
	}
	e := r.entryLocked(modelID, providerID)
	r.refreshLocked(e, time.Now())
	return e.state != StateOpen
}

func (r *Registry) Status(modelID, providerID string) State {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entryLocked(modelID, providerID)
	r.refreshLocked(e, time.Now())
	return e.state
}

// RetryAt reports when an open circuit will next admit a probe request. It is
// the zero time when the provider is not currently blocked.
func (r *Registry) RetryAt(modelID, providerID string) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entryLocked(modelID, providerID)
	r.refreshLocked(e, time.Now())
	if e.state != StateOpen {
		return time.Time{}
	}
	return e.retryAt
}

// RecordSuccess closes the circuit and clears the failure streak.
func (r *Registry) RecordSuccess(modelID, providerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entryLocked(modelID, providerID)
	e.failures = 0
	e.trips = 0
	e.state = StateClosed
	e.openedAt = time.Time{}
	e.retryAt = time.Time{}
	e.lastStatus = 0
	e.lastError = ""
}

// RecordFailure counts one failed dispatch. Once the consecutive failure count
// reaches the configured threshold the circuit opens and the provider drops out
// of the chain until its backoff expires.
func (r *Registry) RecordFailure(modelID, providerID string, status int, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.tripStatusLocked(status) {
		return
	}
	now := time.Now()
	e := r.entryLocked(modelID, providerID)
	e.failures++
	e.lastStatus = status
	e.lastError = truncate(reason, 300)
	e.lastFailAt = now
	switch {
	case e.failures >= e.config.FailureThreshold:
		if e.state != StateOpen {
			e.trips++
		}
		e.state = StateOpen
		e.openedAt = now
		e.retryAt = now.Add(backoff(e.config, e.trips))
	case e.failures >= e.config.DegradedThreshold:
		e.state = StateDegraded
	}
}

// Reset clears the failover state for one model/provider pair.
func (r *Registry) Reset(modelID, providerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, key(modelID, providerID))
}

// ResetProvider clears the failover state for a provider across every model.
func (r *Registry) ResetProvider(providerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, e := range r.entries {
		if e.providerID == providerID {
			delete(r.entries, k)
		}
	}
}

// ResetAll clears every tracked circuit.
func (r *Registry) ResetAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = make(map[string]*entry)
}

// Snapshot returns the current state of every tracked model/provider pair,
// skipping healthy pairs that have nothing to report. Pass a model ID to scope
// the result to a single model, or an empty string for everything.
func (r *Registry) Snapshot(modelID string) []Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	result := make([]Snapshot, 0, len(r.entries))
	for _, e := range r.entries {
		if modelID != "" && e.modelID != modelID {
			continue
		}
		r.refreshLocked(e, now)
		if e.state == StateClosed && e.failures == 0 {
			continue
		}
		result = append(result, Snapshot{
			ModelID:     e.modelID,
			ProviderID:  e.providerID,
			State:       e.state,
			Failures:    e.failures,
			Threshold:   e.config.FailureThreshold,
			Trips:       e.trips,
			OpenedAt:    e.openedAt,
			RetryAt:     e.retryAt,
			LastStatus:  e.lastStatus,
			LastError:   e.lastError,
			LastFailure: e.lastFailAt,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ModelID != result[j].ModelID {
			return result[i].ModelID < result[j].ModelID
		}
		return result[i].ProviderID < result[j].ProviderID
	})
	return result
}

func (r *Registry) entryLocked(modelID, providerID string) *entry {
	k := key(modelID, providerID)
	e := r.entries[k]
	if e == nil {
		cfg, ok := r.providerCfg[providerID]
		if !ok {
			cfg = r.defaultCfg
		}
		e = &entry{modelID: modelID, providerID: providerID, state: StateClosed, config: cfg.normalized()}
		r.entries[k] = e
	}
	return e
}

func (r *Registry) refreshLocked(e *entry, now time.Time) {
	if e.state != StateOpen {
		return
	}
	if !now.Before(e.retryAt) {
		e.state = StateHalfOpen
	}
}

// backoff grows the open window on repeated trips so a provider that keeps
// failing is probed less and less often, capped by MaxResetTimeout.
func backoff(cfg Config, trips int) time.Duration {
	wait := cfg.ResetTimeout
	if cfg.MaxResetTimeout <= 0 {
		return wait
	}
	for i := 1; i < trips; i++ {
		wait *= 2
		if wait >= cfg.MaxResetTimeout {
			return cfg.MaxResetTimeout
		}
	}
	if wait > cfg.MaxResetTimeout {
		return cfg.MaxResetTimeout
	}
	return wait
}

// tripStatusLocked decides whether an upstream status counts against the
// provider. Infrastructure failures and provider-level 4xx do; per-account
// throttling and auth errors do not, unless explicitly enabled.
func (r *Registry) tripStatusLocked(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusUnauthorized, http.StatusForbidden:
		return r.countAccount
	}
	return tripStatus(status)
}

func tripStatus(status int) bool {
	switch {
	case status == 0:
		// No HTTP response at all: DNS, TCP, TLS or client timeout.
		return true
	case status == http.StatusRequestTimeout:
		return true
	case status == http.StatusPaymentRequired:
		return true
	case status == http.StatusNotFound:
		return true
	case status >= 500:
		return true
	default:
		return false
	}
}

func truncate(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max] + "…"
}
