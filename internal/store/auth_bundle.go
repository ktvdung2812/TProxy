package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/security"
)

// MaxAuthBundleBytes bounds OAuth bundle imports and exports exposed through
// the CLI and management API.
const MaxAuthBundleBytes = 8 << 20

const maxAuthBundleBytes = MaxAuthBundleBytes

type authBundle struct {
	Version     int                    `json:"version"`
	ExportedAt  time.Time              `json:"exported_at"`
	Providers   []authBundleProvider   `json:"providers,omitempty"`
	Credentials []authBundleCredential `json:"credentials"`
}

type authBundleProvider struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name,omitempty"`
	Enabled bool   `json:"enabled"`
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
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return errors.New("auth bundle destination is required")
	}
	data, err := s.ExportAuthBundle(ctx)
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err = os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create auth bundle directory: %w", err)
	}
	temporaryFile, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create auth bundle temporary file: %w", err)
	}
	temporary := temporaryFile.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporary)
		}
	}()
	if err = temporaryFile.Chmod(0o600); err == nil {
		_, err = temporaryFile.Write(data)
	}
	if err == nil {
		err = temporaryFile.Sync()
	}
	if closeErr := temporaryFile.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write auth bundle: %w", err)
	}
	if err = os.Rename(temporary, path); err != nil {
		return err
	}
	removeTemporary = false
	return nil
}

func (s *Store) ImportAuthFile(ctx context.Context, path string) error {
	path = filepath.Clean(strings.TrimSpace(path))
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxAuthBundleBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxAuthBundleBytes {
		return fmt.Errorf("auth bundle exceeds %d bytes", maxAuthBundleBytes)
	}
	return s.ImportAuthBundle(ctx, data)
}

func (s *Store) ExportAuthBundle(ctx context.Context) ([]byte, error) {
	bundle, err := s.buildAuthBundle(ctx)
	if err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(data) > maxAuthBundleBytes {
		return nil, fmt.Errorf("auth bundle exceeds %d bytes", maxAuthBundleBytes)
	}
	return data, nil
}

func (s *Store) ImportAuthBundle(ctx context.Context, data []byte) error {
	if len(data) > maxAuthBundleBytes {
		return fmt.Errorf("auth bundle exceeds %d bytes", maxAuthBundleBytes)
	}
	var bundle authBundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		return fmt.Errorf("parse auth bundle: %w", err)
	}
	return s.applyAuthBundle(ctx, bundle)
}

