package store

import (
	"context"
	"path/filepath"
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
	if err != nil || generated || reused != DefaultDashboardPassword {
		t.Fatalf("second EnsureDashboardPassword() = (%q, %v, %v)", reused, generated, err)
	}

	if err := dataStore.SaveDashboardPassword(ctx, "new-secret"); err != nil {
		t.Fatal(err)
	}
	updated, err := dataStore.DashboardPassword(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if updated != "new-secret" {
		t.Fatalf("updated password = %q", updated)
	}
}
