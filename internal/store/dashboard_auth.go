package store

import (
	"context"
	"errors"
	"strings"
)

const AppSettingDashboardAuth = "dashboard_auth"

// DefaultDashboardPassword is created for a new local installation. Users must
// change it before enabling management access outside loopback.
const DefaultDashboardPassword = "123123"

func (s *Store) DashboardPassword(ctx context.Context) (string, error) {
	raw, err := s.GetAppSettingJSON(ctx, AppSettingDashboardAuth)
	if err != nil {
		return "", err
	}
	if pwd := strings.TrimSpace(stringFromAny(raw["password"])); pwd != "" {
		return pwd, nil
	}
	return "", nil
}

func (s *Store) EnsureDashboardPassword(ctx context.Context) (password string, generated bool, err error) {
	saved, err := s.DashboardPasswordSaved(ctx)
	if err != nil {
		return "", false, err
	}
	if saved {
		pwd, loadErr := s.DashboardPassword(ctx)
		return pwd, false, loadErr
	}
	pwd := DefaultDashboardPassword
	if err := s.SaveDashboardPassword(ctx, pwd); err != nil {
		return "", false, err
	}
	return pwd, true, nil
}

func (s *Store) DashboardPasswordSaved(ctx context.Context) (bool, error) {
	raw, err := s.GetAppSettingJSON(ctx, AppSettingDashboardAuth)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(stringFromAny(raw["password"])) != "", nil
}

func (s *Store) SaveDashboardPassword(ctx context.Context, password string) error {
	pwd := strings.TrimSpace(password)
	if pwd == "" {
		return errors.New("password required")
	}
	return s.SetAppSettingJSON(ctx, AppSettingDashboardAuth, map[string]any{
		"password": pwd,
	})
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return ""
	}
}
