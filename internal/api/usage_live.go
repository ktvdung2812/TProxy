package api

import (
	"sync"
	"time"
)

type liveUsageRequest struct {
	RequestID    string `json:"request_id"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	CredentialID string `json:"credential_id,omitempty"`
	Account      string `json:"account,omitempty"`
}

type liveUsageSnapshot struct {
	ActiveRequests []liveUsageRequest `json:"activeRequests"`
	ErrorProvider  string             `json:"errorProvider"`
}

type LiveUsageTracker struct {
	mu            sync.Mutex
	active        map[string]liveUsageRequest
	errorProvider string
	errorAt       time.Time
}

func NewLiveUsageTracker() *LiveUsageTracker {
	return &LiveUsageTracker{active: make(map[string]liveUsageRequest)}
}

func (t *LiveUsageTracker) Begin(requestID, provider, model, credentialID, account string) {
	if requestID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.active[requestID] = liveUsageRequest{
		RequestID:    requestID,
		Provider:     provider,
		Model:        model,
		CredentialID: credentialID,
		Account:      account,
	}
}

func (t *LiveUsageTracker) End(requestID string) {
	if requestID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.active, requestID)
}

func (t *LiveUsageTracker) RecordError(provider string) {
	if provider == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.errorProvider = provider
	t.errorAt = time.Now().UTC()
}

func (t *LiveUsageTracker) Snapshot() liveUsageSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	items := make([]liveUsageRequest, 0, len(t.active))
	for _, item := range t.active {
		items = append(items, item)
	}
	errorProvider := ""
	if !t.errorAt.IsZero() && time.Since(t.errorAt) < 2*time.Minute {
		errorProvider = t.errorProvider
	}
	return liveUsageSnapshot{ActiveRequests: items, ErrorProvider: errorProvider}
}
