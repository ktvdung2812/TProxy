package store

import (
	"context"
	"fmt"
	"strings"
)

const AppSettingTunnel = "tunnel"

type TunnelSettings struct {
	Enabled               bool   `json:"tunnel_enabled"`
	TunnelURL             string `json:"tunnel_url,omitempty"`
	TunnelToken           string `json:"-"`
	TunnelHostname        string `json:"tunnel_hostname,omitempty"`
	TailscaleEnabled      bool   `json:"tailscale_enabled"`
	TailscaleURL          string `json:"tailscale_url,omitempty"`
	TunnelDashboardAccess bool   `json:"tunnel_dashboard_access"`
}

// TunnelAccessPolicy contains the non-secret settings used to identify a
// public tunnel request and decide whether it may reach management surfaces.
type TunnelAccessPolicy struct {
	TunnelURL             string
	TunnelHostname        string
	TailscaleURL          string
	TunnelDashboardAccess bool
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
	settings := tunnelSettingsFromRaw(raw)
	if value, ok := raw["tunnel_token_ciphertext"].(string); ok && value != "" {
		settings.TunnelToken, err = s.encryptor.Decrypt(value)
		if err != nil {
			return TunnelSettings{}, fmt.Errorf("decrypt Cloudflare Tunnel token: %w", err)
		}
	}
	return settings, nil
}

func (s *Store) TunnelAccessPolicy(ctx context.Context) (TunnelAccessPolicy, error) {
	raw, err := s.GetAppSettingJSON(ctx, AppSettingTunnel)
	if err != nil {
		return TunnelAccessPolicy{}, err
	}
	settings := tunnelSettingsFromRaw(raw)
	return TunnelAccessPolicy{
		TunnelURL:             settings.TunnelURL,
		TunnelHostname:        settings.TunnelHostname,
		TailscaleURL:          settings.TailscaleURL,
		TunnelDashboardAccess: settings.TunnelDashboardAccess,
	}, nil
}

func tunnelSettingsFromRaw(raw map[string]any) TunnelSettings {
	settings := DefaultTunnelSettings()
	if value, ok := raw["tunnel_enabled"].(bool); ok {
		settings.Enabled = value
	}
	if value, ok := raw["tunnel_url"].(string); ok {
		settings.TunnelURL = strings.TrimSpace(value)
	}
	if value, ok := raw["tunnel_hostname"].(string); ok {
		settings.TunnelHostname = strings.TrimSpace(value)
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
	return settings
}

func (s *Store) SaveTunnelSettings(ctx context.Context, settings TunnelSettings) error {
	ciphertext, err := s.encryptor.Encrypt(strings.TrimSpace(settings.TunnelToken))
	if err != nil {
		return fmt.Errorf("encrypt Cloudflare Tunnel token: %w", err)
	}
	return s.SetAppSettingJSON(ctx, AppSettingTunnel, map[string]any{
		"tunnel_enabled":          settings.Enabled,
		"tunnel_url":              strings.TrimSpace(settings.TunnelURL),
		"tunnel_token_ciphertext": ciphertext,
		"tunnel_hostname":         strings.TrimSpace(settings.TunnelHostname),
		"tailscale_enabled":       settings.TailscaleEnabled,
		"tailscale_url":           strings.TrimSpace(settings.TailscaleURL),
		"tunnel_dashboard_access": settings.TunnelDashboardAccess,
	})
}

func (s *Store) DatabasePath() string {
	return s.path
}