func (s *Store) buildAuthBundle(ctx context.Context) (authBundle, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id,c.provider_id,c.auth_type,c.label,c.email,c.secret_ciphertext,c.metadata_json,c.priority,c.weight,c.enabled,c.status,p.type,p.name,p.enabled FROM credentials c JOIN providers p ON p.id=c.provider_id WHERE c.auth_type='oauth' ORDER BY c.provider_id,c.id`)
	if err != nil {
		return authBundle{}, err
	}
	defer rows.Close()
	bundle := authBundle{Version: 1, ExportedAt: time.Now().UTC()}
	providerSeen := make(map[string]struct{})
	for rows.Next() {
		var item authBundleCredential
		var enabled, providerEnabled int
		var providerType, providerName string
		if err := rows.Scan(&item.ID, &item.ProviderID, &item.AuthType, &item.Label, &item.Email, &item.SecretCiphertext, &item.MetadataJSON, &item.Priority, &item.Weight, &enabled, &item.Status, &providerType, &providerName, &providerEnabled); err != nil {
			return authBundle{}, err
		}
		item.Enabled = enabled != 0
		if _, exists := providerSeen[item.ProviderID]; !exists {
			providerSeen[item.ProviderID] = struct{}{}
			bundle.Providers = append(bundle.Providers, authBundleProvider{ID: item.ProviderID, Type: providerType, Name: providerName, Enabled: providerEnabled != 0})
		}
		if item.SecretCiphertext == "" {
			continue
		}
		item.MetadataJSON, err = normalizeAuthBundleMetadata(item.MetadataJSON, false)
		if err != nil {
			return authBundle{}, fmt.Errorf("credential %q metadata is invalid: %w", item.ID, err)
		}
		item.SecretCiphertext, err = s.normalizeOAuthCiphertext(item.SecretCiphertext)
		if err != nil {
			return authBundle{}, fmt.Errorf("credential %q token envelope is invalid: %w", item.ID, err)
		}
		item.Status = authBundleStatus(item.Status, item.Enabled)
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
	var providerCount int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM providers`).Scan(&providerCount); err != nil {
		return rollback(err)
	}
	// Snapshot the destination topology once: fresh stores seed every provider
	// from the bundle, while existing stores keep their configured providers.
	seedBundleProviders := providerCount == 0
	providerSeen := make(map[string]struct{}, len(bundle.Providers))
	for _, provider := range bundle.Providers {
		provider.ID = strings.TrimSpace(provider.ID)
		provider.Type = strings.TrimSpace(provider.Type)
		if provider.ID == "" || provider.Type == "" {
			return rollback(errors.New("auth bundle provider is incomplete"))
		}
		if _, exists := providerSeen[provider.ID]; exists {
			return rollback(fmt.Errorf("auth bundle contains duplicate provider %q", provider.ID))
		}
		providerSeen[provider.ID] = struct{}{}
		var existingType string
		existingErr := tx.QueryRowContext(ctx, `SELECT type FROM providers WHERE id=?`, provider.ID).Scan(&existingType)
		if existingErr == nil {
			if existingType != provider.Type {
				return rollback(fmt.Errorf("provider %q type does not match configured topology", provider.ID))
			}
			continue
		}
		if !errors.Is(existingErr, sql.ErrNoRows) {
			return rollback(existingErr)
		}
		if !seedBundleProviders {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO providers(id,type,name,base_url,enabled,status,last_error,last_checked_at,headers_json,config_json,created_at,updated_at)
VALUES(?,?,?,'',?,'unknown','','','{}','{}',?,?)`, provider.ID, provider.Type, provider.Name, boolInt(provider.Enabled), time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return rollback(err)
		}
	}
	seen := make(map[string]struct{}, len(bundle.Credentials))
	for _, item := range bundle.Credentials {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.ProviderID) == "" || item.SecretCiphertext == "" {
			return rollback(errors.New("auth bundle credential is incomplete"))
		}
		if item.AuthType != "oauth" {
			return rollback(fmt.Errorf("credential %q has unsupported auth type %q", item.ID, item.AuthType))
		}
		if _, exists := seen[item.ID]; exists {
			return rollback(fmt.Errorf("auth bundle contains duplicate credential %q", item.ID))
		}
		seen[item.ID] = struct{}{}
		if err = validateAuthBundleStatus(item.Status); err != nil {
			return rollback(fmt.Errorf("credential %q status is invalid: %w", item.ID, err))
		}
		item.MetadataJSON, err = normalizeAuthBundleMetadata(item.MetadataJSON, true)
		if err != nil {
			return rollback(fmt.Errorf("credential %q metadata is invalid: %w", item.ID, err))
		}
		var providerExists int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM providers WHERE id=?`, item.ProviderID).Scan(&providerExists); err != nil || providerExists == 0 {
			if err == nil {
				err = fmt.Errorf("provider %q is not configured", item.ProviderID)
			}
			return rollback(err)
		}
		item.SecretCiphertext, err = s.normalizeOAuthCiphertext(item.SecretCiphertext)
		if err != nil {
			return rollback(fmt.Errorf("credential %q cannot be decrypted with the active master key", item.ID))
		}
		var existingProvider, existingAuth string
		existingErr := tx.QueryRowContext(ctx, `SELECT provider_id,auth_type FROM credentials WHERE id=?`, item.ID).Scan(&existingProvider, &existingAuth)
		if existingErr == nil {
			if existingProvider != item.ProviderID {
				return rollback(fmt.Errorf("credential %q belongs to provider %q", item.ID, existingProvider))
			}
			if existingAuth != "oauth" {
				return rollback(fmt.Errorf("credential %q is not an oauth credential", item.ID))
			}
		} else if !errors.Is(existingErr, sql.ErrNoRows) {
			return rollback(existingErr)
		}
		if item.Weight <= 0 {
			item.Weight = 1
		}
		item.Status = authBundleStatus(item.Status, item.Enabled)
		if item.MetadataJSON == "" {
			item.MetadataJSON = "{}"
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO credentials(id,provider_id,auth_type,status,label,email,secret_ciphertext,metadata_json,priority,weight,enabled,last_error_code,last_error,last_validated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,'','','') ON CONFLICT(id) DO UPDATE SET provider_id=excluded.provider_id,auth_type=excluded.auth_type,status=excluded.status,label=excluded.label,email=excluded.email,secret_ciphertext=excluded.secret_ciphertext,metadata_json=excluded.metadata_json,priority=excluded.priority,weight=excluded.weight,enabled=excluded.enabled,cooldown_until='',last_error_code='',last_error='',last_validated_at=''`, item.ID, item.ProviderID, item.AuthType, item.Status, item.Label, item.Email, item.SecretCiphertext, item.MetadataJSON, item.Priority, item.Weight, boolInt(item.Enabled)); err != nil {
			return rollback(err)
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM credential_model_cooldowns WHERE credential_id=?`, item.ID); err != nil {
			return rollback(err)
		}
	}
	return tx.Commit()
}

