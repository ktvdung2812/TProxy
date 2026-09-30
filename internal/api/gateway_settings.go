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
		s.setLanManagement(false)
		return
	}
	s.setLanManagement(settings.AllowLANManagement)
	s.ccFilterNaming.Store(settings.CCFilterNaming)
}

func (s *Server) managementClientAllowed(r *http.Request) bool {
	policy, viaTunnel, err := s.managementTunnelPolicy(r)
	if err != nil {
		return false
	}
	if viaTunnel {
		return policy.TunnelDashboardAccess
	}
	if security.IsLoopback(r) {
		return true
	}
	remote, lan := s.managementScopes()
	if remote {
		return true
	}
	if lan && security.IsPrivateNetwork(r) {
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
	_, viaTunnel, err := s.managementTunnelPolicy(r)
	// If the policy cannot be loaded, classify the request as tunnel traffic so
	// callers fail closed instead of granting the loopback exception.
	return err != nil || viaTunnel
}

func (s *Server) tunnelDashboardAccessAllowed(r *http.Request) bool {
	policy, viaTunnel, err := s.managementTunnelPolicy(r)
	return err == nil && (!viaTunnel || policy.TunnelDashboardAccess)
}

func (s *Server) managementTunnelPolicy(r *http.Request) (store.TunnelAccessPolicy, bool, error) {
	if r == nil {
		return store.TunnelAccessPolicy{}, false, nil
	}
	policy, err := s.store.TunnelAccessPolicy(r.Context())
	if err != nil {
		return store.TunnelAccessPolicy{}, false, err
	}
	requestHost := normalizedHostname(r.Host)
	if requestHost == "" {
		return policy, false, nil
	}
	for _, raw := range []string{policy.TunnelURL, policy.TailscaleURL, policy.TunnelHostname} {
		if requestHost == normalizedHostname(raw) {
			return policy, true, nil
		}
	}
	return policy, false, nil
}

func normalizedHostname(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parseValue := raw
	if !strings.Contains(raw, "://") && !strings.HasPrefix(raw, "//") {
		parseValue = "//" + raw
	}
	parsed, err := url.Parse(parseValue)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
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
		s.setLanManagement(settings.AllowLANManagement)
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
	return s.currentConfig().Server.Port
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
		"server_host":          s.currentConfig().Server.Host,
		"server_port":          s.clientFacingPort(),
		"restart_required":     settings.AllowLANManagement && isLoopbackBindHost(s.currentConfig().Server.Host),
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
