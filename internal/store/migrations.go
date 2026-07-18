package store

import (
	"context"
	"database/sql"
	"fmt"
)

const sqliteCurrentSchemaVersion = 19

type sqliteMigration struct {
	version int
	apply   func(context.Context, *sql.Tx) error
}

var sqliteMigrations = []sqliteMigration{
	{version: 1, apply: createCoreSchema},
	{version: 2, apply: func(ctx context.Context, tx *sql.Tx) error {
		return addColumnIfMissing(ctx, tx, "providers", "config_json", `ALTER TABLE providers ADD COLUMN config_json TEXT NOT NULL DEFAULT '{}'`)
	}},
	{version: 3, apply: func(ctx context.Context, tx *sql.Tx) error {
		return addColumnIfMissing(ctx, tx, "credentials", "status", `ALTER TABLE credentials ADD COLUMN status TEXT NOT NULL DEFAULT 'unknown'`)
	}},
	{version: 4, apply: func(ctx context.Context, tx *sql.Tx) error {
		return addColumnIfMissing(ctx, tx, "usage_events", "tokens_saved", `ALTER TABLE usage_events ADD COLUMN tokens_saved INTEGER NOT NULL DEFAULT 0`)
	}},
	{version: 5, apply: createProxyPoolSchema},
	{version: 6, apply: createMediaJobSchema},
	{version: 7, apply: func(ctx context.Context, tx *sql.Tx) error {
		return addColumnIfMissing(ctx, tx, "usage_events", "estimated_cost_usd", `ALTER TABLE usage_events ADD COLUMN estimated_cost_usd REAL NOT NULL DEFAULT 0`)
	}},
	{version: 8, apply: func(ctx context.Context, tx *sql.Tx) error {
		if err := addColumnIfMissing(ctx, tx, "providers", "status", `ALTER TABLE providers ADD COLUMN status TEXT NOT NULL DEFAULT 'unknown'`); err != nil {
			return err
		}
		if err := addColumnIfMissing(ctx, tx, "providers", "last_error", `ALTER TABLE providers ADD COLUMN last_error TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
		return addColumnIfMissing(ctx, tx, "providers", "last_checked_at", `ALTER TABLE providers ADD COLUMN last_checked_at TEXT NOT NULL DEFAULT ''`)
	}},
	{version: 9, apply: func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS api_keys (
 id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', key_hash TEXT NOT NULL UNIQUE, models_json TEXT NOT NULL DEFAULT '[]',
 policy_json TEXT NOT NULL DEFAULT '{}', enabled INTEGER NOT NULL DEFAULT 1, last_used_at TEXT NOT NULL DEFAULT ''
);`); err != nil {
			return err
		}
		return addColumnIfMissing(ctx, tx, "api_keys", "policy_json", `ALTER TABLE api_keys ADD COLUMN policy_json TEXT NOT NULL DEFAULT '{}'`)
	}},
	{version: 10, apply: createObservabilitySchema},
	{version: 11, apply: migrateScopedAliases},
	{version: 12, apply: ensureUsageSchema},
	{version: 13, apply: createComboSchema},
	{version: 14, apply: createConfigVersionSchema},
	{version: 15, apply: migrateTeamScopedAliases},
	{version: 16, apply: createCredentialModelCooldownSchema},
	{version: 17, apply: createAppSettingsSchema},
	{version: 18, apply: migrateCredentialRotationState},
	{version: 19, apply: func(ctx context.Context, tx *sql.Tx) error {
		return addColumnIfMissing(ctx, tx, "usage_events", "cached_tokens", `ALTER TABLE usage_events ADD COLUMN cached_tokens INTEGER NOT NULL DEFAULT 0`)
	}},
}

