package store

import (
	"context"
	"strings"
)

const AppSettingGateway = "gateway"

type GatewaySettings struct {
	AllowLANManagement bool   `json:"allow_lan_management"`
	PublicBaseURL      string `json:"public_base_url,omitempty"`
	// CCFilterNaming answers Claude Code's housekeeping turns (topic naming, warmup,
	// token counting) locally instead of billing them to an upstream provider.
	CCFilterNaming bool `json:"cc_filter_naming,omitempty"`
}

func DefaultGatewaySettings() GatewaySettings {
	return GatewaySettings{}
}

func (s *Store) GatewaySettings(ctx context.Context) (GatewaySettings, error) {
	raw, err := s.GetAppSettingJSON(ctx, AppSettingGateway)
	if err != nil {
		return GatewaySettings{}, err
	}
	settings := DefaultGatewaySettings()
	if value, ok := raw["allow_lan_management"].(bool); ok {
		settings.AllowLANManagement = value
	}
	if value, ok := raw["public_base_url"].(string); ok {
		settings.PublicBaseURL = strings.TrimSpace(value)
	}
	if value, ok := raw["cc_filter_naming"].(bool); ok {
		settings.CCFilterNaming = value
	}
	return settings, nil
}

func (s *Store) SaveGatewaySettings(ctx context.Context, settings GatewaySettings) error {
	return s.SetAppSettingJSON(ctx, AppSettingGateway, map[string]any{
		"allow_lan_management": settings.AllowLANManagement,
		"public_base_url":      strings.TrimSpace(settings.PublicBaseURL),
		"cc_filter_naming":     settings.CCFilterNaming,
	})
}
