package importcliproxy_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tproxy/tproxy/internal/importcliproxy"
	"github.com/tproxy/tproxy/internal/security"
	"github.com/tproxy/tproxy/internal/store"
)

func TestImportCliproxyCodexAuth(t *testing.T) {
	dataStore := openTestStore(t)
	payload := []byte(`{
	  "access_token": "access-token",
	  "account_id": "acct-123",
	  "disabled": false,
	  "email": "user@example.com",
	  "expired": "2026-12-31T00:00:00Z",
	  "id_token": "id-token",
	  "last_refresh": "2026-07-15T10:12:03+07:00",
	  "refresh_token": "refresh-token",
	  "type": "codex"
	}`)

	result, err := importcliproxy.Import(context.Background(), dataStore, payload, importcliproxy.Options{})
	if err != nil || !result.OK {
		t.Fatalf("import: result=%+v err=%v", result, err)
	}
	if result.Counts.Credentials != 1 || result.Counts.Providers != 1 {
		t.Fatalf("counts=%+v", result.Counts)
	}
	providers, err := dataStore.Providers(context.Background())
	if err != nil || len(providers) != 1 || providers[0].ID != "codex" {
		t.Fatalf("providers=%+v err=%v", providers, err)
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

func TestParseCliproxyAuthArray(t *testing.T) {
	files, err := importcliproxy.ParseAuthFiles([]byte(`[
	  {"type":"codex","access_token":"a","email":"one@example.com"},
	  {"type":"claude","access_token":"b","email":"two@example.com"}
	]`))
	if err != nil || len(files) != 2 {
		t.Fatalf("files=%+v err=%v", files, err)
	}
}

func TestImportCliproxyAntigravityPreservesProjectID(t *testing.T) {
	dataStore := openTestStore(t)
	payload := []byte(`{
	  "type": "antigravity",
	  "access_token": "access-token",
	  "refresh_token": "refresh-token",
	  "email": "user@example.com",
	  "cloudaicompanionProject": {"id": "cloud-project-123"}
	}`)

	result, err := importcliproxy.Import(context.Background(), dataStore, payload, importcliproxy.Options{})
	if err != nil || !result.OK {
		t.Fatalf("import: result=%+v err=%v", result, err)
	}
	credentials, err := dataStore.Credentials(context.Background(), "antigravity")
	if err != nil || len(credentials) != 1 {
		t.Fatalf("credentials=%+v err=%v", credentials, err)
	}
	if got := credentials[0].OAuthToken.Extra["project_id"]; got != "cloud-project-123" {
		t.Fatalf("project_id=%#v, want cloud-project-123", got)
	}
}

func TestImportCliproxyGeminiCLIUsesAntigravityProjectID(t *testing.T) {
	dataStore := openTestStore(t)
	payload := []byte(`{
	  "type": "gemini-cli",
	  "access_token": "access-token",
	  "email": "user@example.com",
	  "projectId": {"id": "cloud-project-cli"}
	}`)

	result, err := importcliproxy.Import(context.Background(), dataStore, payload, importcliproxy.Options{})
	if err != nil || !result.OK {
		t.Fatalf("import: result=%+v err=%v", result, err)
	}
	providers, err := dataStore.Providers(context.Background())
	if err != nil || len(providers) != 1 || providers[0].Type != "antigravity" || providers[0].BaseURL != "https://cloudcode-pa.googleapis.com" {
		t.Fatalf("providers=%+v err=%v", providers, err)
	}
	credentials, err := dataStore.Credentials(context.Background(), "gemini-cli")
	if err != nil || len(credentials) != 1 {
		t.Fatalf("credentials=%+v err=%v", credentials, err)
	}
	if got := credentials[0].OAuthToken.Extra["project_id"]; got != "cloud-project-cli" {
		t.Fatalf("project_id=%#v, want cloud-project-cli", got)
	}
}

func TestImportCliproxyAntigravityPreservesNestedProjectVariant(t *testing.T) {
	dataStore := openTestStore(t)
	payload := []byte(`{
	  "type": "antigravity",
	  "access_token": "access-token",
	  "project": {"cloudaicompanionProject": {"id": "cloud-project-nested"}}
	}`)

	result, err := importcliproxy.Import(context.Background(), dataStore, payload, importcliproxy.Options{})
	if err != nil || !result.OK {
		t.Fatalf("import: result=%+v err=%v", result, err)
	}
	credentials, err := dataStore.Credentials(context.Background(), "antigravity")
	if err != nil || len(credentials) != 1 {
		t.Fatalf("credentials=%+v err=%v", credentials, err)
	}
	if got := credentials[0].OAuthToken.Extra["project_id"]; got != "cloud-project-nested" {
		t.Fatalf("project_id=%#v, want cloud-project-nested", got)
	}
}

func TestImportCliproxyAntigravityPreservesSnakeCaseCompanionProject(t *testing.T) {
	dataStore := openTestStore(t)
	payload := []byte(`{
	  "type": "antigravity",
	  "access_token": "access-token",
	  "cloudaicompanion_project": {"id": "cloud-project-snake"}
	}`)

	result, err := importcliproxy.Import(context.Background(), dataStore, payload, importcliproxy.Options{})
	if err != nil || !result.OK {
		t.Fatalf("import: result=%+v err=%v", result, err)
	}
	credentials, err := dataStore.Credentials(context.Background(), "antigravity")
	if err != nil || len(credentials) != 1 {
		t.Fatalf("credentials=%+v err=%v", credentials, err)
	}
	if got := credentials[0].OAuthToken.Extra["project_id"]; got != "cloud-project-snake" {
		t.Fatalf("project_id=%#v, want cloud-project-snake", got)
	}
}
