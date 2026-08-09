package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/tproxy/tproxy/internal/netutil"
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
	s.ccFilterNaming.Store(settings.CCFilterNaming)
}

func (s *Server) managementClientAllowed(r *http.Request) bool {
	if s.managementRequestViaTunnel(r) {
		return s.tunnelDashboardAccessAllowed(r)
	}
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

func (s *Server) isLocalManagementRequest(r *http.Request) bool {
	return security.IsLoopback(r) && !s.managementRequestViaTunnel(r)
}

// managementRequestViaTunnel identifies requests addressed to a tunnel URL
// that TProxy persisted when it created a Cloudflare or Tailscale connector.
// Host matching is local provenance: an internet client cannot change the
// request's Host after the connector has selected the configured public URL.
func (s *Server) managementRequestViaTunnel(r *http.Request) bool {
	if r == nil || strings.TrimSpace(r.Host) == "" {
		return false
	}
	settings, err := s.store.TunnelSettings(r.Context())
	if err != nil {
		return false
	}
	requestURL, parseErr := url.Parse("//" + strings.TrimSpace(r.Host))
	if parseErr != nil {
		return false
	}
	requestHost := strings.ToLower(strings.TrimSuffix(requestURL.Hostname(), "."))
	if requestHost == "" {
		return false
	}
	for _, raw := range []string{settings.TunnelURL, settings.TailscaleURL} {
		parsed, parseErr := url.Parse(strings.TrimSpace(raw))
		if parseErr == nil && strings.EqualFold(requestHost, strings.TrimSuffix(parsed.Hostname(), ".")) {
			return true
		}
	}
	return false
}

func (s *Server) tunnelDashboardAccessAllowed(r *http.Request) bool {
	if !s.managementRequestViaTunnel(r) {
		return true
	}
	settings, err := s.store.TunnelSettings(r.Context())
	return err == nil && settings.TunnelDashboardAccess
}

func (s *Server) tunnelDashboard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.tunnelDashboardAccessAllowed(r) {
			writeError(w, http.StatusForbidden, "tunnel_dashboard_disabled", "dashboard access through the tunnel is disabled", useClientRequestID(r))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) adminGatewaySettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := s.store.GatewaySettings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "gateway_settings_failed", err.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, gatewaySettingsPayload(s, settings))
	case http.MethodPut, http.MethodPatch:
		var payload struct {
			AllowLANManagement *bool   `json:"allow_lan_management"`
			PublicBaseURL      *string `json:"public_base_url"`
			CCFilterNaming     *bool   `json:"cc_filter_naming"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error(), useClientRequestID(r))
			return
		}
		if payload.AllowLANManagement == nil && payload.PublicBaseURL == nil && payload.CCFilterNaming == nil {
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
		if payload.CCFilterNaming != nil {
			settings.CCFilterNaming = *payload.CCFilterNaming
		}
		if err := s.store.SaveGatewaySettings(r.Context(), settings); err != nil {
			writeError(w, http.StatusInternalServerError, "gateway_settings_failed", err.Error(), useClientRequestID(r))
			return
		}
		s.allowLanMgmt = settings.AllowLANManagement
		s.ccFilterNaming.Store(settings.CCFilterNaming)
		response := gatewaySettingsPayload(s, settings)
		response["ok"] = true
		writeJSON(w, http.StatusOK, response)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET/PUT/PATCH required", useClientRequestID(r))
	}
}

func (s *Server) clientFacingPort() int {
	if raw := strings.TrimSpace(os.Getenv("TPROXY_PUBLIC_PORT")); raw != "" {
		if port, err := strconv.Atoi(raw); err == nil && port >= 1 && port <= 65535 {
			return port
		}
	}
	return s.cfg.Server.Port
}

func lanIPsForGateway(allowLAN bool) []string {
	if !allowLAN {
		return []string{}
	}
	return netutil.LANIPv4Addresses()
}

func gatewaySettingsPayload(s *Server, settings store.GatewaySettings) map[string]any {
	payload := map[string]any{
		"allow_lan_management": settings.AllowLANManagement,
		"public_base_url":      settings.PublicBaseURL,
		"cc_filter_naming":     settings.CCFilterNaming,
		"server_host":          s.cfg.Server.Host,
		"server_port":          s.clientFacingPort(),
		"restart_required":     settings.AllowLANManagement && isLoopbackBindHost(s.cfg.Server.Host),
	}
	if settings.AllowLANManagement {
		payload["lan_ips"] = lanIPsForGateway(true)
	}
	return payload
}

func isLoopbackBindHost(host string) bool {
	switch host {
	case "", "127.0.0.1", "localhost", "::1":
		return true
	default:
		return false
	}
}