func migrateSQLite(ctx context.Context, db *sql.DB) error {
	current, err := sqliteSchemaVersion(ctx, db)
	if err != nil {
		return fmt.Errorf("read sqlite schema version: %w", err)
	}
	if current > sqliteCurrentSchemaVersion {
		return fmt.Errorf("sqlite schema version %d is newer than supported version %d", current, sqliteCurrentSchemaVersion)
	}
	for _, migration := range sqliteMigrations {
		if migration.version <= current {
			continue
		}
		tx, beginErr := db.BeginTx(ctx, nil)
		if beginErr != nil {
			return fmt.Errorf("begin sqlite migration %d: %w", migration.version, beginErr)
		}
		if applyErr := migration.apply(ctx, tx); applyErr != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply sqlite migration %d: %w", migration.version, applyErr)
		}
		if _, setErr := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", migration.version)); setErr != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record sqlite migration %d: %w", migration.version, setErr)
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return fmt.Errorf("commit sqlite migration %d: %w", migration.version, commitErr)
		}
		current = migration.version
	}
	return nil
}

func createCoreSchema(ctx context.Context, tx *sql.Tx) error {
	const schema = `
CREATE TABLE IF NOT EXISTS providers (
 id TEXT PRIMARY KEY, type TEXT NOT NULL, name TEXT NOT NULL, base_url TEXT NOT NULL DEFAULT '',
 enabled INTEGER NOT NULL DEFAULT 1, status TEXT NOT NULL DEFAULT 'unknown', last_error TEXT NOT NULL DEFAULT '', last_checked_at TEXT NOT NULL DEFAULT '',
 headers_json TEXT NOT NULL DEFAULT '{}', config_json TEXT NOT NULL DEFAULT '{}',
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS credentials (
 id TEXT PRIMARY KEY, provider_id TEXT NOT NULL, auth_type TEXT NOT NULL, label TEXT NOT NULL DEFAULT '', email TEXT NOT NULL DEFAULT '',
 secret_ciphertext TEXT NOT NULL DEFAULT '', metadata_json TEXT NOT NULL DEFAULT '{}', priority INTEGER NOT NULL DEFAULT 0,
 weight INTEGER NOT NULL DEFAULT 1, enabled INTEGER NOT NULL DEFAULT 1, status TEXT NOT NULL DEFAULT 'unknown', cooldown_until TEXT NOT NULL DEFAULT '',
 last_error_code TEXT NOT NULL DEFAULT '', last_error TEXT NOT NULL DEFAULT '', last_validated_at TEXT NOT NULL DEFAULT '',
 FOREIGN KEY(provider_id) REFERENCES providers(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS public_models (
 id TEXT PRIMARY KEY, display_name TEXT NOT NULL DEFAULT '', aliases_json TEXT NOT NULL DEFAULT '[]', enabled INTEGER NOT NULL DEFAULT 1,
 expose_upstream_name INTEGER NOT NULL DEFAULT 0, rewrite_response_model INTEGER NOT NULL DEFAULT 1,
 capabilities_json TEXT NOT NULL DEFAULT '[]', limits_json TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE IF NOT EXISTS model_aliases (
	alias TEXT NOT NULL, public_model_id TEXT NOT NULL, scope_api_key_id TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1,
	PRIMARY KEY(alias,scope_api_key_id),
 FOREIGN KEY(public_model_id) REFERENCES public_models(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS route_targets (
 id TEXT PRIMARY KEY, public_model_id TEXT NOT NULL, provider_id TEXT NOT NULL, upstream_model TEXT NOT NULL,
 priority INTEGER NOT NULL DEFAULT 0, weight INTEGER NOT NULL DEFAULT 1, enabled INTEGER NOT NULL DEFAULT 1,
 conditions_json TEXT NOT NULL DEFAULT '{}',
 FOREIGN KEY(public_model_id) REFERENCES public_models(id) ON DELETE CASCADE,
 FOREIGN KEY(provider_id) REFERENCES providers(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS api_keys (
 id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', key_hash TEXT NOT NULL UNIQUE, models_json TEXT NOT NULL DEFAULT '[]',
 policy_json TEXT NOT NULL DEFAULT '{}', enabled INTEGER NOT NULL DEFAULT 1, last_used_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS usage_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT, request_id TEXT NOT NULL, public_model_id TEXT NOT NULL DEFAULT '', provider_id TEXT NOT NULL DEFAULT '',
 upstream_model TEXT NOT NULL DEFAULT '', credential_id TEXT NOT NULL DEFAULT '', client_api_key_id TEXT NOT NULL DEFAULT '', attempt INTEGER NOT NULL DEFAULT 0, status INTEGER NOT NULL DEFAULT 0,
 input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0, reasoning_tokens INTEGER NOT NULL DEFAULT 0,
 tokens_saved INTEGER NOT NULL DEFAULT 0, estimated_cost_usd REAL NOT NULL DEFAULT 0,
 latency_ms INTEGER NOT NULL DEFAULT 0, error_code TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_usage_created_at ON usage_events(created_at);
CREATE INDEX IF NOT EXISTS idx_usage_request_id ON usage_events(request_id);
CREATE TABLE IF NOT EXISTS proxy_pools (
 id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', url_ciphertext TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1, status TEXT NOT NULL DEFAULT 'unknown',
 last_error TEXT NOT NULL DEFAULT '', last_tested_at TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
`
	_, err := tx.ExecContext(ctx, schema)
	return err
}

