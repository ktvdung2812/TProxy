package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/security"
	"github.com/tproxy/tproxy/internal/store"
)

type quotaResetCall struct {
	done    chan struct{}
	result  providers.CodexResetConsumeResult
	err     error
	expires time.Time
}
type quotaResetState struct {
	mu     sync.Mutex
	calls  map[string]*quotaResetCall
	active map[string]bool
}

// Reset retries with the same ID reuse the same upstream redemption ID. Never
// retry an ambiguous consumption with a newly generated ID.
func (s *Server) consumeQuotaReset(ctx context.Context, provider store.Provider, credential store.Credential, id string) (providers.CodexResetConsumeResult, error) {
	return s.quotaResets.consume(ctx, credential.ID, id, func(id string) (providers.CodexResetConsumeResult, error) {
		return s.router.ConsumeCodexResetCreditWithID(ctx, provider, credential, id)
	})
}

func (q *quotaResetState) consume(ctx context.Context, credentialID, id string, consume func(string) (providers.CodexResetConsumeResult, error)) (providers.CodexResetConsumeResult, error) {
	if id == "" {
		id = uuid.NewString()
	}
	if _, err := uuid.Parse(id); err != nil {
		return providers.CodexResetConsumeResult{}, &providers.ProviderError{Status: 400, Code: "invalid_idempotency_key", Message: "Idempotency-Key must be a UUID"}
	}
	key := credentialID + ":" + id
	q.mu.Lock()
	if q.calls == nil {
		q.calls = map[string]*quotaResetCall{}
		q.active = map[string]bool{}
	}
	for key, call := range q.calls {
		if !call.expires.IsZero() && time.Now().After(call.expires) {
			delete(q.calls, key)
		}
	}
	if call := q.calls[key]; call != nil {
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return providers.CodexResetConsumeResult{}, ctx.Err()
		case <-call.done:
			return call.result, call.err
		}
	}
	if q.active[credentialID] || len(q.calls) >= 1024 {
		q.mu.Unlock()
		return providers.CodexResetConsumeResult{}, &providers.ProviderError{Status: 409, Code: "reset_in_progress", Message: "reset is already running or the reset retry cache is full; retry later"}
	}
	call := &quotaResetCall{done: make(chan struct{})}
	q.calls[key] = call
	q.active[credentialID] = true
	q.mu.Unlock()
	result, err := consume(id)
	q.mu.Lock()
	call.result = result
	call.err = err
	call.expires = time.Now().Add(15 * time.Minute)
	delete(q.active, credentialID)
	close(call.done)
	q.mu.Unlock()
	return result, err
}

type quotaActionResult struct {
	CredentialID  string                     `json:"credential_id"`
	OK            bool                       `json:"ok"`
	ActionApplied bool                       `json:"action_applied"`
	Error         string                     `json:"error,omitempty"`
	Quota         *providers.CredentialQuota `json:"quota,omitempty"`
}

func (s *Server) adminQuotaActions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method_not_allowed", "POST required", "")
		return
	}
	var payload struct {
		Action        string   `json:"action"`
		CredentialIDs []string `json:"credential_ids"`
		RequestID     string   `json:"request_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&payload) != nil || len(payload.CredentialIDs) == 0 || len(payload.CredentialIDs) > 100 {
		writeError(w, 400, "invalid_request", "provide between 1 and 100 credential IDs", "")
		return
	}
	if payload.Action != "refresh" && payload.Action != "clear-cooldown" && payload.Action != "reset-upstream" {
		writeError(w, 400, "invalid_action", "unknown quota action", "")
		return
	}
	if payload.Action == "reset-upstream" {
		if _, err := uuid.Parse(payload.RequestID); err != nil {
			writeError(w, 400, "invalid_request", "reset requires a UUID request_id", "")
			return
		}
	}
	// Validate the complete scope before the first mutation or upstream request.
	credentials := make([]store.Credential, 0, len(payload.CredentialIDs))
	providerByID := map[string]store.Provider{}
	seen := map[string]bool{}
	for _, id := range payload.CredentialIDs {
		if seen[id] || strings.TrimSpace(id) == "" {
			writeError(w, 400, "invalid_request", "duplicate or empty credential ID", "")
			return
		}
		seen[id] = true
		credential, err := s.store.CredentialByID(r.Context(), id)
		if err != nil {
			writeError(w, 404, "credential_not_found", id, "")
			return
		}
		provider, err := s.store.Provider(r.Context(), credential.ProviderID)
		if err != nil {
			writeError(w, 404, "provider_not_found", credential.ProviderID, "")
			return
		}
		if payload.Action == "reset-upstream" && provider.Type != "codex" {
			writeError(w, 400, "unsupported_reset", "upstream reset is supported only for Codex", "")
			return
		}
		credentials = append(credentials, credential)
		providerByID[provider.ID] = *provider
	}
	results := make([]quotaActionResult, 0, len(credentials))
	// Serial execution bounds upstream load and stops dispatching on cancellation.
	batchCtx, batchCancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer batchCancel()
	for _, credential := range credentials {
		if batchCtx.Err() != nil {
			results = append(results, quotaActionResult{CredentialID: credential.ID, Error: batchCtx.Err().Error()})
			continue
		}
		result := quotaActionResult{CredentialID: credential.ID}
		provider := providerByID[credential.ProviderID]
		ctx, cancel := context.WithTimeout(batchCtx, 30*time.Second)
		var err error
		if payload.Action == "clear-cooldown" {
			err = s.store.ClearCooldown(ctx, credential.ID)
			result.ActionApplied = err == nil
		}
		if err == nil && (credential.AuthType == "oauth" || credential.AuthType == "service_account") {
			credential, err = s.auth.EnsureValid(ctx, provider, credential, false)
		}
		if err == nil && payload.Action == "reset-upstream" {
			digest := sha256.Sum256([]byte(payload.RequestID + ":" + credential.ID))
			id, _ := uuid.FromBytes(digest[:16])
			var reset providers.CodexResetConsumeResult
			reset, err = s.consumeQuotaReset(ctx, provider, credential, id.String())
			result.ActionApplied = reset.OK && reset.Reset
			if err == nil && !result.ActionApplied {
				err = fmt.Errorf("upstream did not confirm a reset")
			}
		}
		if err == nil {
			quota, quotaErr := s.router.CredentialQuota(ctx, provider, credential)
			err = quotaErr
			if err == nil {
				result.Quota = &quota
				if len(quota.Quotas) == 0 {
					err = fmt.Errorf("quota unavailable: %s", quota.Message)
				} else {
					result.OK = true
					if payload.Action == "refresh" {
						result.ActionApplied = true
					}
				}
			}
		}
		if err != nil {
			result.Error = security.RedactText(err.Error())
		}
		cancel()
		results = append(results, result)
	}
	writeJSON(w, 200, map[string]any{"results": results, "action": payload.Action})
}
