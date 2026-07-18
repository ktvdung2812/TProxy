package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

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

func (s *Store) ClaudeAliasOverrides(ctx context.Context) (bridge.Overrides, error) {
	raw, err := s.GetAppSettingJSON(ctx, bridge.AppSettingClaudeAliases)
	if err != nil {
		return nil, err
	}
	overrides := bridge.Overrides{}
	for _, role := range bridge.Roles {
		if value, ok := raw[string(role)].(string); ok && value != "" {
			overrides[role] = value
		}
	}
	return overrides, nil
}

func (s *Store) SaveClaudeAliasOverrides(ctx context.Context, overrides bridge.Overrides) error {
	payload := map[string]any{}
	for _, role := range bridge.Roles {
		if value := overrides[role]; value != "" {
			payload[string(role)] = value
		}
	}
	return s.SetAppSettingJSON(ctx, bridge.AppSettingClaudeAliases, payload)
}