func createProxyPoolSchema(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS proxy_pools (
 id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', url_ciphertext TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1, status TEXT NOT NULL DEFAULT 'unknown',
 last_error TEXT NOT NULL DEFAULT '', last_tested_at TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
); CREATE INDEX IF NOT EXISTS idx_proxy_pools_enabled ON proxy_pools(enabled);`)
	return err
}

func createMediaJobSchema(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS media_jobs (
 id TEXT PRIMARY KEY, kind TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'queued',
 public_model_id TEXT NOT NULL DEFAULT '', provider_id TEXT NOT NULL DEFAULT '', credential_id TEXT NOT NULL DEFAULT '',
 upstream_id TEXT NOT NULL DEFAULT '', client_api_key_id TEXT NOT NULL DEFAULT '', idempotency_key TEXT NOT NULL DEFAULT '',
 response_json TEXT NOT NULL DEFAULT '', error_code TEXT NOT NULL DEFAULT '', error_message TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
); CREATE UNIQUE INDEX IF NOT EXISTS idx_media_jobs_idempotency ON media_jobs(client_api_key_id,idempotency_key) WHERE idempotency_key <> ''; CREATE INDEX IF NOT EXISTS idx_media_jobs_updated ON media_jobs(updated_at);`)
	return err
}

func createObservabilitySchema(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS request_logs (
 id INTEGER PRIMARY KEY AUTOINCREMENT, request_id TEXT NOT NULL, client_api_key_id TEXT NOT NULL DEFAULT '',
 method TEXT NOT NULL, path TEXT NOT NULL, protocol TEXT NOT NULL DEFAULT '', public_model_id TEXT NOT NULL DEFAULT '',
 provider_id TEXT NOT NULL DEFAULT '', credential_id TEXT NOT NULL DEFAULT '', attempt INTEGER NOT NULL DEFAULT 0,
 status INTEGER NOT NULL DEFAULT 0, latency_ms INTEGER NOT NULL DEFAULT 0, error_code TEXT NOT NULL DEFAULT '',
 metadata_json TEXT NOT NULL DEFAULT '{}', created_at TEXT NOT NULL
); CREATE INDEX IF NOT EXISTS idx_request_logs_created_at ON request_logs(created_at);
CREATE INDEX IF NOT EXISTS idx_request_logs_request_id ON request_logs(request_id);
CREATE TABLE IF NOT EXISTS audit_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT, actor TEXT NOT NULL DEFAULT '', action TEXT NOT NULL, resource_type TEXT NOT NULL DEFAULT '',
 resource_id TEXT NOT NULL DEFAULT '', status INTEGER NOT NULL DEFAULT 0, metadata_json TEXT NOT NULL DEFAULT '{}', created_at TEXT NOT NULL
); CREATE INDEX IF NOT EXISTS idx_audit_events_created_at ON audit_events(created_at);`)
	return err
}

func migrateScopedAliases(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS public_models (
 id TEXT PRIMARY KEY, display_name TEXT NOT NULL DEFAULT '', aliases_json TEXT NOT NULL DEFAULT '[]', enabled INTEGER NOT NULL DEFAULT 1,
 expose_upstream_name INTEGER NOT NULL DEFAULT 0, rewrite_response_model INTEGER NOT NULL DEFAULT 1,
 capabilities_json TEXT NOT NULL DEFAULT '[]', limits_json TEXT NOT NULL DEFAULT '{}'
); CREATE TABLE IF NOT EXISTS model_aliases (
 alias TEXT NOT NULL, public_model_id TEXT NOT NULL, scope_api_key_id TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1,
 PRIMARY KEY(alias,scope_api_key_id), FOREIGN KEY(public_model_id) REFERENCES public_models(id) ON DELETE CASCADE
); CREATE TABLE IF NOT EXISTS route_targets (
 id TEXT PRIMARY KEY, public_model_id TEXT NOT NULL, provider_id TEXT NOT NULL, upstream_model TEXT NOT NULL,
 priority INTEGER NOT NULL DEFAULT 0, weight INTEGER NOT NULL DEFAULT 1, enabled INTEGER NOT NULL DEFAULT 1,
 conditions_json TEXT NOT NULL DEFAULT '{}', FOREIGN KEY(public_model_id) REFERENCES public_models(id) ON DELETE CASCADE,
 FOREIGN KEY(provider_id) REFERENCES providers(id) ON DELETE CASCADE
); CREATE TABLE model_aliases_v2 (
 alias TEXT NOT NULL, public_model_id TEXT NOT NULL, scope_api_key_id TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1,
 PRIMARY KEY(alias,scope_api_key_id), FOREIGN KEY(public_model_id) REFERENCES public_models(id) ON DELETE CASCADE
); INSERT OR IGNORE INTO model_aliases_v2(alias,public_model_id,scope_api_key_id,enabled)
SELECT alias,public_model_id,scope_api_key_id,enabled FROM model_aliases;
DROP TABLE model_aliases;
ALTER TABLE model_aliases_v2 RENAME TO model_aliases;
CREATE INDEX IF NOT EXISTS idx_model_aliases_model ON model_aliases(public_model_id);`)
	return err
}

