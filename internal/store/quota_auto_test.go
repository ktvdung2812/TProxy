package store

import (
	"context"
	"testing"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/security"
)

func TestSyncCredentialQuotaStateDisablesAtZeroAndRestores(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(t.TempDir()+"/quota-auto.db", encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	ctx := context.Background()
	if err := dataStore.SaveProvider(ctx, config.ProviderConfig{ID: "provider", Type: "codex", Name: "Codex", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	enabled := true
	if err := dataStore.SaveCredential(ctx, "provider", config.CredentialConfig{ID: "cred-a", AuthType: "oauth", Secret: "token", Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}

	credential, err := dataStore.CredentialByID(ctx, "cred-a")
	if err != nil {
		t.Fatal(err)
	}
	changed, err := dataStore.SyncCredentialQuotaState(ctx, credential, true)
	if err != nil || !changed {
		t.Fatalf("disable sync changed=%v err=%v", changed, err)
	}

	credential, err = dataStore.CredentialByID(ctx, "cred-a")
	if err != nil {
		t.Fatal(err)
	}
	if credential.Enabled || !QuotaAutoDisabled(credential.Metadata) {
		t.Fatalf("credential = %+v", credential)
	}

	changed, err = dataStore.SyncCredentialQuotaState(ctx, credential, false)
	if err != nil || !changed {
		t.Fatalf("restore sync changed=%v err=%v", changed, err)
	}

	credential, err = dataStore.CredentialByID(ctx, "cred-a")
	if err != nil {
		t.Fatal(err)
	}
	if !credential.Enabled || QuotaAutoDisabled(credential.Metadata) {
		t.Fatalf("credential = %+v", credential)
	}
}

func TestSyncCredentialQuotaStateLeavesManualDisableAlone(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(t.TempDir()+"/quota-manual.db", encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	ctx := context.Background()
	if err := dataStore.SaveProvider(ctx, config.ProviderConfig{ID: "provider", Type: "codex", Name: "Codex", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	disabled := false
	if err := dataStore.SaveCredential(ctx, "provider", config.CredentialConfig{ID: "cred-a", AuthType: "oauth", Secret: "token", Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}

	credential, err := dataStore.CredentialByID(ctx, "cred-a")
	if err != nil {
		t.Fatal(err)
	}
	changed, err := dataStore.SyncCredentialQuotaState(ctx, credential, false)
	if err != nil || changed {
		t.Fatalf("manual disable should not auto-enable: changed=%v err=%v", changed, err)
	}
}

func TestSyncCredentialRenewalPersistsAndClears(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(t.TempDir()+"/quota-renewal.db", encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	ctx := context.Background()
	if err := dataStore.SaveProvider(ctx, config.ProviderConfig{ID: "provider", Type: "codex", Name: "Codex", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	enabled := true
	if err := dataStore.SaveCredential(ctx, "provider", config.CredentialConfig{ID: "cred-a", AuthType: "oauth", Secret: "token", Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}

	if err := dataStore.SyncCredentialRenewal(ctx, "cred-a", "2026-09-14T04:43:46Z"); err != nil {
		t.Fatal(err)
	}
	credential, err := dataStore.CredentialByID(ctx, "cred-a")
	if err != nil {
		t.Fatal(err)
	}
	renewsAt, ok := QuotaRenewsAt(credential.Metadata)
	if !ok || renewsAt.UTC().Format("2006-01-02") != "2026-09-14" {
		t.Fatalf("renewal = %v ok=%v", renewsAt, ok)
	}

	// Re-syncing the same value must not clobber other metadata keys.
	credential.Metadata["other_key"] = "kept"
	if err := dataStore.UpdateCredentialMetadata(ctx, "cred-a", credential.Metadata); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.SyncCredentialRenewal(ctx, "cred-a", "2026-09-14T04:43:46Z"); err != nil {
		t.Fatal(err)
	}
	credential, err = dataStore.CredentialByID(ctx, "cred-a")
	if err != nil {
		t.Fatal(err)
	}
	if credential.Metadata["other_key"] != "kept" {
		t.Fatalf("metadata clobbered: %+v", credential.Metadata)
	}

	// An empty value (subscription gone) clears the key.
	if err := dataStore.SyncCredentialRenewal(ctx, "cred-a", ""); err != nil {
		t.Fatal(err)
	}
	credential, err = dataStore.CredentialByID(ctx, "cred-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := QuotaRenewsAt(credential.Metadata); ok {
		t.Fatalf("renewal should be cleared: %+v", credential.Metadata)
	}
}

func TestQuotaRenewsAtRejectsBadValues(t *testing.T) {
	if _, ok := QuotaRenewsAt(nil); ok {
		t.Fatal("nil metadata should report no renewal")
	}
	if _, ok := QuotaRenewsAt(map[string]any{quotaRenewsAtKey: "not-a-date"}); ok {
		t.Fatal("invalid date should report no renewal")
	}
}
