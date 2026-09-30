package importsub2api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/importsub2api"
	"github.com/tproxy/tproxy/internal/security"
	"github.com/tproxy/tproxy/internal/store"
)

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

func TestImportSub2apiOpenAIOAuthAccounts(t *testing.T) {
	dataStore := openTestStore(t)
	payload := []byte(`{
	  "exported_at": "2026-09-22T17:42:29Z",
	  "proxies": [],
	  "accounts": [
	    {
	      "name": "one@example.com",
	      "platform": "openai",
	      "type": "oauth",
	      "concurrency": 10,
	      "priority": 1,
	      "credentials": {
	        "access_token": "access-1",
	        "refresh_token": "refresh-1",
	        "id_token": "id-1",
	        "session_token": "session-1",
	        "chatgpt_account_id": "acct-1",
	        "email": "one@example.com",
	        "expires_at": "2026-10-02T17:42:29Z",
	        "plan_type": "self_serve_business"
	      },
	      "extra": {"source": "chatgpt_web_session", "team_workspace_id": "ws-1"}
	    },
	    {
	      "name": "two@example.com",
	      "platform": "openai",
	      "type": "oauth",
	      "credentials": {
	        "access_token": "access-2",
	        "chatgpt_account_id": "acct-2",
	        "email": "two@example.com"
	      }
	    }
	  ]
	}`)

	result, err := importsub2api.Import(context.Background(), dataStore, payload, importsub2api.Options{})
	if err != nil || !result.OK {
		t.Fatalf("import: result=%+v err=%v", result, err)
	}
	if result.Counts.Credentials != 2 || result.Counts.Providers != 1 {
		t.Fatalf("counts=%+v", result.Counts)
	}
	providers, err := dataStore.Providers(context.Background())
	if err != nil || len(providers) != 1 || providers[0].ID != "codex" || providers[0].Type != "codex" {
		t.Fatalf("providers=%+v err=%v", providers, err)
	}
	if providers[0].BaseURL != "https://chatgpt.com/backend-api/codex" {
		t.Fatalf("codex base url = %q", providers[0].BaseURL)
	}
	credentials, err := dataStore.Credentials(context.Background(), "codex")
	if err != nil || len(credentials) != 2 {
		t.Fatalf("credentials=%+v err=%v", credentials, err)
	}
	var first *store.Credential
	for index := range credentials {
		if credentials[index].ID == "sub2api-openai-one-example-com" {
			first = &credentials[index]
		}
	}
	if first == nil {
		t.Fatalf("credential ids: %+v", credentials)
	}
	if first.AuthType != "oauth" || first.Secret != "access-1" {
		t.Fatalf("credential=%+v", first)
	}
	if first.OAuthToken == nil || first.OAuthToken.RefreshToken != "refresh-1" {
		t.Fatalf("oauth token=%+v", first.OAuthToken)
	}
	if got := first.OAuthToken.Extra["account_id"]; got != "acct-1" {
		t.Fatalf("account_id=%#v", got)
	}
	if got := first.OAuthToken.Extra["id_token"]; got != "id-1" {
		t.Fatalf("id_token=%#v", got)
	}
	if first.OAuthToken.ExpiresAt.IsZero() {
		t.Fatal("expires_at was not parsed")
	}
	if first.Metadata["sub2api_concurrency"] != float64(10) {
		t.Fatalf("metadata=%+v", first.Metadata)
	}
}

