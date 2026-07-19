package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tproxy/tproxy/internal/security"
)

func TestGatewaySettingsDefaultsAndPersist(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "gateway-settings.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	ctx := context.Background()
	defaults, err := dataStore.GatewaySettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.AllowLANManagement {
		t.Fatalf("defaults = %+v", defaults)
	}

	if err := dataStore.SaveGatewaySettings(ctx, GatewaySettings{AllowLANManagement: true}); err != nil {
		t.Fatal(err)
	}
	updated, err := dataStore.GatewaySettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.AllowLANManagement {
		t.Fatalf("updated = %+v", updated)
	}
}
