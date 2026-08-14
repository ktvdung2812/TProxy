package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/tproxy/tproxy/internal/bridge"
)

func (s *Store) GetAppSettingJSON(ctx context.Context, key string) (map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value_json FROM app_settings WHERE key=?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, err
	}
	if value == nil {
		return map[string]any{}, nil
	}
	return value, nil
}

func (s *Store) SetAppSettingJSON(ctx context.Context, key string, value map[string]any) error {
	if value == nil {
		value = map[string]any{}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.db.ExecContext(ctx, `INSERT INTO app_settings(key,value_json) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json`, key, string(encoded))
	return err
}

type ClaudeAliasSettings struct {
	Models          bridge.Overrides
	ReasoningEffort bridge.ReasoningEffortOverrides
}

func (s *Store) ClaudeAliasSettings(ctx context.Context) (ClaudeAliasSettings, error) {
	raw, err := s.GetAppSettingJSON(ctx, bridge.AppSettingClaudeAliases)
	if err != nil {
		return ClaudeAliasSettings{}, err
	}
	settings := ClaudeAliasSettings{
		Models:          bridge.Overrides{},
		ReasoningEffort: bridge.ReasoningEffortOverrides{},
	}
	for _, role := range bridge.Roles {
		if value, ok := raw[string(role)].(string); ok && value != "" {
			settings.Models[role] = value
		}
	}
	if nested, ok := raw["reasoning_effort"].(map[string]any); ok {
		for _, role := range bridge.Roles {
			if value, ok := nested[string(role)].(string); ok && value != "" {
				settings.ReasoningEffort[role] = value
			}
		}
	}
	return settings, nil
}

func (s *Store) SaveClaudeAliasSettings(ctx context.Context, settings ClaudeAliasSettings) error {
	payload := map[string]any{}
	for _, role := range bridge.Roles {
		if value := settings.Models[role]; value != "" {
			payload[string(role)] = value
		}
	}
	reasoning := map[string]any{}
	for _, role := range bridge.Roles {
		if effort := bridge.NormalizeReasoningEffort(settings.ReasoningEffort[role]); effort != "" {
			reasoning[string(role)] = effort
		}
	}
	if len(reasoning) > 0 {
		payload["reasoning_effort"] = reasoning
	}
	return s.SetAppSettingJSON(ctx, bridge.AppSettingClaudeAliases, payload)
}

func (s *Store) ClaudeAliasOverrides(ctx context.Context) (bridge.Overrides, error) {
	settings, err := s.ClaudeAliasSettings(ctx)
	if err != nil {
		return nil, err
	}
	return settings.Models, nil
}

func (s *Store) SaveClaudeAliasOverrides(ctx context.Context, overrides bridge.Overrides) error {
	settings, err := s.ClaudeAliasSettings(ctx)
	if err != nil {
		return err
	}
	settings.Models = overrides
	return s.SaveClaudeAliasSettings(ctx, settings)
}

// CursorAliasSettings stores free-form Cursor client model → target rewrites.
type CursorAliasSettings struct {
	Models bridge.CursorAliases
}

func (s *Store) CursorAliasSettings(ctx context.Context) (CursorAliasSettings, error) {
	raw, err := s.GetAppSettingJSON(ctx, bridge.AppSettingCursorAliases)
	if err != nil {
		return CursorAliasSettings{}, err
	}
	settings := CursorAliasSettings{Models: bridge.CursorAliases{}}
	// Preferred shape: { "models": { "cursor-fast": "my-virtual" } }
	if nested, ok := raw["models"].(map[string]any); ok {
		for source, value := range nested {
			if target, ok := value.(string); ok && strings.TrimSpace(target) != "" {
				settings.Models[source] = target
			}
		}
	} else {
		// Flat shape for backwards compatibility / simple imports.
		for source, value := range raw {
			if source == "models" {
				continue
			}
			if target, ok := value.(string); ok && strings.TrimSpace(target) != "" {
				settings.Models[source] = target
			}
		}
	}
	settings.Models = bridge.NormalizeCursorAliases(settings.Models)
	return settings, nil
}

func (s *Store) SaveCursorAliasSettings(ctx context.Context, settings CursorAliasSettings) error {
	models := bridge.NormalizeCursorAliases(settings.Models)
	payloadModels := map[string]any{}
	for source, target := range models {
		payloadModels[source] = target
	}
	return s.SetAppSettingJSON(ctx, bridge.AppSettingCursorAliases, map[string]any{
		"models": payloadModels,
	})
}