func TestImportSub2apiProxyPoolsAndProxyKey(t *testing.T) {
	dataStore := openTestStore(t)
	payload := []byte(`{
	  "proxies": [
	    {
	      "proxy_key": "http|10.0.0.1|8080|user|pass",
	      "name": "us-proxy",
	      "protocol": "http",
	      "host": "10.0.0.1",
	      "port": 8080,
	      "username": "user",
	      "password": "pass",
	      "status": "active"
	    }
	  ],
	  "accounts": [
	    {
	      "name": "one@example.com",
	      "platform": "anthropic",
	      "type": "oauth",
	      "proxy_key": "http|10.0.0.1|8080|user|pass",
	      "credentials": {
	        "access_token": "sk-ant-oat01-token",
	        "refresh_token": "refresh-1",
	        "email": "one@example.com"
	      }
	    }
	  ]
	}`)

	result, err := importsub2api.Import(context.Background(), dataStore, payload, importsub2api.Options{})
	if err != nil || !result.OK {
		t.Fatalf("import: result=%+v err=%v", result, err)
	}
	if result.Counts.ProxyPools != 1 {
		t.Fatalf("counts=%+v", result.Counts)
	}
	pools, err := dataStore.ProxyPools(context.Background())
	if err != nil || len(pools) != 1 {
		t.Fatalf("pools=%+v err=%v", pools, err)
	}
	if pools[0].URL != "http://user:pass@10.0.0.1:8080" {
		t.Fatalf("pool url=%q", pools[0].URL)
	}
	credentials, err := dataStore.Credentials(context.Background(), "claude")
	if err != nil || len(credentials) != 1 {
		t.Fatalf("credentials=%+v err=%v", credentials, err)
	}
	if len(credentials[0].ProxyPoolIDs) != 1 || credentials[0].ProxyPoolIDs[0] != pools[0].ID {
		t.Fatalf("proxy pool ids=%+v", credentials[0].ProxyPoolIDs)
	}
}

func TestImportSub2apiAPIKeyAndDryRun(t *testing.T) {
	dataStore := openTestStore(t)
	payload := []byte(`{
	  "accounts": [
	    {
	      "name": "key-account",
	      "platform": "openai",
	      "type": "apikey",
	      "priority": 1,
	      "credentials": {"api_key": "sk-test-123"}
	    }
	  ]
	}`)

	dry, err := importsub2api.Import(context.Background(), dataStore, payload, importsub2api.Options{DryRun: true})
	if err != nil || !dry.OK || !dry.DryRun {
		t.Fatalf("dry run: %+v err=%v", dry, err)
	}
	if providers, _ := dataStore.Providers(context.Background()); len(providers) != 0 {
		t.Fatalf("dry run wrote providers: %+v", providers)
	}

	result, err := importsub2api.Import(context.Background(), dataStore, payload, importsub2api.Options{})
	if err != nil || !result.OK {
		t.Fatalf("import: %+v err=%v", result, err)
	}
	credentials, err := dataStore.Credentials(context.Background(), "openai-api")
	if err != nil || len(credentials) != 1 {
		t.Fatalf("credentials=%+v err=%v", credentials, err)
	}
	if credentials[0].AuthType != "api_key" || credentials[0].Secret != "sk-test-123" {
		t.Fatalf("credential=%+v", credentials[0])
	}
	// sub2api priority 1 is highest precedence; tproxy stores higher-is-better.
	if credentials[0].Priority != -1 {
		t.Fatalf("priority=%d", credentials[0].Priority)
	}
}

func TestParseSub2apiRejectsForeignFormat(t *testing.T) {
	if _, err := importsub2api.ParseExport([]byte(`{"providerConnections": []}`)); err == nil {
		t.Fatal("expected parse error for non-sub2api payload")
	}
}

func TestImportSub2apiSkipsUnsupportedType(t *testing.T) {
	dataStore := openTestStore(t)
	payload := []byte(`{
	  "accounts": [
	    {"name": "a", "platform": "openai", "type": "oauth", "credentials": {"access_token": "x"}},
	    {"name": "b", "platform": "anthropic", "type": "bedrock", "credentials": {"access_token": "y"}}
	  ]
	}`)
	result, err := importsub2api.Import(context.Background(), dataStore, payload, importsub2api.Options{})
	if err != nil || !result.OK {
		t.Fatalf("import: %+v err=%v", result, err)
	}
	if result.Counts.Credentials != 1 || len(result.Warnings) == 0 {
		t.Fatalf("counts=%+v warnings=%v", result.Counts, result.Warnings)
	}
}

