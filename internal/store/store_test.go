package store

import (
	"context"
	"database/sql"
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