func ensureUsageSchema(ctx context.Context, tx *sql.Tx) error {
	columns := []struct {
		name      string
		statement string
	}{
		{"public_model_id", `ALTER TABLE usage_events ADD COLUMN public_model_id TEXT NOT NULL DEFAULT ''`},
		{"provider_id", `ALTER TABLE usage_events ADD COLUMN provider_id TEXT NOT NULL DEFAULT ''`},
		{"upstream_model", `ALTER TABLE usage_events ADD COLUMN upstream_model TEXT NOT NULL DEFAULT ''`},
		{"credential_id", `ALTER TABLE usage_events ADD COLUMN credential_id TEXT NOT NULL DEFAULT ''`},
		{"client_api_key_id", `ALTER TABLE usage_events ADD COLUMN client_api_key_id TEXT NOT NULL DEFAULT ''`},
		{"attempt", `ALTER TABLE usage_events ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0`},
		{"status", `ALTER TABLE usage_events ADD COLUMN status INTEGER NOT NULL DEFAULT 0`},
		{"input_tokens", `ALTER TABLE usage_events ADD COLUMN input_tokens INTEGER NOT NULL DEFAULT 0`},
		{"output_tokens", `ALTER TABLE usage_events ADD COLUMN output_tokens INTEGER NOT NULL DEFAULT 0`},
		{"reasoning_tokens", `ALTER TABLE usage_events ADD COLUMN reasoning_tokens INTEGER NOT NULL DEFAULT 0`},
		{"latency_ms", `ALTER TABLE usage_events ADD COLUMN latency_ms INTEGER NOT NULL DEFAULT 0`},
		{"error_code", `ALTER TABLE usage_events ADD COLUMN error_code TEXT NOT NULL DEFAULT ''`},
	}
	for _, column := range columns {
		if err := addColumnIfMissing(ctx, tx, "usage_events", column.name, column.statement); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_usage_created_at ON usage_events(created_at); CREATE INDEX IF NOT EXISTS idx_usage_request_id ON usage_events(request_id); CREATE INDEX IF NOT EXISTS idx_usage_client_created ON usage_events(client_api_key_id,created_at);`)
	return err
}

func createComboSchema(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS combos (
 id TEXT PRIMARY KEY, display_name TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1,
 rewrite_response_model INTEGER NOT NULL DEFAULT 1, capabilities_json TEXT NOT NULL DEFAULT '[]',
 limits_json TEXT NOT NULL DEFAULT '{}', policy_json TEXT NOT NULL DEFAULT '{}'
); CREATE TABLE IF NOT EXISTS combo_items (
 combo_id TEXT NOT NULL, position INTEGER NOT NULL, public_model_id TEXT NOT NULL DEFAULT '', route_target_id TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(combo_id,position), FOREIGN KEY(combo_id) REFERENCES combos(id) ON DELETE CASCADE
); CREATE INDEX IF NOT EXISTS idx_combo_items_model ON combo_items(public_model_id);`)
	return err
}