func TestImportTeamExportAndReimport(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	// Synthetic data follows the attached export: ten users, one workspace.
	export := importsub2api.Export{ExportedAt: "2026-09-22T17:42:29Z"}
	for index := 0; index < 10; index++ {
		email := fmt.Sprintf("member%d@example.com", index)
		export.Accounts = append(export.Accounts, importsub2api.Account{
			Name: email, Platform: "openai", Type: "oauth", Concurrency: 10, Priority: 1,
			Credentials: map[string]any{
				"email": email, "access_token": fmt.Sprintf("access-%d", index),
				"refresh_token": fmt.Sprintf("refresh-%d", index),
				"id_token":      "synthetic-id", "session_token": "synthetic-session",
				"chatgpt_account_id": "shared-workspace", "chatgpt_user_id": fmt.Sprintf("user-%d", index),
				"expires_at": "2026-10-02T17:42:29Z", "expires_in": 3600,
				"plan_type": "self_serve_business",
			},
			Extra: map[string]any{"team_workspace_id": "shared-workspace"},
		})
	}
	payload, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	dry, err := importsub2api.Import(ctx, dataStore, payload, importsub2api.Options{DryRun: true})
	if err != nil || !dry.OK || dry.Counts.Credentials != 10 || dry.Counts.Providers != 1 {
		t.Fatalf("preview: %+v err=%v", dry, err)
	}
	if providers, err := dataStore.Providers(ctx); err != nil || len(providers) != 0 {
		t.Fatal("preview changed providers", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		result, err := importsub2api.Import(ctx, dataStore, payload, importsub2api.Options{})
		if err != nil || !result.OK || result.Counts.Credentials != 10 || len(result.Warnings) != 0 {
			t.Fatalf("import %d: %+v err=%v", attempt, result, err)
		}
		credentials, err := dataStore.Credentials(ctx, "codex")
		if err != nil || len(credentials) != 10 {
			t.Fatalf("import %d: got %d accounts err=%v", attempt, len(credentials), err)
		}
		for _, credential := range credentials {
			token := credential.OAuthToken
			if token == nil || token.RefreshToken == "" || token.Extra["account_id"] != "shared-workspace" ||
				token.Extra["session_token"] != "synthetic-session" || token.ExpiresAt.Format(time.RFC3339) != "2026-10-02T17:42:29Z" {
				t.Fatal("OAuth data was not preserved")
			}
		}
	}
}

func TestImportPreservesExistingProvider(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	existing := config.ProviderConfig{ID: "codex", Type: "codex", Name: "My Codex", BaseURL: "https://example.com/codex", Enabled: false,
		Headers: map[string]string{"X-Custom": "preserved"}}
	if err := dataStore.SaveProvider(ctx, existing); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"accounts":[{"name":"member","platform":"openai","type":"oauth","credentials":{"access_token":"test-token"}}]}`)
	result, err := importsub2api.Import(ctx, dataStore, payload, importsub2api.Options{})
	if err != nil || !result.OK || result.Counts.Credentials != 1 {
		t.Fatalf("import: %+v err=%v", result, err)
	}
	provider, err := dataStore.Provider(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if provider.Name != existing.Name || provider.BaseURL != existing.BaseURL || provider.Enabled || provider.Headers["X-Custom"] != "preserved" {
		t.Fatal("import reset existing provider configuration")
	}
}

func TestImportRejectsProviderTypeConflict(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	if err := dataStore.SaveProvider(ctx, config.ProviderConfig{ID: "codex", Type: "claude", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"accounts":[{"platform":"openai","type":"oauth","credentials":{"access_token":"test-token"}}]}`)
	for _, dryRun := range []bool{true, false} {
		result, err := importsub2api.Import(ctx, dataStore, payload, importsub2api.Options{DryRun: dryRun})
		if err != nil || result.OK || len(result.Errors) != 1 || result.Counts.Credentials != 0 {
			t.Fatalf("dry_run=%v result=%+v err=%v", dryRun, result, err)
		}
	}
	credentials, err := dataStore.Credentials(ctx, "codex")
	if err != nil || len(credentials) != 0 {
		t.Fatal("provider conflict wrote credentials", err)
	}
}
