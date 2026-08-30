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
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/security"
	_ "modernc.org/sqlite"
)

func TestOpenSQLiteRepairsPrivatePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file mode assertions are not applicable on Windows")
	}
	directory := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "tproxy.db")
	dataStore, err := OpenSQLite(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := dataStore.Close(); err != nil {
		t.Fatal(err)
	}
	for filePath, want := range map[string]os.FileMode{directory: 0o700, path: 0o600} {
		info, err := os.Stat(filePath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Fatalf("mode for %s = %v; want %04o", filePath, info.Mode().Perm(), want)
		}
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	dataStore, err = OpenSQLite(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("reopened database mode = %v; want repaired 0600", info.Mode().Perm())
	}
}

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
	if exists, existsErr := sqliteColumnExists(context.Background(), opened.db, "usage_events", "cached_tokens"); existsErr != nil || !exists {
		t.Fatalf("missing migrated column usage_events.cached_tokens exists=%v err=%v", exists, existsErr)
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
	if err = dataStore.AddAuditEvent(context.Background(), AuditEvent{Action: "POST /api/admin/models", Status: 200, CreatedAt: old}); err != nil {
		t.Fatal(err)
	}
	if err = dataStore.AddUsage(context.Background(), UsageEvent{RequestID: "usage-old", CreatedAt: old}); err != nil {
		t.Fatal(err)
	}
	if _, err = dataStore.PruneAuditEvents(context.Background(), time.Now().UTC().Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = dataStore.PruneUsage(context.Background(), time.Now().UTC().Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	audits, _ := dataStore.RecentAuditEvents(context.Background(), 10)
	usage, _ := dataStore.RecentUsage(context.Background(), 10)
	if len(audits) != 0 || len(usage) != 0 {
		t.Fatalf("retention audits=%d usage=%d", len(audits), len(usage))
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

func TestOAuthCredentialIDForLoginMergesByEmail(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "oauth-login.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "oauth-provider", Type: "openai-compatible", BaseURL: "http://127.0.0.1", Enabled: true}}}
	if err = dataStore.Seed(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err = dataStore.SaveOAuthCredential(context.Background(), "oauth-provider", "existing-account", "Primary", "User@Example.com", OAuthToken{AccessToken: "old-access"}); err != nil {
		t.Fatal(err)
	}
	resolved, err := dataStore.OAuthCredentialIDForLogin(context.Background(), "oauth-provider", "oauth_cred_new", "user@example.com", false)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != "existing-account" {
		t.Fatalf("resolved credential id=%q", resolved)
	}
	explicit, err := dataStore.OAuthCredentialIDForLogin(context.Background(), "oauth-provider", "forced-account", "user@example.com", true)
	if err != nil {
		t.Fatal(err)
	}
	if explicit != "forced-account" {
		t.Fatalf("explicit credential id=%q", explicit)
	}
}

func TestSaveOAuthCredentialPreservesHistoryAndRemovesDuplicates(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "oauth-history.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "oauth-provider", Type: "openai-compatible", BaseURL: "http://127.0.0.1", Enabled: true}}}
	if err = dataStore.Seed(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err = dataStore.SaveOAuthCredential(context.Background(), "oauth-provider", "keep-account", "Primary", "user@example.com", OAuthToken{AccessToken: "old-access", Extra: map[string]any{"machine_id": "machine-1"}}); err != nil {
		t.Fatal(err)
	}
	if err = dataStore.UpdateCredentialMetadata(context.Background(), "keep-account", map[string]any{"notes": "keep-me"}); err != nil {
		t.Fatal(err)
	}
	if _, err = dataStore.db.ExecContext(context.Background(), `UPDATE credentials SET priority=7, weight=3, enabled=0, last_used_at=?, consecutive_use_count=4, created_at=? WHERE id=?`,
		time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Format(time.RFC3339Nano),
		time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		"keep-account"); err != nil {
		t.Fatal(err)
	}
	if err = dataStore.SaveOAuthCredential(context.Background(), "oauth-provider", "duplicate-account", "Duplicate", "User@Example.com", OAuthToken{AccessToken: "dup-access"}); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	if err = dataStore.AddUsage(context.Background(), UsageEvent{RequestID: "req-keep", CredentialID: "keep-account", ProviderID: "oauth-provider", CreatedAt: createdAt}); err != nil {
		t.Fatal(err)
	}
	if err = dataStore.AddUsage(context.Background(), UsageEvent{RequestID: "req-dup", CredentialID: "duplicate-account", ProviderID: "oauth-provider", CreatedAt: createdAt.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err = dataStore.SaveOAuthCredential(context.Background(), "oauth-provider", "keep-account", "", "user@example.com", OAuthToken{AccessToken: "new-access", RefreshToken: "new-refresh", TokenType: "Bearer", Extra: map[string]any{"account_id": "acct-1"}}); err != nil {
		t.Fatal(err)
	}
	credentials, err := dataStore.Credentials(context.Background(), "oauth-provider")
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 1 {
		t.Fatalf("credentials=%+v", credentials)
	}
	credential := credentials[0]
	if credential.ID != "keep-account" || credential.OAuthToken == nil || credential.OAuthToken.AccessToken != "new-access" {
		t.Fatalf("credential=%+v", credential)
	}
	if credential.OAuthToken.Extra["machine_id"] != "machine-1" || credential.OAuthToken.Extra["account_id"] != "acct-1" {
		t.Fatalf("merged token extra=%#v", credential.OAuthToken.Extra)
	}
	if credential.Metadata["notes"] != "keep-me" || credential.Priority != 7 || credential.Weight != 3 || !credential.Enabled == false || credential.ConsecutiveUseCount != 4 {
		t.Fatalf("preserved fields=%+v", credential)
	}
	var usageCount int
	if err = dataStore.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM usage_events WHERE credential_id='keep-account'`).Scan(&usageCount); err != nil {
		t.Fatal(err)
	}
	if usageCount != 2 {
		t.Fatalf("usage count=%d", usageCount)
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

func TestAuditLogsRedactSecretMetadata(t *testing.T) {
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
	if err = dataStore.AddAuditEvent(context.Background(), AuditEvent{Action: "test", Metadata: metadata}); err != nil {
		t.Fatal(err)
	}
	var auditRaw string
	if err = dataStore.db.QueryRow(`SELECT metadata_json FROM audit_events WHERE action='test'`).Scan(&auditRaw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditRaw, "access-secret") || strings.Contains(auditRaw, "refresh-secret") || strings.Contains(auditRaw, "key-secret") {
		t.Fatalf("audit metadata leaked secret: %s", auditRaw)
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

func TestOAuthAuthBundleRoundTripIntoFreshStoreSeedsAllProviders(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	providerConfig := &config.Config{Providers: []config.ProviderConfig{
		{ID: "provider-a", Type: "openai-compatible", Name: "Provider A", Enabled: true},
		{ID: "provider-b", Type: "openai-compatible", Name: "Provider B", Enabled: true},
	}}
	source, err := OpenSQLite(filepath.Join(t.TempDir(), "multi-source.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err = source.Seed(context.Background(), providerConfig); err != nil {
		t.Fatal(err)
	}
	if err = source.SaveOAuthCredential(context.Background(), "provider-a", "credential-a", "", "", OAuthToken{AccessToken: "access-a"}); err != nil {
		t.Fatal(err)
	}
	if err = source.SaveOAuthCredential(context.Background(), "provider-b", "credential-b", "", "", OAuthToken{AccessToken: "access-b"}); err != nil {
		t.Fatal(err)
	}
	bundle, err := source.ExportAuthBundle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	target, err := OpenSQLite(filepath.Join(t.TempDir(), "multi-target.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err = target.ImportAuthBundle(context.Background(), bundle); err != nil {
		t.Fatal(err)
	}
	for _, providerID := range []string{"provider-a", "provider-b"} {
		provider, providerErr := target.Provider(context.Background(), providerID)
		if providerErr != nil || provider.ID != providerID {
			t.Fatalf("provider %q missing after fresh import: %+v err=%v", providerID, provider, providerErr)
		}
		credentials, credentialsErr := target.Credentials(context.Background(), providerID)
		if credentialsErr != nil || len(credentials) != 1 {
			t.Fatalf("provider %q credentials missing after fresh import: %+v err=%v", providerID, credentials, credentialsErr)
		}
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

func TestSnapshotIncludesDisabledRouteTargets(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "routes.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	disabled := false
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{
			ID: "provider", Type: "openai-compatible", Enabled: true,
			Credentials: []config.CredentialConfig{{ID: "credential", AuthType: "none"}},
		}},
		Models: []config.PublicModelConfig{{
			ID: "model", Enabled: true,
			Routes: []config.RouteTargetConfig{
				{ID: "enabled-route", Provider: "provider", UpstreamModel: "enabled", Priority: 100},
				{ID: "disabled-route", Provider: "provider", UpstreamModel: "disabled", Priority: 10, Enabled: &disabled},
			},
		}},
	}
	if err = dataStore.Seed(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	snapshot, err := dataStore.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	routes := snapshot.Routes["model"]
	if len(routes) != 2 {
		t.Fatalf("routes=%+v, want both enabled and disabled routes", routes)
	}
	foundDisabled := false
	for _, route := range routes {
		if route.ID == "disabled-route" {
			foundDisabled = true
			if route.Enabled {
				t.Fatalf("disabled route was reported as enabled: %+v", route)
			}
		}
	}
	if !foundDisabled {
		t.Fatalf("disabled route was not returned: %+v", routes)
	}

	if _, err = dataStore.db.Exec(`PRAGMA foreign_keys=OFF; DELETE FROM providers WHERE id=?`, "provider"); err != nil {
		t.Fatal(err)
	}
	routes, err = dataStore.Routes(context.Background(), "model")
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 0 {
		t.Fatalf("orphaned routes=%+v, want none", routes)
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

func TestAccountRotationSettingsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rotation.db")
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(path, encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	ctx := context.Background()
	defaults, err := dataStore.AccountRotationSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.StickyRoundRobinLimit != 3 {
		t.Fatalf("default sticky limit = %d", defaults.StickyRoundRobinLimit)
	}
	settings := AccountRotationSettings{
		Strategy:              "fill-first",
		StickyRoundRobinLimit: 5,
		ProviderStrategies: map[string]ProviderRotationStrategy{
			"codex-main": {Strategy: "round-robin", StickyRoundRobinLimit: 7},
		},
	}
	if err := dataStore.SaveAccountRotationSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := dataStore.AccountRotationSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Strategy != "fill-first" || loaded.StickyRoundRobinLimit != 5 || loaded.ProviderStrategies["codex-main"].StickyRoundRobinLimit != 7 {
		t.Fatalf("loaded settings = %+v", loaded)
	}
}

func TestResetProviderRotationStateClearsCounters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rotation-reset.db")
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(path, encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	ctx := context.Background()
	if err := dataStore.SaveProvider(ctx, config.ProviderConfig{ID: "provider-a", Type: "openai-compatible", Name: "Provider A", BaseURL: "http://127.0.0.1:9", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.SaveCredential(ctx, "provider-a", config.CredentialConfig{ID: "cred-a", Label: "A", AuthType: "none", Priority: 1, Enabled: boolPtr(true)}); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.TouchCredentialRotation(ctx, "cred-a", 2, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.ResetProviderRotationState(ctx, "provider-a"); err != nil {
		t.Fatal(err)
	}
	credential, err := dataStore.CredentialByID(ctx, "cred-a")
	if err != nil {
		t.Fatal(err)
	}
	if !credential.LastUsedAt.IsZero() || credential.ConsecutiveUseCount != 0 {
		t.Fatalf("credential rotation state = %+v", credential)
	}
}

func TestTouchCredentialRotationPersistsState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rotation-touch.db")
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(path, encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	ctx := context.Background()
	if err := dataStore.SaveProvider(ctx, config.ProviderConfig{ID: "provider-a", Type: "openai-compatible", Name: "Provider A", BaseURL: "http://127.0.0.1:9", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.SaveCredential(ctx, "provider-a", config.CredentialConfig{ID: "cred-a", AuthType: "api_key", Secret: "secret", Enabled: boolPtr(true)}); err != nil {
		t.Fatal(err)
	}
	usedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if err := dataStore.TouchCredentialRotation(ctx, "cred-a", 2, usedAt); err != nil {
		t.Fatal(err)
	}
	credential, err := dataStore.CredentialByID(ctx, "cred-a")
	if err != nil {
		t.Fatal(err)
	}
	if credential.ConsecutiveUseCount != 2 || credential.LastUsedAt.IsZero() {
		t.Fatalf("credential rotation state = %+v", credential)
	}
}

func TestReorderCredentialsPersistsRouterOrderAtomically(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "credential-order.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	ctx := context.Background()
	if err := dataStore.SaveProvider(ctx, config.ProviderConfig{ID: "codex", Type: "codex", Name: "Codex", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second", "third"} {
		if err := dataStore.SaveCredential(ctx, "codex", config.CredentialConfig{ID: id, AuthType: "oauth", Enabled: boolPtr(true)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := dataStore.ReorderCredentials(ctx, "codex", []string{"third", "first", "second"}); err != nil {
		t.Fatal(err)
	}
	credentials, err := dataStore.Credentials(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(credentials))
	for _, credential := range credentials {
		got = append(got, credential.ID)
	}
	if !reflect.DeepEqual(got, []string{"third", "first", "second"}) {
		t.Fatalf("credential order = %v", got)
	}
	if err := dataStore.ReorderCredentials(ctx, "codex", []string{"third", "first"}); err == nil {
		t.Fatal("incomplete credential order was accepted")
	}
	credentials, err = dataStore.Credentials(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if credentials[0].ID != "third" || credentials[1].ID != "first" || credentials[2].ID != "second" {
		t.Fatalf("invalid reorder changed persisted order: %+v", credentials)
	}
}

func TestSeedSkipsPlaceholderCredentials(t *testing.T) {
	t.Setenv("TPROXY_TEST_SEED_SECRET", "real-api-key")

	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "seed-skip.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	cfg := &config.Config{Providers: []config.ProviderConfig{
		{
			ID: "openai-main", Type: "openai-compatible", Name: "OpenAI", Enabled: true,
			Credentials: []config.CredentialConfig{
				{ID: "openai-primary", AuthType: "api_key", SecretEnv: "TPROXY_TEST_SEED_SECRET"},
				{ID: "openai-missing", AuthType: "api_key", SecretEnv: "TPROXY_UNSET_SECRET"},
				{ID: "openai-oauth", AuthType: "oauth", Label: "OAuth placeholder"},
			},
		},
		{
			ID: "ollama-local", Type: "ollama", Name: "Ollama", Enabled: false,
			Credentials: []config.CredentialConfig{
				{ID: "ollama-none", AuthType: "none"},
			},
		},
		{
			ID: "ollama-enabled", Type: "ollama", Name: "Local Ollama", Enabled: true, BaseURL: "http://127.0.0.1:11434",
			Credentials: []config.CredentialConfig{
				{ID: "ollama-local-none", AuthType: "none"},
			},
		},
	}}
	if err = dataStore.Seed(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	openAICreds, err := dataStore.Credentials(context.Background(), "openai-main")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Credential{}
	for _, credential := range openAICreds {
		byID[credential.ID] = credential
	}
	// An api_key credential whose secret never resolves is a genuine
	// placeholder: it can never authenticate, so it gets no row.
	if _, present := byID["openai-missing"]; present {
		t.Fatalf("api_key credential with unresolved secret was seeded: %+v", openAICreds)
	}
	if byID["openai-primary"].Status != "healthy" {
		t.Fatalf("api_key credential = %+v", byID["openai-primary"])
	}
	// An OAuth credential is different: the token arrives from the enrolment
	// wizard, which needs the row to bind to. Seed it, but park it in
	// auth_required so the router leaves it alone until it is enrolled.
	oauth, present := byID["openai-oauth"]
	if !present {
		t.Fatalf("oauth credential was dropped by seed: %+v", openAICreds)
	}
	if oauth.Status != "auth_required" {
		t.Fatalf("unenrolled oauth credential status = %q, want auth_required", oauth.Status)
	}
	for _, eligible := range EligibleCredentials(openAICreds, time.Now()) {
		if eligible.ID == "openai-oauth" {
			t.Fatal("unenrolled oauth credential must not be routable")
		}
	}

	// A disabled provider still keeps its credentials: routing already filters
	// on the provider's own enabled flag, and dropping the rows here would make
	// a config export/import round-trip lose them.
	disabledCreds, err := dataStore.Credentials(context.Background(), "ollama-local")
	if err != nil {
		t.Fatal(err)
	}
	if len(disabledCreds) != 1 || disabledCreds[0].ID != "ollama-none" {
		t.Fatalf("disabled provider credentials = %+v", disabledCreds)
	}
	disabledProvider, err := dataStore.Provider(context.Background(), "ollama-local")
	if err != nil {
		t.Fatal(err)
	}
	if disabledProvider.Enabled {
		t.Fatal("provider should have stayed disabled")
	}

	enabledNoneCreds, err := dataStore.Credentials(context.Background(), "ollama-enabled")
	if err != nil {
		t.Fatal(err)
	}
	if len(enabledNoneCreds) != 1 || enabledNoneCreds[0].ID != "ollama-local-none" {
		t.Fatalf("enabled none-auth credentials = %+v", enabledNoneCreds)
	}
}

// Config export writes OAuth credentials back out, so seeding has to accept
// them or every export/import cycle silently drops the operator's accounts.
func TestSeedPreservesOAuthCredentialsAcrossConfigRoundTrip(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "roundtrip.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	ctx := context.Background()
	if err = dataStore.Seed(ctx, &config.Config{Providers: []config.ProviderConfig{{
		ID: "claude-sub", Type: "claude", Name: "Claude", Enabled: true,
		Credentials: []config.CredentialConfig{{ID: "claude-account", AuthType: "oauth", Label: "Subscription", Secret: "token-from-wizard"}},
	}}}); err != nil {
		t.Fatal(err)
	}

	exported, err := dataStore.ExportConfig(ctx, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = dataStore.Seed(ctx, exported); err != nil {
		t.Fatal(err)
	}

	credentials, err := dataStore.Credentials(ctx, "claude-sub")
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 1 || credentials[0].ID != "claude-account" {
		t.Fatalf("oauth credential lost on round trip: %+v", credentials)
	}
	// The secret-free export redacts the secret, so re-importing must not wipe
	// the stored token: the ON CONFLICT clause keeps the existing ciphertext.
	if credentials[0].Secret != "token-from-wizard" {
		t.Fatalf("stored oauth token was clobbered by import: %q", credentials[0].Secret)
	}
}

func TestExportConfigWithOAuthTokensRoundTripsAcrossMasterKeys(t *testing.T) {
	sourceKey, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	sourceEncryptor, err := security.NewEncryptor(sourceKey)
	if err != nil {
		t.Fatal(err)
	}
	source, err := OpenSQLite(filepath.Join(t.TempDir(), "source.db"), sourceEncryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()

	ctx := context.Background()
	if err = source.Seed(ctx, &config.Config{Providers: []config.ProviderConfig{{
		ID: "claude-sub", Type: "claude", Name: "Claude", Enabled: true,
		Credentials: []config.CredentialConfig{{ID: "claude-account", AuthType: "oauth", Label: "Subscription"}},
	}}}); err != nil {
		t.Fatal(err)
	}
	if err = source.SaveOAuthCredential(ctx, "claude-sub", "claude-account", "Subscription", "user@example.com", OAuthToken{
		AccessToken:  "access-secret",
		RefreshToken: "refresh-secret",
		TokenType:    "Bearer",
		Extra:        map[string]any{"machine_id": "mid-1"},
	}); err != nil {
		t.Fatal(err)
	}

	redacted, err := source.ExportConfig(ctx, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if redacted.Providers[0].Credentials[0].Secret != "" {
		t.Fatalf("secret-free export leaked oauth secret: %q", redacted.Providers[0].Credentials[0].Secret)
	}

	exported, err := source.ExportConfigWithOAuthTokens(ctx, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(exported.Providers[0].Credentials[0].Secret, "access-secret") || !strings.Contains(exported.Providers[0].Credentials[0].Secret, "refresh-secret") {
		t.Fatalf("oauth export missing tokens: %q", exported.Providers[0].Credentials[0].Secret)
	}

	destKey, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	destEncryptor, err := security.NewEncryptor(destKey)
	if err != nil {
		t.Fatal(err)
	}
	dest, err := OpenSQLite(filepath.Join(t.TempDir(), "dest.db"), destEncryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	if err = dest.Seed(ctx, exported); err != nil {
		t.Fatal(err)
	}
	credentials, err := dest.Credentials(ctx, "claude-sub")
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 1 || credentials[0].OAuthToken == nil {
		t.Fatalf("imported oauth credential = %+v", credentials)
	}
	if credentials[0].OAuthToken.AccessToken != "access-secret" || credentials[0].OAuthToken.RefreshToken != "refresh-secret" {
		t.Fatalf("imported oauth token = %+v", credentials[0].OAuthToken)
	}
	if credentials[0].OAuthToken.Extra["machine_id"] != "mid-1" {
		t.Fatalf("imported oauth extra = %#v", credentials[0].OAuthToken.Extra)
	}
	if credentials[0].Status == "auth_required" {
		t.Fatalf("imported oauth status = %q", credentials[0].Status)
	}
}

// RecordConfigVersion is called after every admin mutation, but most of those
// leave the configuration untouched. Without suppression a production install
// accumulated 335,952 rows holding only 919 distinct digests.
func TestRecordConfigVersionSuppressesDuplicates(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "versions.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	ctx := context.Background()
	cfg := &config.Config{Providers: []config.ProviderConfig{{
		ID: "p1", Type: "openai-compatible", Name: "P1", Enabled: true,
		Credentials: []config.CredentialConfig{{ID: "c1", AuthType: "api_key", Secret: "s"}},
	}}}
	if err = dataStore.Seed(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 10; i++ {
		if err = dataStore.RecordConfigVersion(ctx, "admin:POST /api/admin/whatever", cfg); err != nil {
			t.Fatal(err)
		}
	}
	versions, err := dataStore.RecentConfigVersions(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 {
		t.Fatalf("10 identical recordings produced %d rows, want 1", len(versions))
	}

	// A genuine change must still append a row.
	if err = dataStore.SaveProvider(ctx, config.ProviderConfig{ID: "p2", Type: "ollama", Name: "P2", Enabled: true, BaseURL: "http://127.0.0.1:11434"}); err != nil {
		t.Fatal(err)
	}
	if err = dataStore.RecordConfigVersion(ctx, "admin:POST /api/admin/providers", cfg); err != nil {
		t.Fatal(err)
	}
	versions, err = dataStore.RecentConfigVersions(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("a real configuration change produced %d rows, want 2", len(versions))
	}
}
