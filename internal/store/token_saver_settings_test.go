package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tproxy/tproxy/internal/security"
)

func TestTokenSaverSettingsDefaultsAndPersist(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "token-saver.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	ctx := context.Background()
	defaults, err := dataStore.TokenSaverSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !defaults.RTKEnabled || !defaults.PerRequestOptOut {
		t.Fatalf("defaults = %+v", defaults)
	}

	if err := dataStore.SaveTokenSaverSettings(ctx, TokenSaverSettings{RTKEnabled: false, PerRequestOptOut: true}); err != nil {
		t.Fatal(err)
	}
	updated, err := dataStore.TokenSaverSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if updated.RTKEnabled {
		t.Fatalf("updated = %+v", updated)
	}
}
