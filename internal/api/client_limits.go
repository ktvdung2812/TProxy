package api

import (
	"net/http"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/router"
	"github.com/tproxy/tproxy/internal/store"
)

// clientLimits reads the authenticated key's capacity without spending RPM,
// advancing account rotation, or probing an upstream provider.
func (s *Server) clientLimits(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	key, _ := r.Context().Value(apiKeyContext).(*store.APIKey)
	// Local inference may allow anonymous access, but limits always belong to
	// an authenticated key rather than exposing the unrestricted gateway pool.
	if key == nil {
		writeError(w, http.StatusUnauthorized, "missing_api_key", "client API key is required", useClientRequestID(r))
		return
	}
	model := resolveIngressModel(r, r.URL.Query().Get("model"))
	request := canonical.Request{PublicModelID: model}
	if model != "" {
		s.applyMappingIngress(r, &request)
	}
	capacity, err := s.keyCapacity(r.Context(), key, request.PublicModelID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "capacity_error", "could not read API key limits", useClientRequestID(r))
		return
	}
	// Diagnostic errors and reset-credit redemption details belong to the
	// management API. CachedQuota returns copies, so this leaves its cache intact.
	for _, snapshot := range capacity.QuotaWindows {
		if snapshot.Quota != nil {
			snapshot.Quota.Message = ""
			snapshot.Quota.ResetCredits = nil
		}
	}
	writeJSON(w, http.StatusOK, struct {
		Object   string `json:"object"`
		APIKeyID string `json:"api_key_id"`
		Model    string `json:"model,omitempty"`
		router.CapacitySnapshot
	}{"tproxy.limits", key.ID, model, capacity})
}
