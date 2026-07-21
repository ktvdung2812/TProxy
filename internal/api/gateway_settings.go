package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/tproxy/tproxy/internal/security"
	"github.com/tproxy/tproxy/internal/store"
)

func (s *Server) loadGatewaySettings(ctx context.Context) {
	settings, err := s.store.GatewaySettings(ctx)
	if err != nil {
		s.allowLanMgmt = false
		return
	}
	s.allowLanMgmt = settings.AllowLANManagement
}

func (s *Server) managementClientAllowed(r *http.Request) bool {
	if security.IsLoopback(r) {
		return true
	}
	if s.allowRemoteMgmt {
		return true
	}
	if s.allowLanMgmt && security.IsPrivateNetwork(r) {
		return true
	}
	return false
}

func (s *Server) adminGatewaySettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := s.store.GatewaySettings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "gateway_settings_failed", err.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"allow_lan_management": settings.AllowLANManagement,
			"public_base_url":      settings.PublicBaseURL,
			"server_host":          s.cfg.Server.Host,
			"server_port":          s.cfg.Server.Port,
		})
	case http.MethodPut, http.MethodPatch:
		var payload struct {
			AllowLANManagement *bool   `json:"allow_lan_management"`
			PublicBaseURL      *string `json:"public_base_url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error(), useClientRequestID(r))
			return
		}
		if payload.AllowLANManagement == nil && payload.PublicBaseURL == nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "at least one gateway setting is required", useClientRequestID(r))
			return
		}
		settings, err := s.store.GatewaySettings(r.Context())
		if err != nil {
			settings = store.DefaultGatewaySettings()
		}
		if payload.AllowLANManagement != nil {
			settings.AllowLANManagement = *payload.AllowLANManagement
		}
		if payload.PublicBaseURL != nil {
			settings.PublicBaseURL = strings.TrimSpace(*payload.PublicBaseURL)
		}
		if err := s.store.SaveGatewaySettings(r.Context(), settings); err != nil {
			writeError(w, http.StatusInternalServerError, "gateway_settings_failed", err.Error(), useClientRequestID(r))
			return
		}
		s.allowLanMgmt = settings.AllowLANManagement
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":                   true,
			"allow_lan_management": settings.AllowLANManagement,
			"public_base_url":      settings.PublicBaseURL,
			"server_host":          s.cfg.Server.Host,
			"server_port":          s.cfg.Server.Port,
			"restart_required":     settings.AllowLANManagement && isLoopbackBindHost(s.cfg.Server.Host),
		})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET/PUT/PATCH required", useClientRequestID(r))
	}
}

func isLoopbackBindHost(host string) bool {
	switch host {
	case "", "127.0.0.1", "localhost", "::1":
		return true
	default:
		return false
	}
}
