package store

import (
	"context"
	"errors"
	"strings"

	"github.com/tproxy/tproxy/internal/security"
)

const AppSettingDashboardAuth = "dashboard_auth"

// DefaultDashboardPassword is created for a new local installation. Users must
// change it before enabling management access outside loopback.
const DefaultDashboardPassword = "123123"

func (s *Store) DashboardPassword(ctx context.Context) (string, error) {
	// Passwords are write-only. Keep this method for source compatibility, but
	// never return a stored verifier or legacy plaintext value to callers.
	if _, err := s.GetAppSettingJSON(ctx, AppSettingDashboardAuth); err != nil {
		return "", err
	}
	return "", nil
}

func (s *Store) EnsureDashboardPassword(ctx context.Context) (password string, generated bool, err error) {
	raw, err := s.GetAppSettingJSON(ctx, AppSettingDashboardAuth)
	if err != nil {
		return "", false, err
	}
	if strings.TrimSpace(stringFromAny(raw["password_hash"])) != "" {
		return "", false, nil
	}
	// Existing installations may still have the legacy plaintext setting. Migrate
	// it during bootstrap so it is not retained until the next login attempt.
	if legacy := stringFromAny(raw["password"]); legacy != "" {
		if err := s.SaveDashboardPassword(ctx, legacy); err != nil {
			return "", false, err
		}
		return "", false, nil
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
	return strings.TrimSpace(stringFromAny(raw["password_hash"])) != "" || strings.TrimSpace(stringFromAny(raw["password"])) != "", nil
}

func (s *Store) SaveDashboardPassword(ctx context.Context, password string) error {
	pwd := strings.TrimSpace(password)
	if pwd == "" {
		return errors.New("password required")
	}
	hash, err := security.HashPassword(pwd)
	if err != nil {
		return err
	}
	return s.SetAppSettingJSON(ctx, AppSettingDashboardAuth, map[string]any{
		"password_hash": hash,
	})
}

// VerifyDashboardPassword verifies a submitted password and transparently
// migrates legacy plaintext records on the first successful login.
func (s *Store) VerifyDashboardPassword(ctx context.Context, password string) (bool, error) {
	raw, err := s.GetAppSettingJSON(ctx, AppSettingDashboardAuth)
	if err != nil {
		return false, err
	}
	if encoded := strings.TrimSpace(stringFromAny(raw["password_hash"])); encoded != "" {
		return security.VerifyPassword(password, encoded)
	}
	legacy := stringFromAny(raw["password"])
	if legacy == "" || !security.ConstantTimeEqual(password, legacy) {
		return false, nil
	}
	if err := s.SaveDashboardPassword(ctx, password); err != nil {
		return false, err
	}
	return true, nil
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return ""
	}
}
