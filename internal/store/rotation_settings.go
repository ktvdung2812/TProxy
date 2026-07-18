package store

import (
	"context"
	"encoding/json"
	"strings"
)

const AppSettingAccountRotation = "account_rotation"

type ProviderRotationStrategy struct {
	Strategy              string `json:"strategy,omitempty"`
	StickyRoundRobinLimit int    `json:"sticky_round_robin_limit,omitempty"`
}

type AccountRotationSettings struct {
	Strategy              string                              `json:"strategy,omitempty"`
	StickyRoundRobinLimit int                                 `json:"sticky_round_robin_limit"`
	ProviderStrategies    map[string]ProviderRotationStrategy `json:"provider_strategies,omitempty"`
}

func DefaultAccountRotationSettings() AccountRotationSettings {
	return AccountRotationSettings{StickyRoundRobinLimit: 3, ProviderStrategies: map[string]ProviderRotationStrategy{}}
}

func (s *Store) AccountRotationSettings(ctx context.Context) (AccountRotationSettings, error) {
	raw, err := s.GetAppSettingJSON(ctx, AppSettingAccountRotation)
	if err != nil {
		return AccountRotationSettings{}, err
	}
	if len(raw) == 0 {
		return DefaultAccountRotationSettings(), nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return AccountRotationSettings{}, err
	}
	settings := DefaultAccountRotationSettings()
	if err := json.Unmarshal(encoded, &settings); err != nil {
		return AccountRotationSettings{}, err
	}
	if settings.StickyRoundRobinLimit <= 0 {
		settings.StickyRoundRobinLimit = 3
	}
	if settings.ProviderStrategies == nil {
		settings.ProviderStrategies = map[string]ProviderRotationStrategy{}
	}
	return settings, nil
}

func (s *Store) SaveAccountRotationSettings(ctx context.Context, settings AccountRotationSettings) error {
	if settings.StickyRoundRobinLimit <= 0 {
		settings.StickyRoundRobinLimit = 3
	}
	if settings.ProviderStrategies == nil {
		settings.ProviderStrategies = map[string]ProviderRotationStrategy{}
	}
	payload := map[string]any{
		"sticky_round_robin_limit": settings.StickyRoundRobinLimit,
		"provider_strategies":      settings.ProviderStrategies,
	}
	if settings.Strategy != "" {
		payload["strategy"] = settings.Strategy
	}
	return s.SetAppSettingJSON(ctx, AppSettingAccountRotation, payload)
}

func (s *Store) ResetProviderRotationState(ctx context.Context, providerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(providerID) == "" {
		_, err := s.db.ExecContext(ctx, `UPDATE credentials SET last_used_at='', consecutive_use_count=0`)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE credentials SET last_used_at='', consecutive_use_count=0 WHERE provider_id=?`, providerID)
	return err
}

func (s *Store) TouchCredentialRotation(ctx context.Context, credentialID string, consecutiveUseCount int, usedAt string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `UPDATE credentials SET last_used_at=?, consecutive_use_count=? WHERE id=?`, usedAt, consecutiveUseCount, credentialID)
	return err
}

func (s *Store) ResetCredentialRotationState(ctx context.Context, credentialID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `UPDATE credentials SET last_used_at='', consecutive_use_count=0 WHERE id=?`, credentialID)
	return err
}
