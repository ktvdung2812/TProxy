package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/store"
	"github.com/tproxy/tproxy/internal/tunnel"
)

type tunnelSettingsAdapter struct {
	store *store.Store
}

func (a tunnelSettingsAdapter) LoadSettings(ctx context.Context) (tunnel.SettingsSnapshot, error) {
	settings, err := a.store.TunnelSettings(ctx)
	if err != nil {
		return tunnel.SettingsSnapshot{}, err
	}
	return tunnel.SettingsSnapshot{
		Enabled:          settings.Enabled,
		TunnelURL:        settings.TunnelURL,
		TailscaleEnabled: settings.TailscaleEnabled,
		TailscaleURL:     settings.TailscaleURL,
	}, nil
}

func (a tunnelSettingsAdapter) SaveCloudflare(ctx context.Context, enabled bool, tunnelURL string) error {
	settings, err := a.store.TunnelSettings(ctx)
	if err != nil {
		settings = store.DefaultTunnelSettings()
	}
	settings.Enabled = enabled
	settings.TunnelURL = tunnelURL
	return a.store.SaveTunnelSettings(ctx, settings)
}

func (a tunnelSettingsAdapter) SaveTailscale(ctx context.Context, enabled bool, tunnelURL string) error {
	settings, err := a.store.TunnelSettings(ctx)
	if err != nil {
		settings = store.DefaultTunnelSettings()
	}
	settings.TailscaleEnabled = enabled
	settings.TailscaleURL = tunnelURL
	return a.store.SaveTunnelSettings(ctx, settings)
}

func (a tunnelSettingsAdapter) OnPublicURL(ctx context.Context, publicURL string) error {
	publicURL = strings.TrimSpace(publicURL)
	if publicURL == "" {
		return nil
	}
	gateway, err := a.store.GatewaySettings(ctx)
	if err != nil {
		gateway = store.DefaultGatewaySettings()
	}
	gateway.PublicBaseURL = publicURL
	return a.store.SaveGatewaySettings(ctx, gateway)
}

func (s *Server) initTunnelService() {
	if s.tunnel != nil {
		return
	}
	layout := tunnel.DataLayoutFromDatabasePath(s.store.DatabasePath())
	s.tunnel = tunnel.NewService(layout, s.clientFacingPort(), tunnelSettingsAdapter{store: s.store})
}

func (s *Server) adminTunnel(w http.ResponseWriter, r *http.Request) {
	s.initTunnelService()
	switch r.URL.Path {
	case "/api/admin/tunnel/status":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
			return
		}
		tunnelStatus, err := s.tunnel.Status(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "tunnel_status_failed", err.Error(), useClientRequestID(r))
			return
		}
		tailscaleStatus, err := s.tunnel.TailscaleStatus(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "tunnel_status_failed", err.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"tunnel":    tunnelStatus,
			"tailscale": tailscaleStatus,
			"download":  s.tunnel.DownloadStatus(),
		})
	case "/api/admin/tunnel/enable":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
			return
		}
		result, err := s.tunnel.Enable(r.Context(), s.clientFacingPort())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "tunnel_enable_failed", err.Error(), useClientRequestID(r))
			return
		}
		s.tunnel.ConfigureMonitoringFromSettings(r.Context())
		time.Sleep(8 * time.Second)
		writeJSON(w, http.StatusOK, result)
	case "/api/admin/tunnel/disable":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
			return
		}
		if err := s.tunnel.Disable(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "tunnel_disable_failed", err.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	case "/api/admin/tunnel/tailscale-check":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, s.tunnel.TailscaleCheckResponse())
	case "/api/admin/tunnel/tailscale-enable":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
			return
		}
		result, err := s.tunnel.EnableTailscale(r.Context(), s.clientFacingPort())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "tailscale_enable_failed", err.Error(), useClientRequestID(r))
			return
		}
		s.tunnel.ConfigureMonitoringFromSettings(r.Context())
		writeJSON(w, http.StatusOK, result)
	case "/api/admin/tunnel/tailscale-disable":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
			return
		}
		if err := s.tunnel.DisableTailscale(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "tailscale_disable_failed", err.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	case "/api/admin/tunnel/dashboard-access":
		switch r.Method {
		case http.MethodGet:
			settings, err := s.store.TunnelSettings(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "tunnel_settings_failed", err.Error(), useClientRequestID(r))
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"tunnel_dashboard_access": settings.TunnelDashboardAccess})
		case http.MethodPatch, http.MethodPut:
			var payload struct {
				TunnelDashboardAccess *bool `json:"tunnel_dashboard_access"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&payload); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_json", err.Error(), useClientRequestID(r))
				return
			}
			if payload.TunnelDashboardAccess == nil {
				writeError(w, http.StatusBadRequest, "invalid_request", "tunnel_dashboard_access is required", useClientRequestID(r))
				return
			}
			settings, err := s.store.TunnelSettings(r.Context())
			if err != nil {
				settings = store.DefaultTunnelSettings()
			}
			settings.TunnelDashboardAccess = *payload.TunnelDashboardAccess
			if err := s.store.SaveTunnelSettings(r.Context(), settings); err != nil {
				writeError(w, http.StatusInternalServerError, "tunnel_settings_failed", err.Error(), useClientRequestID(r))
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"tunnel_dashboard_access": settings.TunnelDashboardAccess})
		default:
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET/PATCH required", useClientRequestID(r))
		}
	default:
		writeError(w, http.StatusNotFound, "not_found", "tunnel endpoint not found", useClientRequestID(r))
	}
}
