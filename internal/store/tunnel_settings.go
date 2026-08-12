package store

import (
	"context"
	"strings"
)

const AppSettingTunnel = "tunnel"

type TunnelSettings struct {
	Enabled               bool   `json:"tunnel_enabled"`
	TunnelURL             string `json:"tunnel_url,omitempty"`
	TailscaleEnabled      bool   `json:"tailscale_enabled"`
	TailscaleURL          string `json:"tailscale_url,omitempty"`
	TunnelDashboardAccess bool   `json:"tunnel_dashboard_access"`
}

func DefaultTunnelSettings() TunnelSettings {
	// Public connectors must not expose the dashboard until the operator
	// explicitly enables it and configures a dedicated management secret.
	return TunnelSettings{TunnelDashboardAccess: false}
}

func (s *Store) TunnelSettings(ctx context.Context) (TunnelSettings, error) {
	raw, err := s.GetAppSettingJSON(ctx, AppSettingTunnel)
	if err != nil {
		return TunnelSettings{}, err
	}
	settings := DefaultTunnelSettings()
	if value, ok := raw["tunnel_enabled"].(bool); ok {
		settings.Enabled = value
	}
	if value, ok := raw["tunnel_url"].(string); ok {
		settings.TunnelURL = strings.TrimSpace(value)
	}
	if value, ok := raw["tailscale_enabled"].(bool); ok {
		settings.TailscaleEnabled = value
	}
	if value, ok := raw["tailscale_url"].(string); ok {
		settings.TailscaleURL = strings.TrimSpace(value)
	}
	if value, ok := raw["tunnel_dashboard_access"].(bool); ok {
		settings.TunnelDashboardAccess = value
	}
	return settings, nil
}

func (s *Store) SaveTunnelSettings(ctx context.Context, settings TunnelSettings) error {
	return s.SetAppSettingJSON(ctx, AppSettingTunnel, map[string]any{
		"tunnel_enabled":          settings.Enabled,
		"tunnel_url":              strings.TrimSpace(settings.TunnelURL),
		"tailscale_enabled":       settings.TailscaleEnabled,
		"tailscale_url":           strings.TrimSpace(settings.TailscaleURL),
		"tunnel_dashboard_access": settings.TunnelDashboardAccess,
	})
}

func (s *Store) DatabasePath() string {
	return s.path
}
