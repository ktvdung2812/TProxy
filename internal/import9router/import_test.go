package import9router_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/import9router"
	"github.com/tproxy/tproxy/internal/security"
	"github.com/tproxy/tproxy/internal/store"
)

func TestImport9routerBackupDryRun(t *testing.T) {
	dataStore := openTestStore(t)
	seedProvider(t, dataStore, "codex", "codex")

	payload := []byte(`{
	  "providerConnections": [
	    {
	      "id": "cred-1",
	      "provider": "codex",
	      "authType": "oauth",
	      "name": "Codex Account",
	      "email": "user@example.com",
	      "priority": 1,
	      "isActive": true,
	      "accessToken": "access-token",
	      "refreshToken": "refresh-token",
	      "expiresAt": "2026-12-31T00:00:00Z"
	    }
	  ],
	  "apiKeys": [
	    {"id": "key-1", "key": "sk-test-key", "name": "Test", "isActive": true}
	  ],
	  "combos": [
	    {"id": "combo-1", "name": "claude-combo1", "models": ["cx/gpt-5.5", "cx/gpt-5.4"]}
	  ]
	}`)

	result, err := import9router.Import(context.Background(), dataStore, payload, import9router.Options{DryRun: true})
	if err != nil {
		t.Fatalf("import dry run: %v", err)
	}
	if !result.OK {
		t.Fatalf("result = %+v", result)
	}
	if result.Counts.Credentials != 1 || result.Counts.APIKeys != 1 || result.Counts.Combos != 1 || result.Counts.Models != 2 {
		t.Fatalf("counts = %+v", result.Counts)
	}
}

func TestImport9routerBackupApplies(t *testing.T) {
	dataStore := openTestStore(t)
	seedProvider(t, dataStore, "codex", "codex")

	payload := []byte(`{
	  "apiKeys": [
	    {"id": "imported-key", "key": "sk-imported-key", "name": "Imported", "isActive": true}
	  ],
	  "combos": [
	    {"id": "combo-1", "name": "demo-combo", "models": ["cx/gpt-5.4-mini"]}
	  ]
	}`)

	result, err := import9router.Import(context.Background(), dataStore, payload, import9router.Options{})
	if err != nil || !result.OK {
		t.Fatalf("import: result=%+v err=%v", result, err)
	}
	keys, err := dataStore.APIKeys(context.Background())
	if err != nil || len(keys) != 1 || keys[0].ID != "imported-key" {
		t.Fatalf("api keys = %+v err=%v", keys, err)
	}
	combos, err := dataStore.Combos(context.Background())
	if err != nil || len(combos) != 1 || combos[0].ID != "demo-combo" {
		t.Fatalf("combos = %+v err=%v", combos, err)
	}
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	masterKey, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatalf("master key: %v", err)
	}
	encryptor, err := security.NewEncryptor(masterKey)
	if err != nil {
		t.Fatalf("encryptor: %v", err)
	}
	dataStore, err := store.OpenSQLite(filepath.Join(dir, "test.db"), encryptor)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	return dataStore
}

func seedProvider(t *testing.T, dataStore *store.Store, id, providerType string) {
	t.Helper()
	cfg := config.ProviderConfig{ID: id, Type: providerType, Name: id, Enabled: true}
	if err := dataStore.SaveProvider(context.Background(), cfg); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
