package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

type authBundle struct {
	Version     int                    `json:"version"`
	ExportedAt  time.Time              `json:"exported_at"`
	Credentials []authBundleCredential `json:"credentials"`
}

type authBundleCredential struct {
	ID               string `json:"id"`
	ProviderID       string `json:"provider_id"`
	AuthType         string `json:"auth_type"`
	Label            string `json:"label,omitempty"`
	Email            string `json:"email,omitempty"`
	SecretCiphertext string `json:"secret_ciphertext"`
	MetadataJSON     string `json:"metadata_json"`
	Priority         int    `json:"priority"`
	Weight           int    `json:"weight"`
	Enabled          bool   `json:"enabled"`
	Status           string `json:"status"`
}

func (s *Store) ExportAuthFile(ctx context.Context, path string) error {
	data, err := s.ExportAuthBundle(ctx)
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err = os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	if err = os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func (s *Store) ImportAuthFile(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return s.ImportAuthBundle(ctx, data)
}

func (s *Store) ExportAuthBundle(ctx context.Context) ([]byte, error) {
	bundle, err := s.buildAuthBundle(ctx)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(bundle, "", "  ")
}

func (s *Store) ImportAuthBundle(ctx context.Context, data []byte) error {
	var bundle authBundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		return fmt.Errorf("parse auth bundle: %w", err)
	}
	return s.applyAuthBundle(ctx, bundle)
}

func (s *Store) buildAuthBundle(ctx context.Context) (authBundle, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,provider_id,auth_type,label,email,secret_ciphertext,metadata_json,priority,weight,enabled,status FROM credentials WHERE auth_type='oauth' ORDER BY id`)
	if err != nil {
		return authBundle{}, err
	}
	defer rows.Close()
	bundle := authBundle{Version: 1, ExportedAt: time.Now().UTC()}
	for rows.Next() {
		var item authBundleCredential
		var enabled int
		if err := rows.Scan(&item.ID, &item.ProviderID, &item.AuthType, &item.Label, &item.Email, &item.SecretCiphertext, &item.MetadataJSON, &item.Priority, &item.Weight, &enabled, &item.Status); err != nil {
			return authBundle{}, err
		}
		item.Enabled = enabled != 0
		if item.SecretCiphertext == "" {
			continue
		}
		bundle.Credentials = append(bundle.Credentials, item)
	}
	return bundle, rows.Err()
}

func (s *Store) applyAuthBundle(ctx context.Context, bundle authBundle) error {
	if bundle.Version != 1 {
		return fmt.Errorf("unsupported auth bundle version %d", bundle.Version)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	rollback := func(cause error) error { _ = tx.Rollback(); return cause }
	for _, item := range bundle.Credentials {
		if item.ID == "" || item.ProviderID == "" || item.SecretCiphertext == "" {
			return rollback(errors.New("auth bundle credential is incomplete"))
		}
		var providerExists int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM providers WHERE id=?`, item.ProviderID).Scan(&providerExists); err != nil || providerExists == 0 {
			if err == nil {
				err = fmt.Errorf("provider %q is not configured", item.ProviderID)
			}
			return rollback(err)
		}
		if _, err = s.encryptor.Decrypt(item.SecretCiphertext); err != nil {
			return rollback(fmt.Errorf("credential %q cannot be decrypted with the active master key", item.ID))
		}
		if item.Weight <= 0 {
			item.Weight = 1
		}
		if item.Status == "" {
			item.Status = "healthy"
		}
		if item.MetadataJSON == "" {
			item.MetadataJSON = "{}"
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO credentials(id,provider_id,auth_type,status,label,email,secret_ciphertext,metadata_json,priority,weight,enabled,last_error_code,last_error,last_validated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,'','','') ON CONFLICT(id) DO UPDATE SET provider_id=excluded.provider_id,auth_type=excluded.auth_type,status=excluded.status,label=excluded.label,email=excluded.email,secret_ciphertext=excluded.secret_ciphertext,metadata_json=excluded.metadata_json,priority=excluded.priority,weight=excluded.weight,enabled=excluded.enabled`, item.ID, item.ProviderID, item.AuthType, item.Status, item.Label, item.Email, item.SecretCiphertext, item.MetadataJSON, item.Priority, item.Weight, boolInt(item.Enabled)); err != nil {
			return rollback(err)
		}
	}
	return tx.Commit()
}
