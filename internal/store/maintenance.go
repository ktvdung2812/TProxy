package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Backup creates a SQLite-consistent snapshot. It is safe to call while the
// gateway is serving traffic; VACUUM INTO reads a consistent database snapshot
// and does not copy an incomplete WAL file.
func (s *Store) Backup(ctx context.Context, destination string) error {
	if s == nil || s.db == nil {
		return errors.New("store is not initialized")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return backupSQLiteDB(ctx, s.db, s.path, destination)
}

func backupSQLiteDB(ctx context.Context, db *sql.DB, sourcePath, destination string) error {
	destination = filepath.Clean(strings.TrimSpace(destination))
	if destination == "" || destination == "." {
		return errors.New("backup destination is required")
	}
	if sourcePath != "" {
		sourceAbs, _ := filepath.Abs(sourcePath)
		destinationAbs, _ := filepath.Abs(destination)
		if sourceAbs == destinationAbs {
			return errors.New("backup destination must differ from the active database")
		}
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	temporary := fmt.Sprintf("%s.tmp-%d", destination, time.Now().UnixNano())
	_ = os.Remove(temporary)
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", temporary); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("create sqlite backup: %w", err)
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("protect sqlite backup: %w", err)
	}
	if err := IntegrityCheckSQLite(temporary); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("verify sqlite backup: %w", err)
	}
	if err := os.Rename(temporary, destination); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("publish sqlite backup: %w", err)
	}
	return nil
}

// IntegrityCheck runs SQLite's integrity and foreign-key checks and validates
// that the schema is one understood by this binary.
func (s *Store) IntegrityCheck(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("store is not initialized")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return integrityCheckDB(ctx, s.db, true)
}

func IntegrityCheckSQLite(path string) error {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return errors.New("sqlite path is required")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("stat sqlite database: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open sqlite database: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		return fmt.Errorf("configure sqlite inspection: %w", err)
	}
	return integrityCheckDB(context.Background(), db, true)
}

func integrityCheckDB(ctx context.Context, db *sql.DB, validateVersion bool) error {
	if validateVersion {
		version, err := sqliteSchemaVersion(ctx, db)
		if err != nil {
			return fmt.Errorf("read sqlite schema version: %w", err)
		}
		if version <= 0 || version > sqliteCurrentSchemaVersion {
			return fmt.Errorf("unsupported sqlite schema version %d", version)
		}
	}
	rows, err := db.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return fmt.Errorf("run sqlite integrity_check: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var result string
		if err = rows.Scan(&result); err != nil {
			return err
		}
		if !strings.EqualFold(strings.TrimSpace(result), "ok") {
			return fmt.Errorf("sqlite integrity_check: %s", result)
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	foreignRows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("run sqlite foreign_key_check: %w", err)
	}
	defer foreignRows.Close()
	if foreignRows.Next() {
		return errors.New("sqlite foreign_key_check found violations")
	}
	return foreignRows.Err()
}

// RestoreSQLite restores a consistent snapshot into destination. The active
// gateway must be stopped before calling this function. The destination is
// replaced only after the temporary copy passes integrity and schema checks.
func RestoreSQLite(ctx context.Context, source, destination string) error {
	source = filepath.Clean(strings.TrimSpace(source))
	destination = filepath.Clean(strings.TrimSpace(destination))
	if source == "" || destination == "" || source == "." || destination == "." {
		return errors.New("source and destination sqlite paths are required")
	}
	sourceAbs, _ := filepath.Abs(source)
	destinationAbs, _ := filepath.Abs(destination)
	if sourceAbs == destinationAbs {
		return errors.New("restore source must differ from destination")
	}
	if _, err := os.Stat(source); err != nil {
		return fmt.Errorf("stat restore source: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("create restore directory: %w", err)
	}
	temporary := fmt.Sprintf("%s.restore-%d", destination, time.Now().UnixNano())
	_ = os.Remove(temporary)
	if err := snapshotSQLiteFile(ctx, source, temporary); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := IntegrityCheckSQLite(temporary); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("verify restore snapshot: %w", err)
	}
	if err := replaceSQLiteFile(temporary, destination); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func snapshotSQLiteFile(ctx context.Context, source, destination string) error {
	db, err := sql.Open("sqlite", source)
	if err != nil {
		return fmt.Errorf("open restore source: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		return fmt.Errorf("configure restore source: %w", err)
	}
	if _, err = db.ExecContext(ctx, "VACUUM INTO ?", destination); err != nil {
		return fmt.Errorf("snapshot restore source: %w", err)
	}
	return os.Chmod(destination, 0o600)
}

func replaceSQLiteFile(temporary, destination string) error {
	backup := fmt.Sprintf("%s.pre-restore-%d", destination, time.Now().UnixNano())
	type movedFile struct{ original, backup string }
	var moved []movedFile
	for _, path := range []string{destination, destination + "-wal", destination + "-shm"} {
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("inspect existing sqlite file %s: %w", path, err)
		}
		backupPath := backup
		if path != destination {
			backupPath += strings.TrimPrefix(path, destination)
		}
		if err := os.Rename(path, backupPath); err != nil {
			for i := len(moved) - 1; i >= 0; i-- {
				_ = os.Rename(moved[i].backup, moved[i].original)
			}
			return fmt.Errorf("stage existing sqlite file: %w", err)
		}
		moved = append(moved, movedFile{original: path, backup: backupPath})
	}
	if err := os.Rename(temporary, destination); err != nil {
		for i := len(moved) - 1; i >= 0; i-- {
			_ = os.Rename(moved[i].backup, moved[i].original)
		}
		return fmt.Errorf("activate restored sqlite file: %w", err)
	}
	for _, item := range moved {
		_ = os.Remove(item.backup)
	}
	return nil
}
