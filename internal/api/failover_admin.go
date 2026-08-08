package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/resilience"
)

type failoverResetPayload struct {
	ModelID    string `json:"model_id,omitempty"`
	ProviderID string `json:"provider_id,omitempty"`
}

type failoverStateView struct {
	resilience.Snapshot
	// RetryInSeconds is how long the dashboard should wait before the provider is
	// probed again. Zero when the provider is not currently blocked.
	RetryInSeconds int `json:"retry_in_seconds"`
}

// adminFailover reports which providers have been pulled out of a model's
// priority chain by repeated failures. The Provider Priority Manager uses it to
// show why a high-priority provider is not serving traffic.
func (s *Server) adminFailover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	modelID := strings.TrimSpace(r.URL.Query().Get("model_id"))
	now := time.Now()
	states := s.router.CircuitBreakers().Snapshot(modelID)
	views := make([]failoverStateView, 0, len(states))
	for _, state := range states {
		view := failoverStateView{Snapshot: state}
		if state.State == resilience.StateOpen && state.RetryAt.After(now) {
			view.RetryInSeconds = int(state.RetryAt.Sub(now).Round(time.Second).Seconds())
		}
		views = append(views, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"failover": views})
}

// adminFailoverReset puts a provider back into rotation immediately instead of
// waiting out its backoff. An empty body clears every tracked circuit.
func (s *Server) adminFailoverReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	var payload failoverResetPayload
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&payload); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	modelID := strings.TrimSpace(payload.ModelID)
	providerID := strings.TrimSpace(payload.ProviderID)
	breakers := s.router.CircuitBreakers()
	switch {
	case modelID != "" && providerID != "":
		breakers.Reset(modelID, providerID)
	case providerID != "":
		breakers.ResetProvider(providerID)
	default:
		breakers.ResetAll()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"model_id":    modelID,
		"provider_id": providerID,
	})
}
