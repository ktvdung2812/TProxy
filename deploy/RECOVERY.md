# tproxy SQLite recovery

`tproxy` uses one SQLite database file per deployment. OAuth and API-key secrets are stored as authenticated-encrypted ciphertext; the database backup does not contain decrypted credentials.

## Backup

Run a live, consistent backup from the project directory:

```bash
go run ./cmd/tproxy --config config.yaml --backup-database backups/tproxy-$(date +%Y%m%d-%H%M%S).db
```

The command uses SQLite `VACUUM INTO`, verifies `PRAGMA integrity_check` and `PRAGMA foreign_key_check`, writes the backup with mode `0600`, and publishes it only after verification. Do not copy only `tproxy.db` while `tproxy.db-wal` exists.

## Verification

```bash
go run ./cmd/tproxy --config config.yaml --integrity-check
```

The check validates the schema version, SQLite integrity and foreign-key constraints.

## Restore

Stop the running gateway first, then restore:

```bash
go run ./cmd/tproxy --config config.yaml --restore-database backups/tproxy-YYYYMMDD-HHMMSS.db
```

Restore snapshots the source, validates the temporary copy, stages the current database and its WAL/SHM sidecars, then atomically activates the restored file. A failed activation rolls back the staged files.

The restored database keeps encrypted credential ciphertext. Set the same `TPROXY_MASTER_KEY` before starting `tproxy`; without that key, credentials cannot be decrypted or refreshed.

## Migration recovery

Migrations run transaction-by-transaction before the HTTP server starts. A failed migration leaves the previous schema version active and stops startup. Keep a verified backup before upgrading, and never delete a database after a migration failure until the backup has been checked.
