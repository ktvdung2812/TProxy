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

func TestImport9routerAntigravityPreservesProjectID(t *testing.T) {
	dataStore := openTestStore(t)
	payload := []byte(`{
	  "providerConnections": [
	    {
	      "id": "ag-credential",
	      "provider": "gemini-cli",
	      "authType": "oauth",
	      "name": "Antigravity Account",
	      "email": "user@example.com",
	      "isActive": true,
	      "accessToken": "access-token",
	      "refreshToken": "refresh-token",
	      "projectId": "cloud-project-123",
	      "providerSpecificData": {"project_id": "ignored-project"}
	    }
	  ]
	}`)

	result, err := import9router.Import(context.Background(), dataStore, payload, import9router.Options{})
	if err != nil || !result.OK {
		t.Fatalf("import: result=%+v err=%v", result, err)
	}
	credentials, err := dataStore.Credentials(context.Background(), "gemini-cli")
	if err != nil || len(credentials) != 1 {
		t.Fatalf("credentials=%+v err=%v", credentials, err)
	}
	if got := credentials[0].OAuthToken.Extra["project_id"]; got != "cloud-project-123" {
		t.Fatalf("project_id=%#v, want cloud-project-123", got)
	}
}

func TestImport9routerAntigravityPreservesObjectProjectID(t *testing.T) {
	dataStore := openTestStore(t)
	payload := []byte(`{
	  "providerConnections": [
	    {
	      "id": "ag-object-credential",
	      "provider": "gemini-cli",
	      "authType": "oauth",
	      "name": "Antigravity Account",
	      "email": "user@example.com",
	      "isActive": true,
	      "accessToken": "access-token",
	      "project_id": {"cloudaicompanionProject": {"id": "cloud-project-object"}}
	    }
	  ]
	}`)

	result, err := import9router.Import(context.Background(), dataStore, payload, import9router.Options{})
	if err != nil || !result.OK {
		t.Fatalf("import: result=%+v err=%v", result, err)
	}
	credentials, err := dataStore.Credentials(context.Background(), "gemini-cli")
	if err != nil || len(credentials) != 1 {
		t.Fatalf("credentials=%+v err=%v", credentials, err)
	}
	if got := credentials[0].OAuthToken.Extra["project_id"]; got != "cloud-project-object" {
		t.Fatalf("project_id=%#v, want cloud-project-object", got)
	}
}

func TestImport9routerAntigravityPreservesNestedProjectVariant(t *testing.T) {
	dataStore := openTestStore(t)
	payload := []byte(`{
	  "providerConnections": [
	    {
	      "id": "ag-project-credential",
	      "provider": "gemini-cli",
	      "authType": "oauth",
	      "name": "Antigravity Account",
	      "accessToken": "access-token",
	      "project": {"cloudaicompanionProject": {"id": "cloud-project-nested"}}
	    }
	  ]
	}`)

	result, err := import9router.Import(context.Background(), dataStore, payload, import9router.Options{})
	if err != nil || !result.OK {
		t.Fatalf("import: result=%+v err=%v", result, err)
	}
	credentials, err := dataStore.Credentials(context.Background(), "gemini-cli")
	if err != nil || len(credentials) != 1 {
		t.Fatalf("credentials=%+v err=%v", credentials, err)
	}
	if got := credentials[0].OAuthToken.Extra["project_id"]; got != "cloud-project-nested" {
		t.Fatalf("project_id=%#v, want cloud-project-nested", got)
	}
}

func TestImport9routerAntigravityPreservesSnakeCaseCompanionProject(t *testing.T) {
	dataStore := openTestStore(t)
	payload := []byte(`{
	  "providerConnections": [
	    {
	      "id": "ag-snake-project-credential",
	      "provider": "gemini-cli",
	      "authType": "oauth",
	      "name": "Antigravity Account",
	      "accessToken": "access-token",
	      "cloudaicompanion_project": {"id": "cloud-project-snake"}
	    }
	  ]
	}`)

	result, err := import9router.Import(context.Background(), dataStore, payload, import9router.Options{})
	if err != nil || !result.OK {
		t.Fatalf("import: result=%+v err=%v", result, err)
	}
	credentials, err := dataStore.Credentials(context.Background(), "gemini-cli")
	if err != nil || len(credentials) != 1 {
		t.Fatalf("credentials=%+v err=%v", credentials, err)
	}
	if got := credentials[0].OAuthToken.Extra["project_id"]; got != "cloud-project-snake" {
		t.Fatalf("project_id=%#v, want cloud-project-snake", got)
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
