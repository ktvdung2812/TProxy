package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/security"
	_ "modernc.org/sqlite"
)

func TestSQLiteMigrationsUpgradeLegacySchemaAndTrackVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE providers (id TEXT PRIMARY KEY, type TEXT NOT NULL, name TEXT NOT NULL, base_url TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1, headers_json TEXT NOT NULL DEFAULT '{}', created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE credentials (id TEXT PRIMARY KEY, provider_id TEXT NOT NULL, auth_type TEXT NOT NULL, label TEXT NOT NULL DEFAULT '', email TEXT NOT NULL DEFAULT '', secret_ciphertext TEXT NOT NULL DEFAULT '', metadata_json TEXT NOT NULL DEFAULT '{}', priority INTEGER NOT NULL DEFAULT 0, weight INTEGER NOT NULL DEFAULT 1, enabled INTEGER NOT NULL DEFAULT 1, cooldown_until TEXT NOT NULL DEFAULT '', last_error_code TEXT NOT NULL DEFAULT '', last_error TEXT NOT NULL DEFAULT '', last_validated_at TEXT NOT NULL DEFAULT '');
CREATE TABLE usage_events (id INTEGER PRIMARY KEY AUTOINCREMENT, request_id TEXT NOT NULL, created_at TEXT NOT NULL);
PRAGMA user_version=1;`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := OpenSQLite(path, encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	version, err := opened.SchemaVersion(context.Background())
	if err != nil || version != sqliteCurrentSchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
	for table, column := range map[string]string{"providers": "config_json", "credentials": "status", "usage_events": "tokens_saved"} {
		exists, existsErr := sqliteColumnExists(context.Background(), opened.db, table, column)
		if existsErr != nil {
			err = existsErr
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("missing migrated column %s.%s", table, column)
		}
	}
	for table, column := range map[string]string{"api_keys": "policy_json", "request_logs": "metadata_json", "audit_events": "metadata_json", "model_aliases": "scope_team_id"} {
		exists, existsErr := sqliteColumnExists(context.Background(), opened.db, table, column)
		if existsErr != nil || !exists {
			t.Fatalf("missing migrated column %s.%s exists=%v err=%v", table, column, exists, existsErr)
		}
	}
}

func TestSQLiteMigrationsUpgradeFromEverySupportedSchemaVersion(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range sqliteMigrations {
		target := target
		t.Run(fmt.Sprintf("v%d", target.version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "migration.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			for _, migration := range sqliteMigrations {
				if migration.version > target.version {
					break
				}
				tx, beginErr := db.BeginTx(context.Background(), nil)
				if beginErr != nil {
					db.Close()
					t.Fatal(beginErr)
				}
				if applyErr := migration.apply(context.Background(), tx); applyErr != nil {
					_ = tx.Rollback()
					db.Close()
					t.Fatalf("apply migration %d: %v", migration.version, applyErr)
				}
				if _, setErr := tx.ExecContext(context.Background(), fmt.Sprintf("PRAGMA user_version = %d", migration.version)); setErr != nil {
					_ = tx.Rollback()
					db.Close()
					t.Fatal(setErr)
				}
				if commitErr := tx.Commit(); commitErr != nil {
					db.Close()
					t.Fatal(commitErr)
				}
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			opened, err := OpenSQLite(path, encryptor)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			version, err := opened.SchemaVersion(context.Background())
			if err != nil || version != sqliteCurrentSchemaVersion {
				t.Fatalf("schema version=%d err=%v after upgrading from v%d", version, err, target.version)
			}
		})
	}
}

func TestSQLiteBackupIntegrityAndRestore(t *testing.T) {
	t.Setenv("TPROXY_TEST_BACKUP_SECRET", "plaintext-api-key")
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	databasePath := filepath.Join(dir, "tproxy.db")
	backupPath := filepath.Join(dir, "backups", "snapshot.db")
	dataStore, err := OpenSQLite(databasePath, encryptor)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Database: config.DatabaseConfig{Driver: "sqlite"}, Providers: []config.ProviderConfig{{
		ID: "provider", Type: "openai-compatible", Name: "Provider", BaseURL: "https://example.test", Enabled: true,
		Credentials: []config.CredentialConfig{{ID: "credential", AuthType: "api_key", SecretEnv: "TPROXY_TEST_BACKUP_SECRET"}},
	}}, Models: []config.PublicModelConfig{{ID: "public-model", Enabled: true, Aliases: []string{"model"}, Routes: []config.RouteTargetConfig{{ID: "route", Provider: "provider", UpstreamModel: "upstream-model", Enabled: boolPtr(true)}}}}}
	if err = dataStore.Seed(context.Background(), cfg); err != nil {
		dataStore.Close()
		t.Fatal(err)
	}
	if err = dataStore.Backup(context.Background(), backupPath); err != nil {
		dataStore.Close()
		t.Fatal(err)
	}
	if err = dataStore.IntegrityCheck(context.Background()); err != nil {
		dataStore.Close()
		t.Fatal(err)
	}
	backupBytes, err := os.ReadFile(backupPath)
	if err != nil {
		dataStore.Close()
		t.Fatal(err)
	}
	if string(backupBytes) == "" || containsBytes(backupBytes, []byte("plaintext-api-key")) {
		dataStore.Close()
		t.Fatal("backup is empty or contains plaintext credential material")
	}
	if err = dataStore.DeletePublicModel(context.Background(), "public-model"); err != nil {
		dataStore.Close()
		t.Fatal(err)
	}
	if err = dataStore.Close(); err != nil {
		t.Fatal(err)
	}
	if err = RestoreSQLite(context.Background(), backupPath, databasePath); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenSQLite(databasePath, encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	model, err := restored.ResolveModel(context.Background(), "model", "")
	if err != nil || model.ID != "public-model" {
		t.Fatalf("restored model=%+v err=%v", model, err)
	}
	credentials, err := restored.Credentials(context.Background(), "provider")
	if err != nil || len(credentials) != 1 || credentials[0].Secret != "plaintext-api-key" {
		t.Fatalf("restored credentials=%+v err=%v", credentials, err)
	}
}

func TestRestoreSQLiteRejectsCorruptSourceWithoutReplacingDestination(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "destination.db")
	source := filepath.Join(dir, "corrupt.db")
	if err := os.WriteFile(destination, []byte("keep-me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreSQLite(context.Background(), source, destination); err == nil {
		t.Fatal("expected corrupt restore source to be rejected")
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "keep-me" {
		t.Fatalf("destination changed after failed restore: %q err=%v", data, err)
	}
}

func TestSQLiteRejectsFutureSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=999"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = OpenSQLite(path, encryptor); err == nil {
		t.Fatal("expected future schema version to be rejected")
	}
}

func TestProxyPoolSecretsAreEncryptedAndBindingsAreTracked(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "proxy.db")
	dataStore, err := OpenSQLite(path, encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	cfg := &config.Config{
		ProxyPools: []config.ProxyPoolConfig{{ID: "egress", Name: "Primary", URL: "http://user:password@proxy.example.com:8080"}},
		Providers:  []config.ProviderConfig{{ID: "provider", Type: "openai-compatible", Enabled: true, ProxyPools: []string{"egress"}, Credentials: []config.CredentialConfig{{ID: "credential", AuthType: "none", ProxyPools: []string{"egress"}}}}},
	}
	if err = dataStore.Seed(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	pool, err := dataStore.ProxyPool(context.Background(), "egress")
	if err != nil || pool.URL != "http://user:password@proxy.example.com:8080" {
		t.Fatalf("pool=%+v err=%v", pool, err)
	}
	inspection, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Close()
	var ciphertext string
	if err = inspection.QueryRow(`SELECT url_ciphertext FROM proxy_pools WHERE id='egress'`).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if ciphertext == "" || ciphertext == pool.URL || containsBytes([]byte(ciphertext), []byte("password")) {
		t.Fatalf("proxy URL was not encrypted: %q", ciphertext)
	}
	snapshot, err := dataStore.Snapshot(context.Background())
	if err != nil || len(snapshot.ProxyPools) != 1 {
		t.Fatalf("snapshot=%+v err=%v", snapshot.ProxyPools, err)
	}
	if snapshot.ProxyPools[0].URL != "http://proxy.example.com:8080" || snapshot.ProxyPools[0].UsageCount != 2 {
		t.Fatalf("proxy summary=%+v", snapshot.ProxyPools[0])
	}
	if err = dataStore.DeleteProxyPool(context.Background(), "egress"); err == nil {
		t.Fatal("expected bound proxy pool deletion to fail")
	}
}

func TestSnapshotAndExportRedactProviderConfigurationSecrets(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "redaction.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	cfg := &config.Config{Providers: []config.ProviderConfig{{
		ID:      "provider",
		Type:    "openai-compatible",
		BaseURL: "https://example.test",
		Enabled: true,
		Headers: map[string]string{"Authorization": "Bearer provider-secret", "User-Agent": "tproxy-test"},
		Config: map[string]any{
			"api_token": "provider-config-secret",
			"nested":    map[string]any{"client_password": "nested-secret", "region": "local"},
		},
	}}}
	if err = dataStore.Seed(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	snapshot, err := dataStore.Snapshot(context.Background())
	if err != nil || len(snapshot.Providers) != 1 {
		t.Fatalf("snapshot providers=%+v err=%v", snapshot.Providers, err)
	}
	provider := snapshot.Providers[0]
	if provider.Headers["Authorization"] != "[REDACTED]" || provider.Headers["User-Agent"] != "tproxy-test" || provider.Config["api_token"] != "[REDACTED]" {
		t.Fatalf("snapshot provider was not redacted: %+v", provider)
	}
	nested, _ := provider.Config["nested"].(map[string]any)
	if nested["client_password"] != "[REDACTED]" || nested["region"] != "local" {
		t.Fatalf("snapshot nested config was not redacted: %+v", nested)
	}
	exported, err := dataStore.ExportConfig(context.Background(), cfg)
	if err != nil || len(exported.Providers) != 1 {
		t.Fatalf("export providers=%+v err=%v", exported.Providers, err)
	}
	exportedConfig := exported.Providers[0].Config
	if exported.Providers[0].Headers["User-Agent"] != "tproxy-test" {
		t.Fatalf("non-sensitive export header was lost: %+v", exported.Providers[0].Headers)
	}
	if _, exists := exported.Providers[0].Headers["Authorization"]; exists {
		t.Fatalf("sensitive export header was retained: %+v", exported.Providers[0].Headers)
	}
	if exportedConfig["api_token_env"] != "TPROXY_PROVIDER_API_TOKEN" {
		t.Fatalf("exported config=%+v", exportedConfig)
	}
	exportedNested, _ := exportedConfig["nested"].(map[string]any)
	if exportedNested["client_password_env"] != "TPROXY_PROVIDER_CLIENT_PASSWORD" || exportedNested["region"] != "local" {
		t.Fatalf("exported nested config=%+v", exportedNested)
	}
}

func TestObservabilityRecordsAndRetentionAreBounded(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "observability.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	old := time.Now().UTC().Add(-48 * time.Hour)
	if err = dataStore.AddRequestLog(context.Background(), RequestLog{RequestID: "req-old", Method: "GET", Path: "/v1/models", Status: 200, CreatedAt: old}); err != nil {
		t.Fatal(err)
	}
	if err = dataStore.AddAuditEvent(context.Background(), AuditEvent{Action: "POST /api/admin/models", Status: 200, CreatedAt: old}); err != nil {
		t.Fatal(err)
	}
	if err = dataStore.AddUsage(context.Background(), UsageEvent{RequestID: "usage-old", CreatedAt: old}); err != nil {
		t.Fatal(err)
	}
	if _, err = dataStore.PruneRequestLogs(context.Background(), time.Now().UTC().Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = dataStore.PruneAuditEvents(context.Background(), time.Now().UTC().Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = dataStore.PruneUsage(context.Background(), time.Now().UTC().Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	logs, _ := dataStore.RecentRequestLogs(context.Background(), 10)
	audits, _ := dataStore.RecentAuditEvents(context.Background(), 10)
	usage, _ := dataStore.RecentUsage(context.Background(), 10)
	if len(logs) != 0 || len(audits) != 0 || len(usage) != 0 {
		t.Fatalf("retention logs=%d audits=%d usage=%d", len(logs), len(audits), len(usage))
	}
	if err = dataStore.RecordConfigVersion(context.Background(), "test", &config.Config{Database: config.DatabaseConfig{Driver: "sqlite"}}); err != nil {
		t.Fatal(err)
	}
	versions, err := dataStore.RecentConfigVersions(context.Background(), 10)
	if err != nil || len(versions) != 1 || versions[0].Digest == "" {
		t.Fatalf("config versions=%+v err=%v", versions, err)
	}
}

func TestTeamEstimatedCostAggregatesEnabledClientKeys(t *testing.T) {
	t.Setenv("TPROXY_TEAM_KEY_A", "team-secret-a")
	t.Setenv("TPROXY_TEAM_KEY_B", "team-secret-b")
	t.Setenv("TPROXY_OTHER_KEY", "other-secret")
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "team-budget.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	if err = dataStore.Seed(context.Background(), &config.Config{ClientAPIKeys: []config.ClientAPIKey{
		{ID: "team-a", KeyEnv: "TPROXY_TEAM_KEY_A", Policy: config.ClientKeyPolicy{Team: "engineering"}},
		{ID: "team-b", KeyEnv: "TPROXY_TEAM_KEY_B", Policy: config.ClientKeyPolicy{Team: "engineering"}},
		{ID: "other", KeyEnv: "TPROXY_OTHER_KEY", Policy: config.ClientKeyPolicy{Team: "other"}},
	}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, event := range []UsageEvent{
		{RequestID: "a", ClientAPIKeyID: "team-a", EstimatedCostUSD: 0.4, CreatedAt: now},
		{RequestID: "b", ClientAPIKeyID: "team-b", EstimatedCostUSD: 0.7, CreatedAt: now},
		{RequestID: "other", ClientAPIKeyID: "other", EstimatedCostUSD: 1.5, CreatedAt: now},
	} {
		if err = dataStore.AddUsage(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	total, err := dataStore.TeamEstimatedCostSince(context.Background(), "engineering", now.Add(-time.Minute))
	if err != nil || total < 1.099 || total > 1.101 {
		t.Fatalf("team cost=%f err=%v", total, err)
	}
}

func TestEncryptedOAuthAuthBundleRoundTrip(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	first, err := OpenSQLite(filepath.Join(t.TempDir(), "auth-first.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "oauth-provider", Type: "openai-compatible", BaseURL: "http://127.0.0.1", Enabled: true}}}
	if err = first.Seed(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err = first.SaveOAuthCredential(context.Background(), "oauth-provider", "oauth-credential", "Account", "user@example.com", OAuthToken{AccessToken: "access-secret", RefreshToken: "refresh-secret", TokenType: "Bearer"}); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "oauth.json")
	if err = first.ExportAuthFile(context.Background(), bundle); err != nil {
		t.Fatal(err)
	}
	bundleData, _ := os.ReadFile(bundle)
	if string(bundleData) == "" || strings.Contains(string(bundleData), "access-secret") || strings.Contains(string(bundleData), "refresh-secret") {
		t.Fatalf("auth bundle leaked plaintext: %s", bundleData)
	}
	second, err := OpenSQLite(filepath.Join(t.TempDir(), "auth-second.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err = second.Seed(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err = second.ImportAuthFile(context.Background(), bundle); err != nil {
		t.Fatal(err)
	}
	credentials, err := second.Credentials(context.Background(), "oauth-provider")
	if err != nil || len(credentials) != 1 || credentials[0].OAuthToken == nil || credentials[0].OAuthToken.RefreshToken != "refresh-secret" {
		t.Fatalf("imported credentials=%+v err=%v", credentials, err)
	}
}

func TestOAuthAuthBundleImportRejectsUnknownProviderAtomically(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	source, err := OpenSQLite(filepath.Join(t.TempDir(), "auth-source.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	sourceConfig := &config.Config{Providers: []config.ProviderConfig{
		{ID: "provider-a", Type: "openai-compatible", BaseURL: "http://127.0.0.1", Enabled: true},
		{ID: "provider-b", Type: "openai-compatible", BaseURL: "http://127.0.0.1", Enabled: true},
	}}
	if err = source.Seed(context.Background(), sourceConfig); err != nil {
		t.Fatal(err)
	}
	if err = source.SaveOAuthCredential(context.Background(), "provider-a", "credential-a", "A", "", OAuthToken{AccessToken: "access-a"}); err != nil {
		t.Fatal(err)
	}
	if err = source.SaveOAuthCredential(context.Background(), "provider-b", "credential-b", "B", "", OAuthToken{AccessToken: "access-b"}); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "oauth.json")
	if err = source.ExportAuthFile(context.Background(), bundle); err != nil {
		t.Fatal(err)
	}
	target, err := OpenSQLite(filepath.Join(t.TempDir(), "auth-target.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err = target.Seed(context.Background(), &config.Config{Providers: sourceConfig.Providers[:1]}); err != nil {
		t.Fatal(err)
	}
	if err = target.ImportAuthFile(context.Background(), bundle); err == nil || !strings.Contains(err.Error(), "provider-b") {
		t.Fatalf("expected unknown provider import error, got %v", err)
	}
	credentials, err := target.Credentials(context.Background(), "provider-a")
	if err != nil || len(credentials) != 0 {
		t.Fatalf("partial auth import was not rolled back: credentials=%+v err=%v", credentials, err)
	}
}

func TestOAuthAuthBundleImportRejectsWrongMasterKey(t *testing.T) {
	sourceKey, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	sourceEncryptor, err := security.NewEncryptor(sourceKey)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "provider", Type: "openai-compatible", BaseURL: "http://127.0.0.1", Enabled: true}}}
	source, err := OpenSQLite(filepath.Join(t.TempDir(), "auth-source.db"), sourceEncryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err = source.Seed(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err = source.SaveOAuthCredential(context.Background(), "provider", "credential", "Account", "", OAuthToken{AccessToken: "access-secret"}); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "oauth.json")
	if err = source.ExportAuthFile(context.Background(), bundle); err != nil {
		t.Fatal(err)
	}
	targetKey, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	targetEncryptor, err := security.NewEncryptor(targetKey)
	if err != nil {
		t.Fatal(err)
	}
	target, err := OpenSQLite(filepath.Join(t.TempDir(), "auth-target.db"), targetEncryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err = target.Seed(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err = target.ImportAuthFile(context.Background(), bundle); err == nil || !strings.Contains(err.Error(), "active master key") {
		t.Fatalf("expected wrong master key import error, got %v", err)
	}
	credentials, err := target.Credentials(context.Background(), "provider")
	if err != nil || len(credentials) != 0 {
		t.Fatalf("wrong-key import changed target credentials: credentials=%+v err=%v", credentials, err)
	}
}

func TestOAuthAuthBundleValidatesTypeMetadataAndEnvelopeAtomically(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	target, err := OpenSQLite(filepath.Join(t.TempDir(), "auth-target.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	providerCfg := &config.Config{Providers: []config.ProviderConfig{{ID: "provider", Type: "openai-compatible", Enabled: true}}}
	if err = target.Seed(context.Background(), providerCfg); err != nil {
		t.Fatal(err)
	}
	validEnvelope, err := json.Marshal(OAuthToken{AccessToken: "access-secret", RefreshToken: "refresh-secret", TokenType: "Bearer"})
	if err != nil {
		t.Fatal(err)
	}
	validCiphertext, err := encryptor.Encrypt(string(validEnvelope))
	if err != nil {
		t.Fatal(err)
	}
	malformedCiphertext, err := encryptor.Encrypt("plaintext-token")
	if err != nil {
		t.Fatal(err)
	}
	bundle := authBundle{Version: 1, Credentials: []authBundleCredential{
		{ID: "valid", ProviderID: "provider", AuthType: "oauth", SecretCiphertext: validCiphertext, MetadataJSON: `{"provider_project":"safe"}`, Enabled: true},
		{ID: "bad-type", ProviderID: "provider", AuthType: "api_key", SecretCiphertext: validCiphertext, MetadataJSON: `{}`, Enabled: true},
		{ID: "bad-metadata", ProviderID: "provider", AuthType: "oauth", SecretCiphertext: validCiphertext, MetadataJSON: `{"access_token":"plaintext-leak"}`, Enabled: true},
		{ID: "bad-envelope", ProviderID: "provider", AuthType: "oauth", SecretCiphertext: malformedCiphertext, MetadataJSON: `{}`, Enabled: true},
	}}
	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err = target.ImportAuthBundle(context.Background(), data); err == nil {
		t.Fatal("expected invalid auth bundle to be rejected")
	}
	credentials, err := target.Credentials(context.Background(), "provider")
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 0 {
		t.Fatalf("invalid auth bundle partially committed: %+v", credentials)
	}
}

func TestExportConfigRedactsCredentialMetadata(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "metadata-export.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "provider", Type: "openai-compatible", Enabled: true}}}
	if err = dataStore.Seed(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err = dataStore.SaveCredential(context.Background(), "provider", config.CredentialConfig{ID: "credential", AuthType: "oauth", Secret: "access-secret", Metadata: map[string]any{"access_token": "metadata-secret", "region": "local"}}); err != nil {
		t.Fatal(err)
	}
	exported, err := dataStore.ExportConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	metadata := exported.Providers[0].Credentials[0].Metadata
	if _, exists := metadata["access_token_env"]; !exists {
		t.Fatalf("credential metadata secret was not converted to placeholder: %+v", metadata)
	}
	if metadata["region"] != "local" || strings.Contains(fmt.Sprint(metadata), "metadata-secret") {
		t.Fatalf("credential metadata export leaked secret: %+v", metadata)
	}
}

func TestRequestAndAuditLogsRedactSecretMetadata(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "logs.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	metadata := map[string]any{"Authorization": "Bearer access-secret", "nested": map[string]any{"refresh_token": "refresh-secret"}, "message": "api_key=key-secret"}
	if err = dataStore.AddRequestLog(context.Background(), RequestLog{RequestID: "request", Metadata: metadata}); err != nil {
		t.Fatal(err)
	}
	if err = dataStore.AddAuditEvent(context.Background(), AuditEvent{Action: "test", Metadata: metadata}); err != nil {
		t.Fatal(err)
	}
	var requestRaw, auditRaw string
	if err = dataStore.db.QueryRow(`SELECT metadata_json FROM request_logs WHERE request_id='request'`).Scan(&requestRaw); err != nil {
		t.Fatal(err)
	}
	if err = dataStore.db.QueryRow(`SELECT metadata_json FROM audit_events WHERE action='test'`).Scan(&auditRaw); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{requestRaw, auditRaw} {
		if strings.Contains(raw, "access-secret") || strings.Contains(raw, "refresh-secret") || strings.Contains(raw, "key-secret") {
			t.Fatalf("log metadata leaked secret: %s", raw)
		}
	}
}

func TestClearCredentialCooldownPreservesModelCooldowns(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "cooldowns.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	if err = dataStore.Seed(context.Background(), &config.Config{Providers: []config.ProviderConfig{{
		ID: "provider", Type: "openai-compatible", Enabled: true,
		Credentials: []config.CredentialConfig{{ID: "credential", AuthType: "none"}},
	}}}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	if err = dataStore.SetCooldown(context.Background(), "credential", "account_limit", "account unavailable", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"upstream-a", "upstream-b"} {
		if err = dataStore.SetModelCooldown(context.Background(), "credential", model, "model_limit", "model unavailable", now.Add(time.Hour), 429, 1); err != nil {
			t.Fatal(err)
		}
	}

	if err = dataStore.ClearCredentialCooldown(context.Background(), "credential"); err != nil {
		t.Fatal(err)
	}
	credential, err := dataStore.CredentialByID(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	if credential.Status != "healthy" || !credential.CooldownUntil.IsZero() || credential.LastErrorCode != "" || credential.LastError != "" || credential.LastValidated.IsZero() {
		t.Fatalf("credential cooldown was not cleared: %+v", credential)
	}
	for _, model := range []string{"upstream-a", "upstream-b"} {
		until, cooldownErr := dataStore.ModelCooldownUntil(context.Background(), "credential", model, now)
		if cooldownErr != nil || until.IsZero() || dataStore.ModelCooldownCount(context.Background(), "credential", model) != 1 {
			t.Fatalf("model %q cooldown changed: until=%v err=%v", model, until, cooldownErr)
		}
	}

	if err = dataStore.ClearCooldown(context.Background(), "credential"); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"upstream-a", "upstream-b"} {
		until, cooldownErr := dataStore.ModelCooldownUntil(context.Background(), "credential", model, now)
		if cooldownErr != nil || !until.IsZero() || dataStore.ModelCooldownCount(context.Background(), "credential", model) != 0 {
			t.Fatalf("admin clear retained model %q cooldown: until=%v err=%v", model, until, cooldownErr)
		}
	}
}

func TestClearCredentialCooldownRejectsMissingCredential(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "missing-cooldown.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	if err = dataStore.ClearCredentialCooldown(context.Background(), "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing credential error=%v, want sql.ErrNoRows", err)
	}
}

func TestSetModelCooldownRedactsDurableError(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "model-error.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	if err = dataStore.Seed(context.Background(), &config.Config{Providers: []config.ProviderConfig{{
		ID: "provider", Type: "openai-compatible", Enabled: true,
		Credentials: []config.CredentialConfig{{ID: "credential", AuthType: "none"}},
	}}}); err != nil {
		t.Fatal(err)
	}
	if err = dataStore.SetModelCooldown(context.Background(), "credential", "model", "token=access-secret", "proxy http://proxy-user:proxy-password@example.test failed", time.Now().Add(time.Hour), 429, 1); err != nil {
		t.Fatal(err)
	}
	var code, message string
	if err = dataStore.db.QueryRow(`SELECT code,message FROM credential_model_cooldowns WHERE credential_id='credential' AND model='model'`).Scan(&code, &message); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{code, message} {
		if strings.Contains(raw, "access-secret") || strings.Contains(raw, "proxy-password") || strings.Contains(raw, "proxy-user") {
			t.Fatalf("model cooldown leaked durable secret: %q", raw)
		}
	}
}

func TestLegacyRawOAuthAuthBundleRoundTripIntoFreshStore(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	providerConfig := &config.Config{Providers: []config.ProviderConfig{{ID: "provider", Type: "openai-compatible", Name: "Provider", Enabled: true}}}
	source, err := OpenSQLite(filepath.Join(t.TempDir(), "legacy-source.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err = source.Seed(context.Background(), providerConfig); err != nil {
		t.Fatal(err)
	}
	if err = source.SaveCredential(context.Background(), "provider", config.CredentialConfig{ID: "legacy-oauth", AuthType: "oauth", Secret: "legacy-access"}); err != nil {
		t.Fatal(err)
	}
	bundle, err := source.ExportAuthBundle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bundle, []byte("legacy-access")) {
		t.Fatal("legacy OAuth token leaked into exported bundle")
	}
	target, err := OpenSQLite(filepath.Join(t.TempDir(), "legacy-target.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err = target.ImportAuthBundle(context.Background(), bundle); err != nil {
		t.Fatal(err)
	}
	provider, err := target.Provider(context.Background(), "provider")
	if err != nil || provider.ID != "provider" {
		t.Fatalf("fresh import provider=%+v err=%v", provider, err)
	}
	credentials, err := target.Credentials(context.Background(), "provider")
	if err != nil || len(credentials) != 1 || credentials[0].Secret != "legacy-access" || credentials[0].OAuthToken == nil {
		t.Fatalf("fresh legacy import credentials=%+v err=%v", credentials, err)
	}
}

func TestAuthBundleMetadataValidationBranches(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(OAuthToken{AccessToken: "access-secret", RefreshToken: "refresh-secret", TokenType: "Bearer"})
	if err != nil {
		t.Fatal(err)
	}
	validCiphertext, err := encryptor.Encrypt(string(envelope))
	if err != nil {
		t.Fatal(err)
	}
	emptyEnvelope, err := encryptor.Encrypt("{}")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		authType   string
		metadata   string
		ciphertext string
		wantErr    bool
	}{
		{name: "unsupported type", authType: "api_key", metadata: `{}`, ciphertext: validCiphertext, wantErr: true},
		{name: "plaintext token metadata", authType: "oauth", metadata: `{"api_token":"plaintext"}`, ciphertext: validCiphertext, wantErr: true},
		{name: "malformed envelope", authType: "oauth", metadata: `{}`, ciphertext: emptyEnvelope, wantErr: true},
		{name: "duplicate metadata key", authType: "oauth", metadata: `{"region":"one","region":"two"}`, ciphertext: validCiphertext, wantErr: true},
		{name: "redacted metadata", authType: "oauth", metadata: `{"access_token":"[REDACTED]","copilot_token_expires_at":"2030-01-01T00:00:00Z"}`, ciphertext: validCiphertext, wantErr: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			target, openErr := OpenSQLite(filepath.Join(t.TempDir(), "metadata.db"), encryptor)
			if openErr != nil {
				t.Fatal(openErr)
			}
			defer target.Close()
			if seedErr := target.Seed(context.Background(), &config.Config{Providers: []config.ProviderConfig{{ID: "provider", Type: "openai-compatible", Enabled: true}}}); seedErr != nil {
				t.Fatal(seedErr)
			}
			bundle, marshalErr := json.Marshal(authBundle{Version: 1, Credentials: []authBundleCredential{{ID: "credential", ProviderID: "provider", AuthType: test.authType, SecretCiphertext: test.ciphertext, MetadataJSON: test.metadata, Enabled: true}}})
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			importErr := target.ImportAuthBundle(context.Background(), bundle)
			if (importErr != nil) != test.wantErr {
				t.Fatalf("import error=%v wantErr=%v", importErr, test.wantErr)
			}
		})
	}
}

func TestAuthBundleImportClearsTransientCredentialState(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	providerConfig := &config.Config{Providers: []config.ProviderConfig{{ID: "provider", Type: "openai-compatible", Enabled: true}}}
	source, err := OpenSQLite(filepath.Join(t.TempDir(), "transient-source.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err = source.Seed(context.Background(), providerConfig); err != nil {
		t.Fatal(err)
	}
	if err = source.SaveOAuthCredential(context.Background(), "provider", "credential", "", "", OAuthToken{AccessToken: "source-access"}); err != nil {
		t.Fatal(err)
	}
	bundle, err := source.ExportAuthBundle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	target, err := OpenSQLite(filepath.Join(t.TempDir(), "transient-target.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err = target.Seed(context.Background(), providerConfig); err != nil {
		t.Fatal(err)
	}
	if err = target.SaveOAuthCredential(context.Background(), "provider", "credential", "", "", OAuthToken{AccessToken: "old-access"}); err != nil {
		t.Fatal(err)
	}
	if err = target.SetCooldown(context.Background(), "credential", "old_code", "old error", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = target.SetModelCooldown(context.Background(), "credential", "model", "old_code", "old error", time.Now().Add(time.Hour), 429, 2); err != nil {
		t.Fatal(err)
	}
	if err = target.ImportAuthBundle(context.Background(), bundle); err != nil {
		t.Fatal(err)
	}
	credential, err := target.CredentialByID(context.Background(), "credential")
	if err != nil {
		t.Fatal(err)
	}
	if credential.Status != "healthy" || !credential.CooldownUntil.IsZero() || credential.LastErrorCode != "" || credential.LastError != "" || !credential.LastValidated.IsZero() || credential.Secret != "source-access" {
		t.Fatalf("transient state survived import: %+v", credential)
	}
	if until, untilErr := target.ModelCooldownUntil(context.Background(), "credential", "model", time.Now()); untilErr != nil || !until.IsZero() {
		t.Fatalf("model cooldown survived import: until=%v err=%v", until, untilErr)
	}
}

func TestAuthBundleSizeLimits(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "size.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	if err = dataStore.ImportAuthBundle(context.Background(), bytes.Repeat([]byte{'x'}, MaxAuthBundleBytes+1)); err == nil {
		t.Fatal("oversized direct auth bundle was accepted")
	}
	if err = dataStore.Seed(context.Background(), &config.Config{Providers: []config.ProviderConfig{{ID: "provider", Type: "openai-compatible", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	if err = dataStore.SaveOAuthCredential(context.Background(), "provider", "credential", "", "", OAuthToken{AccessToken: "access"}); err != nil {
		t.Fatal(err)
	}
	if err = dataStore.UpdateCredentialMetadata(context.Background(), "credential", map[string]any{"notes": strings.Repeat("x", MaxAuthBundleBytes)}); err != nil {
		t.Fatal(err)
	}
	if _, err = dataStore.ExportAuthBundle(context.Background()); err == nil {
		t.Fatal("oversized auth bundle export was accepted")
	}
}

func TestCredentialUpdatesRejectMissingRows(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "missing.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	if err = dataStore.UpdateCredentialMetadata(context.Background(), "missing", map[string]any{}); err == nil {
		t.Fatal("expected missing metadata update to fail")
	}
	if err = dataStore.UpdateOAuthToken(context.Background(), "missing", OAuthToken{AccessToken: "access"}); err == nil {
		t.Fatal("expected missing OAuth update to fail")
	}
}

func boolPtr(value bool) *bool { return &value }

func containsBytes(haystack, needle []byte) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