func createConfigVersionSchema(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS config_versions (
 id INTEGER PRIMARY KEY AUTOINCREMENT, source TEXT NOT NULL, digest TEXT NOT NULL, created_at TEXT NOT NULL
); CREATE INDEX IF NOT EXISTS idx_config_versions_created_at ON config_versions(created_at);`)
	return err
}

func migrateTeamScopedAliases(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE model_aliases_v3 (
 alias TEXT NOT NULL, public_model_id TEXT NOT NULL, scope_api_key_id TEXT NOT NULL DEFAULT '', scope_team_id TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1,
 PRIMARY KEY(alias,scope_api_key_id,scope_team_id), FOREIGN KEY(public_model_id) REFERENCES public_models(id) ON DELETE CASCADE
); INSERT OR IGNORE INTO model_aliases_v3(alias,public_model_id,scope_api_key_id,scope_team_id,enabled)
SELECT alias,public_model_id,scope_api_key_id,'',enabled FROM model_aliases;
DROP TABLE model_aliases;
ALTER TABLE model_aliases_v3 RENAME TO model_aliases;
CREATE INDEX IF NOT EXISTS idx_model_aliases_model ON model_aliases(public_model_id);
CREATE INDEX IF NOT EXISTS idx_model_aliases_team ON model_aliases(scope_team_id,alias);`)
	return err
}

func addColumnIfMissing(ctx context.Context, tx *sql.Tx, table, column, statement string) error {
	exists, err := sqliteColumnExists(ctx, tx, table, column)
	if err != nil || exists {
		return err
	}
	_, err = tx.ExecContext(ctx, statement)
	return err
}

func sqliteColumnExists(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, table, column string) (bool, error) {
	rows, err := queryer.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, primaryKey int
		var defaultValue any
		if err = rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func createCredentialModelCooldownSchema(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS credential_model_cooldowns (
 credential_id TEXT NOT NULL,
 model TEXT NOT NULL,
 until TEXT NOT NULL,
 code TEXT NOT NULL DEFAULT '',
 message TEXT NOT NULL DEFAULT '',
 status INTEGER NOT NULL DEFAULT 0,
 count INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY (credential_id, model)
);`)
	return err
}

func createAppSettingsSchema(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS app_settings (
 key TEXT PRIMARY KEY,
 value_json TEXT NOT NULL DEFAULT '{}'
);`)
	return err
}

func migrateCredentialRotationState(ctx context.Context, tx *sql.Tx) error {
	if err := addColumnIfMissing(ctx, tx, "credentials", "last_used_at", `ALTER TABLE credentials ADD COLUMN last_used_at TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	return addColumnIfMissing(ctx, tx, "credentials", "consecutive_use_count", `ALTER TABLE credentials ADD COLUMN consecutive_use_count INTEGER NOT NULL DEFAULT 0`)
}

func sqliteSchemaVersion(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int, error) {
	var version int
	err := queryer.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
	return version, err
}

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sqliteSchemaVersion(ctx, s.db)
}
