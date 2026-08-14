package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tproxy/tproxy/internal/config"
	_ "modernc.org/sqlite"
)

func TestAPIKeyModelPoliciesDistinguishDefaultCustomAndDenyAll(t *testing.T) {
	ctx := context.Background()
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "api-key-models.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	_, defaultSecret, err := dataStore.CreateAPIKey(ctx, "default-models", "Default models", nil)
	if err != nil {
		t.Fatal(err)
	}
	defaultKey, err := dataStore.AuthenticateAPIKey(ctx, defaultSecret)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(defaultKey.Models, []string{"*"}) || !dataStore.PublicModelAllowed(defaultKey, "future-model") {
		t.Fatalf("default model policy = %#v", defaultKey.Models)
	}

	_, deniedSecret, err := dataStore.CreateAPIKey(ctx, "no-models", "No models", []string{})
	if err != nil {
		t.Fatal(err)
	}
	deniedKey, err := dataStore.AuthenticateAPIKey(ctx, deniedSecret)
	if err != nil {
		t.Fatal(err)
	}
	if len(deniedKey.Models) != 0 || dataStore.PublicModelAllowed(deniedKey, "any-model") {
		t.Fatalf("deny-all model policy = %#v", deniedKey.Models)
	}

	if err = dataStore.UpdateAPIKey(ctx, deniedKey.ID, deniedKey.Name, []string{" model-b ", "model-a", "model-b"}, true); err != nil {
		t.Fatal(err)
	}
	customKey, err := dataStore.AuthenticateAPIKey(ctx, deniedSecret)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(customKey.Models, []string{"model-b", "model-a"}) {
		t.Fatalf("normalized custom model policy = %#v", customKey.Models)
	}
	if !dataStore.PublicModelAllowed(customKey, "model-a") || dataStore.PublicModelAllowed(customKey, "model-c") {
		t.Fatalf("custom model access was not enforced: %#v", customKey.Models)
	}

	t.Setenv("TPROXY_LEGACY_EMPTY_MODELS", "legacy-empty-models-secret")
	if err = dataStore.Seed(ctx, &config.Config{ClientAPIKeys: []config.ClientAPIKey{{
		ID: "legacy-config-empty", KeyEnv: "TPROXY_LEGACY_EMPTY_MODELS", Models: []string{},
	}}}); err != nil {
		t.Fatal(err)
	}
	legacyConfigKey, err := dataStore.AuthenticateAPIKey(ctx, "legacy-empty-models-secret")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(legacyConfigKey.Models, []string{"*"}) {
		t.Fatalf("legacy empty config model policy = %#v", legacyConfigKey.Models)
	}
}

func TestAPIKeyModelMigrationPreservesLegacyEmptyListAsAllModels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-api-key-models.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`
CREATE TABLE api_keys (
 id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', key_hash TEXT NOT NULL UNIQUE,
 models_json TEXT NOT NULL DEFAULT '[]', policy_json TEXT NOT NULL DEFAULT '{}',
 enabled INTEGER NOT NULL DEFAULT 1, last_used_at TEXT NOT NULL DEFAULT ''
);
INSERT INTO api_keys(id,name,key_hash,models_json) VALUES('legacy','Legacy','hash','[]');
PRAGMA user_version=20;`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	dataStore, err := OpenSQLite(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	keys, err := dataStore.APIKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || !reflect.DeepEqual(keys[0].Models, []string{"*"}) {
		t.Fatalf("migrated API keys = %#v", keys)
	}
}
