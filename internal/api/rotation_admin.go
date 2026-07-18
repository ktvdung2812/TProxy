package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/tproxy/tproxy/internal/router"
	"github.com/tproxy/tproxy/internal/store"
)

type accountRotationPayload struct {
	Strategy              string                                    `json:"strategy"`
	StickyRoundRobinLimit int                                       `json:"sticky_round_robin_limit"`
	ProviderStrategies    map[string]store.ProviderRotationStrategy `json:"provider_strategies,omitempty"`
}

type accountRotationResetPayload struct {
	ProviderID string `json:"provider_id,omitempty"`
}

func (s *Server) adminAccountRotation(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.adminGetAccountRotation(w, r)
	case http.MethodPut:
		s.adminPutAccountRotation(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or PUT required", useClientRequestID(r))
	}
}

func (s *Server) adminAccountRotationReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	var payload accountRotationResetPayload
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&payload); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	providerID := strings.TrimSpace(payload.ProviderID)
	if err := s.store.ResetProviderRotationState(r.Context(), providerID); err != nil {
		writeError(w, http.StatusInternalServerError, "rotation_reset_failed", err.Error(), useClientRequestID(r))
		return
	}
	s.router.ResetRotationRuntimeState()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"provider_id": providerID,
	})
}

func (s *Server) adminGetAccountRotation(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.AccountRotationSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error(), useClientRequestID(r))
		return
	}
	strategy := s.cfg.Routing.Strategy
	if strategy == "" {
		strategy = router.StrategyRoundRobin
	}
	if settings.Strategy != "" {
		strategy = settings.Strategy
	}
	stickyLimit := s.cfg.Routing.StickyRoundRobinLimit
	if settings.StickyRoundRobinLimit > 0 {
		stickyLimit = settings.StickyRoundRobinLimit
	}
	if stickyLimit <= 0 {
		stickyLimit = 3
	}
	providerStrategies := map[string]store.ProviderRotationStrategy{}
	for providerID, providerCfg := range s.cfg.Routing.ProviderStrategies {
		providerStrategies[providerID] = store.ProviderRotationStrategy{
			Strategy:              providerCfg.Strategy,
			StickyRoundRobinLimit: providerCfg.StickyRoundRobinLimit,
		}
	}
	for providerID, override := range settings.ProviderStrategies {
		providerStrategies[providerID] = override
	}
	writeJSON(w, http.StatusOK, accountRotationPayload{
		Strategy:              strategy,
		StickyRoundRobinLimit: stickyLimit,
		ProviderStrategies:    providerStrategies,
	})
}

func (s *Server) adminPutAccountRotation(w http.ResponseWriter, r *http.Request) {
	var payload accountRotationPayload
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	if payload.StickyRoundRobinLimit <= 0 {
		payload.StickyRoundRobinLimit = 3
	}
	if payload.ProviderStrategies == nil {
		payload.ProviderStrategies = map[string]store.ProviderRotationStrategy{}
	}
	settings := store.AccountRotationSettings{
		Strategy:              strings.TrimSpace(payload.Strategy),
		StickyRoundRobinLimit: payload.StickyRoundRobinLimit,
		ProviderStrategies:    payload.ProviderStrategies,
	}
	if err := s.store.SaveAccountRotationSettings(r.Context(), settings); err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error(), useClientRequestID(r))
		return
	}
	if payload.Strategy != "" {
		s.cfg.Routing.Strategy = payload.Strategy
		s.cfg.Routing.StickyRoundRobinLimit = payload.StickyRoundRobinLimit
	}
	s.router.ConfigureRouting(s.cfg.Routing)
	if err := s.router.SyncAccountRotationSettings(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "rotation_sync_failed", err.Error(), useClientRequestID(r))
		return
	}
	s.router.ResetRotationRuntimeState()
	s.adminGetAccountRotation(w, r)
}
