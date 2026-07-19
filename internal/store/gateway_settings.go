package store

import "context"

const AppSettingGateway = "gateway"

type GatewaySettings struct {
	AllowLANManagement bool `json:"allow_lan_management"`
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
	return settings, nil
}

func (s *Store) SaveGatewaySettings(ctx context.Context, settings GatewaySettings) error {
	return s.SetAppSettingJSON(ctx, AppSettingGateway, map[string]any{
		"allow_lan_management": settings.AllowLANManagement,
	})
}
