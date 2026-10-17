package importsub2api_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/importsub2api"
	"github.com/tproxy/tproxy/internal/store"
)

func TestExportAccountsRoundTrip(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)

	payload := []byte(`{
	  "exported_at": "2026-09-22T17:42:29Z",
	  "proxies": [{
	    "proxy_key": "http|10.0.0.1|8080|user|pass",
	    "name": "us-proxy",
	    "protocol": "http",
	    "host": "10.0.0.1",
	    "port": 8080,
	    "username": "user",
	    "password": "pass",
	    "status": "active"
	  }],
	  "accounts": [{
	    "name": "one@example.com",
	    "platform": "openai",
	    "type": "oauth",
	    "concurrency": 10,
	    "priority": 1,
	    "proxy_key": "http|10.0.0.1|8080|user|pass",
	    "credentials": {
	      "access_token": "access-1",
	      "refresh_token": "refresh-1",
	      "id_token": "id-1",
	      "chatgpt_account_id": "acct-1",
	      "email": "one@example.com",
	      "expires_at": "2026-10-02T17:42:29Z"
	    },
	    "extra": {"team_workspace_id": "ws-1"}
	  }]
	}`)
	result, err := importsub2api.Import(ctx, dataStore, payload, importsub2api.Options{})
	if err != nil || !result.OK {
		t.Fatalf("import: result=%+v err=%v", result, err)
	}
	credentials, err := dataStore.Credentials(ctx, "codex")
	if err != nil || len(credentials) != 1 {
		t.Fatalf("credentials=%+v err=%v", credentials, err)
	}

	exported, exportResult, err := importsub2api.ExportAccounts(ctx, dataStore, []string{credentials[0].ID})
	if err != nil || !exportResult.OK || exportResult.Counts.Credentials != 1 {
		t.Fatalf("export: result=%+v err=%v body=%s", exportResult, err, exported)
	}
	var bundle importsub2api.Export
	if err = json.Unmarshal(exported, &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.Type != "sub2api-data" || bundle.Version != 1 || len(bundle.Accounts) != 1 || len(bundle.Proxies) != 1 {
		t.Fatalf("bundle=%+v", bundle)
	}
	account := bundle.Accounts[0]
	if account.Platform != "openai" || account.Type != "oauth" || account.Concurrency != 10 || account.Priority != 1 {
		t.Fatalf("account=%+v", account)
	}
	if account.ProxyKey == nil || *account.ProxyKey != "http|10.0.0.1|8080|user|pass" {
		t.Fatalf("proxy_key=%v", account.ProxyKey)
	}
	if account.Credentials["access_token"] != "access-1" || account.Credentials["refresh_token"] != "refresh-1" {
		t.Fatalf("credentials=%+v", account.Credentials)
	}
	if account.Credentials["chatgpt_account_id"] != "acct-1" || account.Credentials["id_token"] != "id-1" {
		t.Fatalf("credentials=%+v", account.Credentials)
	}
	if account.Extra["team_workspace_id"] != "ws-1" {
		t.Fatalf("extra=%+v", account.Extra)
	}

	roundTripStore := openTestStore(t)
	reimport, err := importsub2api.Import(ctx, roundTripStore, exported, importsub2api.Options{})
	if err != nil || !reimport.OK || reimport.Counts.Credentials != 1 {
		t.Fatalf("reimport: result=%+v err=%v", reimport, err)
	}
}

func TestExportAccountsAPIKeyAndSelection(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	for _, provider := range []config.ProviderConfig{
		{ID: "openai-api", Type: "openai-compatible", Name: "OpenAI API", BaseURL: "https://api.openai.com/v1", Enabled: true},
		{ID: "codex", Type: "codex", Name: "OpenAI Codex", Enabled: true},
	} {
		config.ApplyProviderDefaults(&provider)
		if err := dataStore.SaveProvider(ctx, provider); err != nil {
			t.Fatal(err)
		}
	}
	enabled := true
	if err := dataStore.SaveCredential(ctx, "openai-api", config.CredentialConfig{
		ID: "key-1", Label: "Primary", Email: "billing@example.com", AuthType: "api_key", Secret: "sk-test", Enabled: &enabled,
	}); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.SaveOAuthCredential(ctx, "codex", "oauth-1", "Member", "member@example.com", store.OAuthToken{
		AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Date(2026, 10, 2, 17, 42, 29, 0, time.UTC),
		Extra: map[string]any{"account_id": "acct-1"},
	}); err != nil {
		t.Fatal(err)
	}

	exported, result, err := importsub2api.ExportAccounts(ctx, dataStore, []string{"oauth-1", "key-1"})
	if err != nil || !result.OK || result.Counts.Credentials != 2 {
		t.Fatalf("export: result=%+v err=%v", result, err)
	}
	var bundle importsub2api.Export
	if err = json.Unmarshal(exported, &bundle); err != nil {
		t.Fatal(err)
	}
	if len(bundle.Accounts) != 2 {
		t.Fatalf("accounts=%+v", bundle.Accounts)
	}
}

func TestExportAccountsMissingCredential(t *testing.T) {
	dataStore := openTestStore(t)
	_, result, err := importsub2api.ExportAccounts(context.Background(), dataStore, []string{"missing-id"})
	if err == nil || result == nil || result.OK {
		t.Fatalf("expected missing credential error, result=%+v err=%v", result, err)
	}
}
