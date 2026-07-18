package store

import "context"

const AppSettingTokenSaver = "token_saver"

type TokenSaverSettings struct {
	RTKEnabled          bool `json:"rtk_enabled"`
	PerRequestOptOut    bool `json:"per_request_opt_out"`
	CLIHookRecommended  bool `json:"cli_hook_recommended"`
}

func DefaultTokenSaverSettings() TokenSaverSettings {
	return TokenSaverSettings{
		RTKEnabled:         true,
		PerRequestOptOut:   true,
		CLIHookRecommended: true,
	}
}

func (s *Store) TokenSaverSettings(ctx context.Context) (TokenSaverSettings, error) {
	raw, err := s.GetAppSettingJSON(ctx, AppSettingTokenSaver)
	if err != nil {
		return TokenSaverSettings{}, err
	}
	settings := DefaultTokenSaverSettings()
	if value, ok := raw["rtk_enabled"].(bool); ok {
		settings.RTKEnabled = value
	}
	if value, ok := raw["per_request_opt_out"].(bool); ok {
		settings.PerRequestOptOut = value
	}
	if value, ok := raw["cli_hook_recommended"].(bool); ok {
		settings.CLIHookRecommended = value
	}
	return settings, nil
}

func (s *Store) SaveTokenSaverSettings(ctx context.Context, settings TokenSaverSettings) error {
	return s.SetAppSettingJSON(ctx, AppSettingTokenSaver, map[string]any{
		"rtk_enabled":           settings.RTKEnabled,
		"per_request_opt_out":   settings.PerRequestOptOut,
		"cli_hook_recommended":  settings.CLIHookRecommended,
	})
}