func (s *Store) normalizeOAuthCiphertext(ciphertext string) (string, error) {
	plaintext, err := s.encryptor.Decrypt(ciphertext)
	if err != nil {
		return "", err
	}
	var token OAuthToken
	if err = json.Unmarshal([]byte(plaintext), &token); err == nil && strings.TrimSpace(token.AccessToken) != "" {
		return ciphertext, nil
	}
	trimmed := strings.TrimSpace(plaintext)
	if trimmed == "" || strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return "", errors.New("OAuth ciphertext does not contain a non-empty access token")
	}
	encoded, err := json.Marshal(OAuthToken{AccessToken: trimmed, TokenType: "Bearer"})
	if err != nil {
		return "", err
	}
	canonical, err := s.encryptor.Encrypt(string(encoded))
	if err != nil {
		return "", err
	}
	return canonical, nil
}

func authBundleStatus(status string, enabled bool) string {
	if !enabled {
		return "disabled"
	}
	return "healthy"
}

func validateAuthBundleStatus(status string) error {
	if status == "" {
		return nil
	}
	switch status {
	case "healthy", "unknown", "cooldown", "auth_required", "disabled":
		return nil
	default:
		return fmt.Errorf("unsupported status %q", status)
	}
}

func validateAuthBundleMetadata(raw string) error {
	_, err := normalizeAuthBundleMetadata(raw, true)
	return err
}

func normalizeAuthBundleMetadata(raw string, rejectSecrets bool) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "{}", nil
	}
	value, err := decodeJSONValue([]byte(raw))
	if err != nil {
		return "", errors.New("metadata_json must be valid JSON")
	}
	metadata, ok := value.(map[string]any)
	if !ok || metadata == nil {
		return "", errors.New("metadata_json must be a JSON object")
	}
	sanitized, containsSecret := sanitizeAuthBundleMetadata(metadata)
	if rejectSecrets && containsSecret {
		return "", errors.New("metadata_json contains plaintext credential material")
	}
	encoded, err := json.Marshal(sanitized)
	if err != nil {
		return "", fmt.Errorf("encode metadata_json: %w", err)
	}
	return string(encoded), nil
}

func sanitizeAuthBundleMetadata(value any) (any, bool) {
	switch item := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(item))
		containsSecret := false
		for key, nested := range item {
			if sensitiveFieldName(key) {
				if isRedactedMetadataValue(nested) {
					result[key] = nested
				} else {
					result[key] = "[REDACTED]"
					containsSecret = true
				}
				continue
			}
			sanitized, nestedSecret := sanitizeAuthBundleMetadata(nested)
			result[key] = sanitized
			containsSecret = containsSecret || nestedSecret
		}
		return result, containsSecret
	case []any:
		result := make([]any, len(item))
		containsSecret := false
		for index, nested := range item {
			sanitized, nestedSecret := sanitizeAuthBundleMetadata(nested)
			result[index] = sanitized
			containsSecret = containsSecret || nestedSecret
		}
		return result, containsSecret
	case string:
		sanitized := security.RedactText(item)
		return sanitized, sanitized != item
	default:
		return value, false
	}
}

func isRedactedMetadataValue(value any) bool {
	text, ok := value.(string)
	return ok && (text == "[REDACTED]" || strings.HasSuffix(text, "_env"))
}

func decodeJSONValue(raw []byte) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	value, err := decodeJSONToken(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, errors.New("metadata_json has trailing data")
		}
		return nil, err
	}
	return value, nil
}

func decodeJSONToken(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch delimiter := token.(type) {
	case json.Delim:
		switch delimiter {
		case '{':
			result := map[string]any{}
			seen := map[string]struct{}{}
			for decoder.More() {
				keyToken, keyErr := decoder.Token()
				key, ok := keyToken.(string)
				if keyErr != nil || !ok {
					return nil, errors.New("metadata_json object key is invalid")
				}
				if _, exists := seen[key]; exists {
					return nil, fmt.Errorf("metadata_json contains duplicate key %q", key)
				}
				seen[key] = struct{}{}
				value, valueErr := decodeJSONToken(decoder)
				if valueErr != nil {
					return nil, valueErr
				}
				result[key] = value
			}
			end, endErr := decoder.Token()
			if endErr != nil || end != json.Delim('}') {
				return nil, errors.New("metadata_json object is incomplete")
			}
			return result, nil
		case '[':
			result := []any{}
			for decoder.More() {
				value, valueErr := decodeJSONToken(decoder)
				if valueErr != nil {
					return nil, valueErr
				}
				result = append(result, value)
			}
			end, endErr := decoder.Token()
			if endErr != nil || end != json.Delim(']') {
				return nil, errors.New("metadata_json array is incomplete")
			}
			return result, nil
		default:
			return nil, errors.New("metadata_json contains an invalid delimiter")
		}
	default:
		return token, nil
	}
}
