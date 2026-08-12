package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/security"
)

func liveProviderFromContext(r *http.Request) string {
	if state, ok := r.Context().Value(requestLogContext).(*requestLogState); ok && state.ProviderID != "" {
		return state.ProviderID
	}
	return ""
}

func (s *Server) adminUsageStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "stream_unsupported", "streaming is not supported", useClientRequestID(r))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	lookups := s.usageLookupMaps(r.Context())
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	send := func() bool {
		live := s.liveUsage.Snapshot()
		recent, err := s.store.RecentUsageRequests(r.Context(), 20, lookups)
		if err != nil {
			return false
		}
		payload := map[string]any{
			"activeRequests": live.ActiveRequests,
			"recentRequests": recent,
			"errorProvider":  live.ErrorProvider,
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", encoded); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if !send() {
		return
	}

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !send() {
				return
			}
		}
	}
}

func (s *Server) adminTopology(w http.ResponseWriter, r *http.Request, suffix string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}

	lookups := s.usageLookupMaps(r.Context())
	switch {
	case suffix == "clients" || suffix == "clients/":
		clients, err := s.store.TopologyClients(r.Context(), lookups)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "store_error", err.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, clients)
	case strings.HasPrefix(suffix, "clients/"):
		clientKeyID, err := topologyClientKeyFromPath(suffix)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
			return
		}
		detail, err := s.store.TopologyClientDetail(r.Context(), clientKeyID, lookups)
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found", "client not found", useClientRequestID(r))
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "store_error", err.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, detail)
	default:
		writeError(w, http.StatusNotFound, "not_found", "topology endpoint not found", useClientRequestID(r))
	}
}

func topologyClientKeyFromPath(suffix string) (string, error) {
	raw := strings.TrimPrefix(suffix, "clients/")
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return "", fmt.Errorf("client key id is required")
	}
	parts := strings.Split(raw, "/")
	return parts[0], nil
}

func managementToken(r *http.Request) string {
	return security.BearerToken(r)
}
