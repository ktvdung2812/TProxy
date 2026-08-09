package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/security"
)

func TestDashboardPasswordDefaultsAndPersist(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "dashboard-auth.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	ctx := context.Background()
	defaults, err := dataStore.DashboardPassword(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if defaults != "" {
		t.Fatalf("default password = %q, want empty", defaults)
	}

	pwd, generated, err := dataStore.EnsureDashboardPassword(ctx)
	if err != nil || !generated || pwd != DefaultDashboardPassword {
		t.Fatalf("EnsureDashboardPassword() = (%q, %v, %v)", pwd, generated, err)
	}
	reused, generated, err := dataStore.EnsureDashboardPassword(ctx)
	if err != nil || generated || reused != "" {
		t.Fatalf("second EnsureDashboardPassword() = (%q, %v, %v)", reused, generated, err)
	}
	verified, err := dataStore.VerifyDashboardPassword(ctx, DefaultDashboardPassword)
	if err != nil || !verified {
		t.Fatalf("VerifyDashboardPassword(default) = (%v, %v)", verified, err)
	}
	if wrong, err := dataStore.VerifyDashboardPassword(ctx, "wrong-password"); err != nil || wrong {
		t.Fatalf("VerifyDashboardPassword(wrong) = (%v, %v)", wrong, err)
	}

	if err := dataStore.SaveDashboardPassword(ctx, "new-secret"); err != nil {
		t.Fatal(err)
	}
	updated, err := dataStore.VerifyDashboardPassword(ctx, "new-secret")
	if err != nil || !updated {
		t.Fatalf("updated password verification = (%v, %v)", updated, err)
	}
	var raw string
	if err := dataStore.db.QueryRowContext(ctx, `SELECT value_json FROM app_settings WHERE key=?`, AppSettingDashboardAuth).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, `"password"`) {
		t.Fatalf("dashboard password stored in plaintext: %s", raw)
	}
}

func TestEnsureDashboardPasswordMigratesLegacyPlaintext(t *testing.T) {
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "dashboard-auth.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	ctx := context.Background()
	if err := dataStore.SetAppSettingJSON(ctx, AppSettingDashboardAuth, map[string]any{"password": "legacy-password"}); err != nil {
		t.Fatal(err)
	}
	if password, generated, err := dataStore.EnsureDashboardPassword(ctx); err != nil || generated || password != "" {
		t.Fatalf("EnsureDashboardPassword legacy = (%q, %v, %v)", password, generated, err)
	}
	if verified, err := dataStore.VerifyDashboardPassword(ctx, "legacy-password"); err != nil || !verified {
		t.Fatalf("legacy password verification = (%v, %v)", verified, err)
	}
	var raw string
	if err := dataStore.db.QueryRowContext(ctx, `SELECT value_json FROM app_settings WHERE key=?`, AppSettingDashboardAuth).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, `"password"`) {
		t.Fatalf("legacy plaintext password remains in setting: %s", raw)
	}
}
