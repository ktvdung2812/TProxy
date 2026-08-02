package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"database/sql"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/security"
	_ "modernc.org/sqlite"
)

type Provider struct {
	ID           string
	Type         string
	Name         string
	BaseURL      string
	Enabled      bool
	Status       string
	LastError    string
	LastChecked  time.Time
	Headers      map[string]string
	Config       map[string]any
	Limits       config.LimitPolicy
	OAuth        *config.OAuthConfig
	ProxyPoolIDs []string
}

type OAuthToken struct {
	AccessToken  string         `json:"access_token"`
	RefreshToken string         `json:"refresh_token,omitempty"`
	TokenType    string         `json:"token_type,omitempty"`
	ExpiresAt    time.Time      `json:"expires_at,omitempty"`
	Extra        map[string]any `json:"extra,omitempty"`
}

type Credential struct {
	ID            string
	ProviderID    string
	AuthType      string
	Status        string
	Label         string
	Email         string
	Secret        string
	TokenType     string
	OAuthToken    *OAuthToken
	Metadata      map[string]any
	ProxyPoolIDs  []string
	ProxyURL      string
	Priority      int
	Weight        int
	Enabled       bool
	CooldownUntil         time.Time
	LastErrorCode         string
	LastError             string
	LastValidated         time.Time
	LastUsedAt            time.Time
	ConsecutiveUseCount   int
	CreatedAt             time.Time
}

type ProxyPool struct {
	ID           string
	Name         string
	URL          string
	Enabled      bool
	Status       string
	LastError    string
	LastTestedAt time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type ProviderCredential struct {
	Provider   Provider
	Credential Credential
}

type PublicModel struct {
	ID                   string
	DisplayName          string
	Aliases              []string
	Enabled              bool
	ExposeUpstreamName   bool
	RewriteResponseModel bool
	Capabilities         []string
	Limits               map[string]any
	ComboItems           []ComboItem
	Policy               map[string]any
}

type ComboItem struct {
	Position      int    `json:"position"`
	PublicModelID string `json:"public_model_id"`
	RouteTargetID string `json:"route_target_id,omitempty"`
}

type Combo struct {
	ID                   string         `json:"id"`
	DisplayName          string         `json:"display_name"`
	Enabled              bool           `json:"enabled"`
	RewriteResponseModel bool           `json:"rewrite_response_model"`
	Capabilities         []string       `json:"capabilities"`
	Limits               map[string]any `json:"limits"`
	Policy               map[string]any `json:"policy"`
	Items                []ComboItem    `json:"items"`
}

type ModelAlias struct {
	Alias         string `json:"alias"`
	PublicModelID string `json:"public_model_id"`
	APIKeyID      string `json:"api_key_id,omitempty"`
	TeamID        string `json:"team_id,omitempty"`
	Enabled       bool   `json:"enabled"`
}

type RouteTarget struct {
	ID            string
	PublicModelID string
	ProviderID    string
	UpstreamModel string
	Priority      int
	Weight        int
	Enabled       bool
	Conditions    map[string]any
	Pricing       *config.PricingConfig
}

type APIKey struct {
	ID       string
	Name     string
	Hash     string
	Enabled  bool
	Models   []string
	Policy   config.ClientKeyPolicy
	LastUsed time.Time
}

type UsageEvent struct {
	RequestID        string    `json:"request_id"`
	ClientAPIKeyID   string    `json:"client_api_key_id,omitempty"`
	PublicModelID    string    `json:"public_model_id"`
	ProviderID       string    `json:"provider_id"`
	UpstreamModel    string    `json:"upstream_model"`
	CredentialID     string    `json:"credential_id"`
	Attempt          int       `json:"attempt"`
	Status           int       `json:"status"`
	InputTokens      int       `json:"input_tokens"`
	OutputTokens     int       `json:"output_tokens"`
	ReasoningTokens  int       `json:"reasoning_tokens"`
	CachedTokens     int       `json:"cached_tokens"`
	TokensSaved      int       `json:"tokens_saved"`
	EstimatedCostUSD float64   `json:"estimated_cost_usd"`
	LatencyMS        int64     `json:"latency_ms"`
	ErrorCode        string    `json:"error_code,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type RequestLog struct {
	RequestID      string         `json:"request_id"`
	ClientAPIKeyID string         `json:"client_api_key_id,omitempty"`
	Method         string         `json:"method"`
	Path           string         `json:"path"`
	Protocol       string         `json:"protocol,omitempty"`
	PublicModelID  string         `json:"public_model_id,omitempty"`
	ProviderID     string         `json:"provider_id,omitempty"`
	CredentialID   string         `json:"credential_id,omitempty"`
	Attempt        int            `json:"attempt,omitempty"`
	Status         int            `json:"status"`
	LatencyMS      int64          `json:"latency_ms"`
	ErrorCode      string         `json:"error_code,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

type AuditEvent struct {
	ID           int64          `json:"id"`
	Actor        string         `json:"actor,omitempty"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type,omitempty"`
	ResourceID   string         `json:"resource_id,omitempty"`
	Status       int            `json:"status"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

type ConfigVersion struct {
	ID        int64     `json:"id"`
	Source    string    `json:"source"`
	Digest    string    `json:"digest"`
	CreatedAt time.Time `json:"created_at"`
}

type MediaJob struct {
	ID             string
	Kind           string
	Status         string
	PublicModelID  string
	ProviderID     string
	CredentialID   string
	UpstreamID     string
	ClientAPIKeyID string
	IdempotencyKey string
	ResponseJSON   string
	ErrorCode      string
	ErrorMessage   string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Store struct {
	db        *sql.DB
	encryptor *security.Encryptor
	path      string
	mu        sync.RWMutex
}

func OpenSQLite(path string, encryptor *security.Encryptor) (*Store, error) {
	if directory := filepath.Dir(path); directory != "." {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, fmt.Errorf("create sqlite directory: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure sqlite: %w", err)
	}
	store := &Store{db: db, encryptor: encryptor, path: path}
	if err = store.Migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Migrate(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return migrateSQLite(ctx, s.db)
}

func (s *Store) Seed(ctx context.Context, cfg *config.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	rollback := func(err error) error { _ = tx.Rollback(); return err }
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, poolCfg := range cfg.ProxyPools {
		poolURL := poolCfg.URL
		if poolURL == "" && poolCfg.URLEnv != "" {
			poolURL = config.Env(poolCfg.URLEnv)
		}
		ciphertext, encryptErr := s.encryptor.Encrypt(poolURL)
		if encryptErr != nil {
			return rollback(fmt.Errorf("encrypt proxy pool %s: %w", poolCfg.ID, encryptErr))
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO proxy_pools(id,name,url_ciphertext,enabled,status,created_at,updated_at)
VALUES(?,?,?,?,'unknown',?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,url_ciphertext=excluded.url_ciphertext,enabled=excluded.enabled,updated_at=excluded.updated_at`,
			poolCfg.ID, poolCfg.Name, ciphertext, boolInt(poolCfg.IsEnabled()), now, now); err != nil {
			return rollback(fmt.Errorf("seed proxy pool %s: %w", poolCfg.ID, err))
		}
	}
	for _, providerCfg := range cfg.Providers {
		config.ApplyProviderDefaults(&providerCfg)
		headers, _ := json.Marshal(providerCfg.Headers)
		providerConfig, _ := json.Marshal(map[string]any{"oauth": providerCfg.OAuth, "proxy_pool_ids": providerCfg.ProxyPools, "config": providerCfg.Config, "limits": providerCfg.Limits})
		if _, err = tx.ExecContext(ctx, `INSERT INTO providers(id,type,name,base_url,enabled,status,last_error,last_checked_at,headers_json,config_json,created_at,updated_at)
VALUES(?,?,?,?,?,'unknown','','',?,?,?,?) ON CONFLICT(id) DO UPDATE SET type=excluded.type,name=excluded.name,base_url=excluded.base_url,enabled=excluded.enabled,headers_json=excluded.headers_json,config_json=excluded.config_json,updated_at=excluded.updated_at`,
			providerCfg.ID, providerCfg.Type, providerCfg.Name, providerCfg.BaseURL, boolInt(providerCfg.Enabled), string(headers), string(providerConfig), now, now); err != nil {
			return rollback(fmt.Errorf("seed provider %s: %w", providerCfg.ID, err))
		}
		for _, credentialCfg := range providerCfg.Credentials {
			if !seedCredentialEligible(providerCfg, credentialCfg) {
				continue
			}
			secret := credentialCfg.Secret
			if secret == "" {
				secret = config.Env(credentialCfg.SecretEnv)
			}
			ciphertext := ""
			if secret != "" {
				ciphertext, err = s.encryptor.Encrypt(secret)
				if err != nil {
					return rollback(fmt.Errorf("encrypt credential %s: %w", credentialCfg.ID, err))
				}
			}
			metadata, _ := json.Marshal(redactPersistedMetadata(credentialMetadata(credentialCfg)))
			if credentialCfg.Weight <= 0 {
				credentialCfg.Weight = 1
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO credentials(id,provider_id,auth_type,label,email,secret_ciphertext,metadata_json,priority,weight,enabled,status,created_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET provider_id=excluded.provider_id,auth_type=excluded.auth_type,label=excluded.label,email=excluded.email,secret_ciphertext=CASE WHEN excluded.secret_ciphertext <> '' THEN excluded.secret_ciphertext ELSE credentials.secret_ciphertext END,metadata_json=excluded.metadata_json,priority=excluded.priority,weight=excluded.weight,enabled=excluded.enabled`,
				credentialCfg.ID, providerCfg.ID, credentialCfg.AuthType, credentialCfg.Label, credentialCfg.Email, ciphertext, string(metadata), credentialCfg.Priority, credentialCfg.Weight, boolInt(credentialCfg.IsEnabled()), credentialStatus(credentialCfg.AuthType), now); err != nil {
				return rollback(fmt.Errorf("seed credential %s: %w", credentialCfg.ID, err))
			}
		}
	}
	usedSeedRouteIDs := map[string]struct{}{}
	for _, modelCfg := range cfg.Models {
		aliases, _ := json.Marshal(modelCfg.Aliases)
		caps, _ := json.Marshal(modelCfg.Capabilities)
		limits, _ := json.Marshal(modelCfg.Limits)
		if _, err = tx.ExecContext(ctx, `INSERT INTO public_models(id,display_name,aliases_json,enabled,expose_upstream_name,rewrite_response_model,capabilities_json,limits_json)
VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET display_name=excluded.display_name,aliases_json=excluded.aliases_json,enabled=excluded.enabled,expose_upstream_name=excluded.expose_upstream_name,rewrite_response_model=excluded.rewrite_response_model,capabilities_json=excluded.capabilities_json,limits_json=excluded.limits_json`,
			modelCfg.ID, modelCfg.DisplayName, string(aliases), boolInt(modelCfg.Enabled), boolInt(modelCfg.ExposeUpstreamName), boolInt(modelCfg.RewriteResponseModel), string(caps), string(limits)); err != nil {
			return rollback(fmt.Errorf("seed model %s: %w", modelCfg.ID, err))
		}
		for _, alias := range append([]string{modelCfg.ID}, modelCfg.Aliases...) {
			if _, err = tx.ExecContext(ctx, `INSERT INTO model_aliases(alias,public_model_id,scope_api_key_id,scope_team_id,enabled) VALUES(?,?,?,?,1)
ON CONFLICT(alias,scope_api_key_id,scope_team_id) DO UPDATE SET public_model_id=excluded.public_model_id,enabled=1`, alias, modelCfg.ID, "", ""); err != nil {
				return rollback(fmt.Errorf("seed alias %s: %w", alias, err))
			}
		}
		for _, route := range modelCfg.Routes {
			routeID := allocateRouteTargetID(modelCfg.ID, route, usedSeedRouteIDs)
			conditions, _ := json.Marshal(routeConditions(route))
			if route.Weight <= 0 {
				route.Weight = 1
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO route_targets(id,public_model_id,provider_id,upstream_model,priority,weight,enabled,conditions_json)
VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET public_model_id=excluded.public_model_id,provider_id=excluded.provider_id,upstream_model=excluded.upstream_model,priority=excluded.priority,weight=excluded.weight,enabled=excluded.enabled,conditions_json=excluded.conditions_json`, routeID, modelCfg.ID, route.Provider, route.UpstreamModel, route.Priority, route.Weight, boolInt(route.IsEnabled()), string(conditions)); err != nil {
				return rollback(fmt.Errorf("seed route %s: %w", routeID, err))
			}
		}
	}
	for _, comboCfg := range cfg.Combos {
		capabilities, _ := json.Marshal(comboCfg.Capabilities)
		limits, _ := json.Marshal(comboCfg.Limits)
		policy, _ := json.Marshal(comboCfg.Policy)
		if _, err = tx.ExecContext(ctx, `INSERT INTO combos(id,display_name,enabled,rewrite_response_model,capabilities_json,limits_json,policy_json) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET display_name=excluded.display_name,enabled=excluded.enabled,rewrite_response_model=excluded.rewrite_response_model,capabilities_json=excluded.capabilities_json,limits_json=excluded.limits_json,policy_json=excluded.policy_json`, comboCfg.ID, comboCfg.DisplayName, boolInt(comboCfg.Enabled), boolInt(comboCfg.RewriteResponseModel), string(capabilities), string(limits), string(policy)); err != nil {
			return rollback(fmt.Errorf("seed combo %s: %w", comboCfg.ID, err))
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM combo_items WHERE combo_id=?`, comboCfg.ID); err != nil {
			return rollback(err)
		}
		for position, item := range comboCfg.Items {
			if _, err = tx.ExecContext(ctx, `INSERT INTO combo_items(combo_id,position,public_model_id,route_target_id) VALUES(?,?,?,?)`, comboCfg.ID, position, item.PublicModelID, item.RouteTargetID); err != nil {
				return rollback(fmt.Errorf("seed combo %s item %d: %w", comboCfg.ID, position, err))
			}
		}
	}
	for _, keyCfg := range cfg.ClientAPIKeys {
		key := config.Env(keyCfg.KeyEnv)
		if key == "" {
			continue
		}
		models, _ := json.Marshal(keyCfg.Models)
		policy, _ := json.Marshal(keyCfg.Policy)
		if _, err = tx.ExecContext(ctx, `INSERT INTO api_keys(id,name,key_hash,models_json,policy_json,enabled) VALUES(?,?,?,?,?,1)
ON CONFLICT(id) DO UPDATE SET name=excluded.name,key_hash=excluded.key_hash,models_json=excluded.models_json,policy_json=excluded.policy_json,enabled=1`, keyCfg.ID, keyCfg.Name, security.HashAPIKey(key), string(models), string(policy)); err != nil {
			return rollback(fmt.Errorf("seed api key %s: %w", keyCfg.ID, err))
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit seed: %w", err)
	}
	return nil
}

func (s *Store) SaveProvider(ctx context.Context, providerCfg config.ProviderConfig) error {
	config.ApplyProviderDefaults(&providerCfg)
	proxyPools, err := s.ProxyPoolConfigs(ctx)
	if err != nil {
		return err
	}
	validation := &config.Config{Database: config.DatabaseConfig{Driver: "sqlite"}, Security: config.SecurityConfig{PluginsEnabled: true}, ProxyPools: proxyPools, Providers: []config.ProviderConfig{providerCfg}}
	if err := validation.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	rollback := func(cause error) error { _ = tx.Rollback(); return cause }
	if providerCfg.ID == "" || providerCfg.Type == "" {
		return rollback(errors.New("provider id and type are required"))
	}
	switch providerCfg.Type {
	case "openai-compatible", "anthropic-compatible", "gemini", "vertex", "vertex-partner", "ollama", "codex", "claude", "kimi", "xai", "antigravity", "tavily", "elevenlabs", "image", "video", "plugin-http", "copilot", "qwen", "kiro", "qoder", "cursor", "cline", "clinepass", "iflow", "codebuddy-cn", "kilocode", "gitlab", "kimchi":
	default:
		return rollback(fmt.Errorf("unsupported provider type %q", providerCfg.Type))
	}
	headers, _ := json.Marshal(providerCfg.Headers)
	providerConfig, _ := json.Marshal(map[string]any{"oauth": providerCfg.OAuth, "proxy_pool_ids": providerCfg.ProxyPools, "config": providerCfg.Config, "limits": providerCfg.Limits})
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `INSERT INTO providers(id,type,name,base_url,enabled,status,last_error,last_checked_at,headers_json,config_json,created_at,updated_at) VALUES(?,?,?,?,?,'unknown','','',?,?,?,?) ON CONFLICT(id) DO UPDATE SET type=excluded.type,name=excluded.name,base_url=excluded.base_url,enabled=excluded.enabled,headers_json=excluded.headers_json,config_json=excluded.config_json,updated_at=excluded.updated_at`, providerCfg.ID, providerCfg.Type, providerCfg.Name, providerCfg.BaseURL, boolInt(providerCfg.Enabled), string(headers), string(providerConfig), now, now); err != nil {
		return rollback(err)
	}
	for _, credentialCfg := range providerCfg.Credentials {
		ciphertext := ""
		if credentialCfg.Secret != "" {
			ciphertext, err = s.encryptor.Encrypt(credentialCfg.Secret)
			if err != nil {
				return rollback(err)
			}
		}
		metadata, _ := json.Marshal(redactPersistedMetadata(credentialMetadata(credentialCfg)))
		if credentialCfg.Weight <= 0 {
			credentialCfg.Weight = 1
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO credentials(id,provider_id,auth_type,label,email,secret_ciphertext,metadata_json,priority,weight,enabled,status,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET provider_id=excluded.provider_id,auth_type=excluded.auth_type,label=excluded.label,email=excluded.email,secret_ciphertext=CASE WHEN excluded.secret_ciphertext <> '' THEN excluded.secret_ciphertext ELSE credentials.secret_ciphertext END,metadata_json=excluded.metadata_json,priority=excluded.priority,weight=excluded.weight,enabled=excluded.enabled`, credentialCfg.ID, providerCfg.ID, credentialCfg.AuthType, credentialCfg.Label, credentialCfg.Email, ciphertext, string(metadata), credentialCfg.Priority, credentialCfg.Weight, boolInt(credentialCfg.IsEnabled()), credentialStatus(credentialCfg.AuthType), now); err != nil {
			return rollback(err)
		}
	}
	return tx.Commit()
}

func (s *Store) SavePublicModel(ctx context.Context, modelCfg config.PublicModelConfig) error {
	if _, err := s.ResolveCombo(ctx, modelCfg.ID); err == nil {
		return fmt.Errorf("public model %q conflicts with a combo", modelCfg.ID)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	rollback := func(cause error) error { _ = tx.Rollback(); return cause }
	if modelCfg.ID == "" {
		return rollback(errors.New("model id is required"))
	}
	aliases, _ := json.Marshal(modelCfg.Aliases)
	capabilities, _ := json.Marshal(modelCfg.Capabilities)
	limits, _ := json.Marshal(modelCfg.Limits)
	if _, err = tx.ExecContext(ctx, `INSERT INTO public_models(id,display_name,aliases_json,enabled,expose_upstream_name,rewrite_response_model,capabilities_json,limits_json) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET display_name=excluded.display_name,aliases_json=excluded.aliases_json,enabled=excluded.enabled,expose_upstream_name=excluded.expose_upstream_name,rewrite_response_model=excluded.rewrite_response_model,capabilities_json=excluded.capabilities_json,limits_json=excluded.limits_json`, modelCfg.ID, modelCfg.DisplayName, string(aliases), boolInt(modelCfg.Enabled), boolInt(modelCfg.ExposeUpstreamName), boolInt(modelCfg.RewriteResponseModel), string(capabilities), string(limits)); err != nil {
		return rollback(err)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM model_aliases WHERE public_model_id=? AND scope_api_key_id='' AND scope_team_id=''`, modelCfg.ID); err != nil {
		return rollback(err)
	}
	for _, alias := range append([]string{modelCfg.ID}, modelCfg.Aliases...) {
		var owner string
		errOwner := tx.QueryRowContext(ctx, `SELECT public_model_id FROM model_aliases WHERE alias=? AND scope_api_key_id='' AND scope_team_id=''`, alias).Scan(&owner)
		if errOwner == nil && owner != modelCfg.ID {
			return rollback(fmt.Errorf("alias %q already belongs to model %q", alias, owner))
		}
		if errOwner != nil && !errors.Is(errOwner, sql.ErrNoRows) {
			return rollback(errOwner)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO model_aliases(alias,public_model_id,scope_api_key_id,scope_team_id,enabled) VALUES(?,?,?,?,1) ON CONFLICT(alias,scope_api_key_id,scope_team_id) DO UPDATE SET public_model_id=excluded.public_model_id,enabled=1`, alias, modelCfg.ID, "", ""); err != nil {
			return rollback(err)
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM route_targets WHERE public_model_id=?`, modelCfg.ID); err != nil {
		return rollback(err)
	}
	usedRouteIDs := map[string]struct{}{}
	for _, route := range modelCfg.Routes {
		routeID := allocateRouteTargetID(modelCfg.ID, route, usedRouteIDs)
		if route.Weight <= 0 {
			route.Weight = 1
		}
		conditions, _ := json.Marshal(routeConditions(route))
		if _, err = tx.ExecContext(ctx, `INSERT INTO route_targets(id,public_model_id,provider_id,upstream_model,priority,weight,enabled,conditions_json) VALUES(?,?,?,?,?,?,?,?)`, routeID, modelCfg.ID, route.Provider, route.UpstreamModel, route.Priority, route.Weight, boolInt(route.IsEnabled()), string(conditions)); err != nil {
			return rollback(err)
		}
	}
	return tx.Commit()
}

func (s *Store) SaveCombo(ctx context.Context, comboCfg config.ComboConfig) error {
	if strings.TrimSpace(comboCfg.ID) == "" || len(comboCfg.Items) == 0 {
		return errors.New("combo id and at least one item are required")
	}
	if _, err := s.ResolveModel(ctx, comboCfg.ID, ""); err == nil {
		return fmt.Errorf("combo %q conflicts with a public model or alias", comboCfg.ID)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for _, item := range comboCfg.Items {
		if _, err := s.ResolveModel(ctx, item.PublicModelID, ""); err != nil {
			return fmt.Errorf("combo item public model %q not found", item.PublicModelID)
		}
		if item.RouteTargetID != "" {
			routes, err := s.Routes(ctx, item.PublicModelID)
			if err != nil {
				return err
			}
			found := false
			for _, route := range routes {
				if route.ID == item.RouteTargetID {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("route target %q does not belong to model %q", item.RouteTargetID, item.PublicModelID)
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	rollback := func(cause error) error { _ = tx.Rollback(); return cause }
	capabilities, _ := json.Marshal(comboCfg.Capabilities)
	limits, _ := json.Marshal(comboCfg.Limits)
	policy, _ := json.Marshal(comboCfg.Policy)
	if _, err = tx.ExecContext(ctx, `INSERT INTO combos(id,display_name,enabled,rewrite_response_model,capabilities_json,limits_json,policy_json) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET display_name=excluded.display_name,enabled=excluded.enabled,rewrite_response_model=excluded.rewrite_response_model,capabilities_json=excluded.capabilities_json,limits_json=excluded.limits_json,policy_json=excluded.policy_json`, comboCfg.ID, comboCfg.DisplayName, boolInt(comboCfg.Enabled), boolInt(comboCfg.RewriteResponseModel), string(capabilities), string(limits), string(policy)); err != nil {
		return rollback(err)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM combo_items WHERE combo_id=?`, comboCfg.ID); err != nil {
		return rollback(err)
	}
	for position, item := range comboCfg.Items {
		if _, err = tx.ExecContext(ctx, `INSERT INTO combo_items(combo_id,position,public_model_id,route_target_id) VALUES(?,?,?,?)`, comboCfg.ID, position, item.PublicModelID, item.RouteTargetID); err != nil {
			return rollback(err)
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteCombo(ctx context.Context, comboID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM combos WHERE id=?`, comboID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) Combos(ctx context.Context) ([]Combo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,display_name,enabled,rewrite_response_model,capabilities_json,limits_json,policy_json FROM combos ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var items []Combo
	for rows.Next() {
		var item Combo
		var enabled, rewrite int
		var capabilities, limits, policy string
		if err := rows.Scan(&item.ID, &item.DisplayName, &enabled, &rewrite, &capabilities, &limits, &policy); err != nil {
			return nil, err
		}
		item.Enabled = enabled != 0
		item.RewriteResponseModel = rewrite != 0
		_ = json.Unmarshal([]byte(capabilities), &item.Capabilities)
		_ = json.Unmarshal([]byte(limits), &item.Limits)
		_ = json.Unmarshal([]byte(policy), &item.Policy)
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	for index := range items {
		items[index].Items, err = s.ComboItems(ctx, items[index].ID)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (s *Store) ComboItems(ctx context.Context, comboID string) ([]ComboItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT position,public_model_id,route_target_id FROM combo_items WHERE combo_id=? ORDER BY position`, comboID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ComboItem
	for rows.Next() {
		var item ComboItem
		if err := rows.Scan(&item.Position, &item.PublicModelID, &item.RouteTargetID); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if items == nil {
		return []ComboItem{}, nil
	}
	return items, nil
}

func (s *Store) ResolveCombo(ctx context.Context, comboID string) (*PublicModel, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,display_name,enabled,rewrite_response_model,capabilities_json,limits_json,policy_json FROM combos WHERE id=? AND enabled=1`, comboID)
	var model PublicModel
	var enabled, rewrite int
	var capabilities, limits, policy string
	if err := row.Scan(&model.ID, &model.DisplayName, &enabled, &rewrite, &capabilities, &limits, &policy); err != nil {
		return nil, err
	}
	model.Enabled = enabled != 0
	model.RewriteResponseModel = rewrite != 0
	_ = json.Unmarshal([]byte(capabilities), &model.Capabilities)
	_ = json.Unmarshal([]byte(limits), &model.Limits)
	_ = json.Unmarshal([]byte(policy), &model.Policy)
	var err error
	model.ComboItems, err = s.ComboItems(ctx, comboID)
	return &model, err
}

func (s *Store) SaveCredential(ctx context.Context, providerID string, credentialCfg config.CredentialConfig) error {
	if providerID == "" || credentialCfg.ID == "" {
		return errors.New("provider id and credential id are required")
	}
	if _, err := s.Provider(ctx, providerID); err != nil {
		return fmt.Errorf("provider %q not found", providerID)
	}
	switch credentialCfg.AuthType {
	case "api_key", "oauth", "service_account", "none":
	default:
		return fmt.Errorf("unsupported auth type %q", credentialCfg.AuthType)
	}
	metadata := credentialMetadata(credentialCfg)
	if existing, err := s.CredentialByID(ctx, credentialCfg.ID); err == nil {
		if len(credentialCfg.Metadata) == 0 {
			for key, value := range existing.Metadata {
				if _, has := metadata[key]; !has {
					metadata[key] = value
				}
			}
		}
		if credentialCfg.Enabled != nil && *credentialCfg.Enabled {
			metadata = quotaAutoDisabledMetadata(metadata, false)
		}
	}
	ciphertext := ""
	secret := credentialCfg.Secret
	if secret == "" {
		secret = config.Env(credentialCfg.SecretEnv)
	}
	var err error
	if secret != "" {
		ciphertext, err = s.encryptor.Encrypt(secret)
		if err != nil {
			return err
		}
	}
	metadataBytes, _ := json.Marshal(redactPersistedMetadata(metadata))
	if credentialCfg.Weight <= 0 {
		credentialCfg.Weight = 1
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO credentials(id,provider_id,auth_type,status,label,email,secret_ciphertext,metadata_json,priority,weight,enabled,created_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET provider_id=excluded.provider_id,auth_type=excluded.auth_type,label=excluded.label,email=excluded.email,secret_ciphertext=CASE WHEN excluded.secret_ciphertext<>'' THEN excluded.secret_ciphertext ELSE credentials.secret_ciphertext END,metadata_json=excluded.metadata_json,priority=excluded.priority,weight=excluded.weight,enabled=excluded.enabled,status=CASE WHEN credentials.status='auth_required' AND excluded.auth_type='oauth' THEN credentials.status ELSE excluded.status END`,
		credentialCfg.ID, providerID, credentialCfg.AuthType, credentialStatus(credentialCfg.AuthType), credentialCfg.Label, credentialCfg.Email, ciphertext, string(metadataBytes), credentialCfg.Priority, credentialCfg.Weight, boolInt(credentialCfg.IsEnabled()), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) DeleteProvider(ctx context.Context, providerID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM providers WHERE id=?`, providerID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteCredential(ctx context.Context, credentialID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM credentials WHERE id=?`, credentialID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeletePublicModel(ctx context.Context, modelID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	rollback := func(err error) error { _ = tx.Rollback(); return err }
	if _, err = tx.ExecContext(ctx, `DELETE FROM combo_items WHERE public_model_id=?`, modelID); err != nil {
		return rollback(err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM public_models WHERE id=?`, modelID)
	if err != nil {
		return rollback(err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return rollback(sql.ErrNoRows)
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *Store) SaveProxyPool(ctx context.Context, poolCfg config.ProxyPoolConfig) error {
	validation := &config.Config{Database: config.DatabaseConfig{Driver: "sqlite"}, ProxyPools: []config.ProxyPoolConfig{poolCfg}}
	if err := validation.Validate(); err != nil {
		return err
	}
	poolURL := poolCfg.URL
	if poolURL == "" && poolCfg.URLEnv != "" {
		poolURL = config.Env(poolCfg.URLEnv)
	}
	ciphertext, err := s.encryptor.Encrypt(poolURL)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `INSERT INTO proxy_pools(id,name,url_ciphertext,enabled,status,created_at,updated_at)
VALUES(?,?,?,?,'unknown',?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,url_ciphertext=excluded.url_ciphertext,enabled=excluded.enabled,status='unknown',last_error='',updated_at=excluded.updated_at`,
		poolCfg.ID, poolCfg.Name, ciphertext, boolInt(poolCfg.IsEnabled()), now, now)
	return err
}

func (s *Store) ProxyPool(ctx context.Context, id string) (*ProxyPool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,name,url_ciphertext,enabled,status,last_error,last_tested_at,created_at,updated_at FROM proxy_pools WHERE id=?`, id)
	var pool ProxyPool
	var ciphertext, lastTested, created, updated string
	var enabled int
	if err := row.Scan(&pool.ID, &pool.Name, &ciphertext, &enabled, &pool.Status, &pool.LastError, &lastTested, &created, &updated); err != nil {
		return nil, err
	}
	pool.Enabled = enabled != 0
	var err error
	pool.URL, err = s.encryptor.Decrypt(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypt proxy pool %s: %w", pool.ID, err)
	}
	pool.LastTestedAt, _ = time.Parse(time.RFC3339Nano, lastTested)
	pool.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	pool.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return &pool, nil
}

func (s *Store) ProxyPools(ctx context.Context) ([]ProxyPool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,url_ciphertext,enabled,status,last_error,last_tested_at,created_at,updated_at FROM proxy_pools ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pools []ProxyPool
	for rows.Next() {
		var pool ProxyPool
		var ciphertext, lastTested, created, updated string
		var enabled int
		if err = rows.Scan(&pool.ID, &pool.Name, &ciphertext, &enabled, &pool.Status, &pool.LastError, &lastTested, &created, &updated); err != nil {
			return nil, err
		}
		pool.Enabled = enabled != 0
		pool.URL, err = s.encryptor.Decrypt(ciphertext)
		if err != nil {
			return nil, fmt.Errorf("decrypt proxy pool %s: %w", pool.ID, err)
		}
		pool.LastTestedAt, _ = time.Parse(time.RFC3339Nano, lastTested)
		pool.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		pool.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		pools = append(pools, pool)
	}
	return pools, rows.Err()
}

func (s *Store) ProxyPoolConfigs(ctx context.Context) ([]config.ProxyPoolConfig, error) {
	pools, err := s.ProxyPools(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]config.ProxyPoolConfig, 0, len(pools))
	for _, pool := range pools {
		enabled := pool.Enabled
		result = append(result, config.ProxyPoolConfig{ID: pool.ID, Name: pool.Name, URL: pool.URL, Enabled: &enabled})
	}
	return result, nil
}

func (s *Store) SetProxyPoolHealth(ctx context.Context, id, status, message string, testedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE proxy_pools SET status=?,last_error=?,last_tested_at=?,updated_at=? WHERE id=?`, status, security.RedactText(message), testedAt.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteProxyPool(ctx context.Context, id string) error {
	usage, err := s.ProxyPoolUsageCount(ctx, id)
	if err != nil {
		return err
	}
	if usage > 0 {
		return fmt.Errorf("proxy pool %q is bound to %d provider or credential records", id, usage)
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM proxy_pools WHERE id=?`, id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) ProxyPoolUsageCount(ctx context.Context, id string) (int, error) {
	providers, err := s.Providers(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, provider := range providers {
		for _, poolID := range provider.ProxyPoolIDs {
			if poolID == id {
				count++
			}
		}
		credentials, credentialErr := s.Credentials(ctx, provider.ID)
		if credentialErr != nil {
			return 0, credentialErr
		}
		for _, credential := range credentials {
			for _, poolID := range credential.ProxyPoolIDs {
				if poolID == id {
					count++
				}
			}
		}
	}
	return count, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Store) AuthenticateAPIKey(ctx context.Context, key string) (*APIKey, error) {
	if key == "" {
		return nil, errors.New("missing api key")
	}
	row := s.db.QueryRowContext(ctx, `SELECT id,name,key_hash,models_json,policy_json,enabled,last_used_at FROM api_keys WHERE key_hash=?`, security.HashAPIKey(key))
	var result APIKey
	var models, policy, lastUsed string
	var enabled int
	if err := row.Scan(&result.ID, &result.Name, &result.Hash, &models, &policy, &enabled, &lastUsed); err != nil {
		return nil, err
	}
	if enabled == 0 {
		return nil, errors.New("api key disabled")
	}
	_ = json.Unmarshal([]byte(models), &result.Models)
	_ = json.Unmarshal([]byte(policy), &result.Policy)
	if lastUsed != "" {
		result.LastUsed, _ = time.Parse(time.RFC3339Nano, lastUsed)
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), result.ID)
	return &result, nil
}

type APIKeySummary struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name"`
	Models   []string               `json:"models"`
	Policy   config.ClientKeyPolicy `json:"policy,omitempty"`
	Enabled  bool                   `json:"enabled"`
	LastUsed time.Time              `json:"last_used_at,omitempty"`
}

func (s *Store) ImportClientAPIKey(ctx context.Context, id, name, plaintext string, models []string, enabled bool) error {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(plaintext) == "" {
		return errors.New("api key id and plaintext are required")
	}
	if len(models) == 0 {
		models = []string{"*"}
	}
	encodedModels, _ := json.Marshal(models)
	encodedPolicy, _ := json.Marshal(config.ClientKeyPolicy{})
	_, err := s.db.ExecContext(ctx, `INSERT INTO api_keys(id,name,key_hash,models_json,policy_json,enabled) VALUES(?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name,key_hash=excluded.key_hash,models_json=excluded.models_json,policy_json=excluded.policy_json,enabled=excluded.enabled`,
		id, name, security.HashAPIKey(plaintext), string(encodedModels), string(encodedPolicy), boolInt(enabled))
	return err
}

func (s *Store) SetCredentialEnabled(ctx context.Context, credentialID string, enabled bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE credentials SET enabled=? WHERE id=?`, boolInt(enabled), credentialID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) CreateAPIKey(ctx context.Context, id, name string, models []string, policies ...config.ClientKeyPolicy) (string, string, error) {
	if strings.TrimSpace(id) == "" {
		id = security.NewID("key_")
	}
	plaintext := security.NewID("tp_")
	encodedModels, _ := json.Marshal(models)
	var policy config.ClientKeyPolicy
	if len(policies) > 0 {
		policy = policies[0]
	}
	encodedPolicy, _ := json.Marshal(policy)
	_, err := s.db.ExecContext(ctx, `INSERT INTO api_keys(id,name,key_hash,models_json,policy_json,enabled) VALUES(?,?,?,?,?,1)`, id, name, security.HashAPIKey(plaintext), string(encodedModels), string(encodedPolicy))
	if err != nil {
		return "", "", err
	}
	return id, plaintext, nil
}

func (s *Store) UpdateAPIKey(ctx context.Context, id, name string, models []string, enabled bool, policies ...config.ClientKeyPolicy) error {
	encodedModels, _ := json.Marshal(models)
	if len(policies) > 0 {
		encodedPolicy, _ := json.Marshal(policies[0])
		result, err := s.db.ExecContext(ctx, `UPDATE api_keys SET name=?,models_json=?,policy_json=?,enabled=? WHERE id=?`, name, string(encodedModels), string(encodedPolicy), boolInt(enabled), id)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return sql.ErrNoRows
		}
		return nil
	}
	result, err := s.db.ExecContext(ctx, `UPDATE api_keys SET name=?,models_json=?,enabled=? WHERE id=?`, name, string(encodedModels), boolInt(enabled), id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteAPIKey(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM api_keys WHERE id=?`, id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) APIKeys(ctx context.Context) ([]APIKeySummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,models_json,policy_json,enabled,last_used_at FROM api_keys ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []APIKeySummary
	for rows.Next() {
		var item APIKeySummary
		var models, policy, lastUsed string
		var enabled int
		if err := rows.Scan(&item.ID, &item.Name, &models, &policy, &enabled, &lastUsed); err != nil {
			return nil, err
		}
		item.Enabled = enabled != 0
		_ = json.Unmarshal([]byte(models), &item.Models)
		_ = json.Unmarshal([]byte(policy), &item.Policy)
		if lastUsed != "" {
			item.LastUsed, _ = time.Parse(time.RFC3339Nano, lastUsed)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// MatchAPIKeySecrets maps enabled API key IDs to plaintext values when a candidate matches the stored hash.
func (s *Store) MatchAPIKeySecrets(ctx context.Context, candidates []string) (map[string]string, error) {
	result := map[string]string{}
	if len(candidates) == 0 {
		return result, nil
	}
	byHash := make(map[string]string, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		byHash[security.HashAPIKey(candidate)] = candidate
	}
	if len(byHash) == 0 {
		return result, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, key_hash FROM api_keys WHERE enabled=1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, hash string
		if err := rows.Scan(&id, &hash); err != nil {
			return nil, err
		}
		if plaintext, ok := byHash[hash]; ok {
			result[id] = plaintext
		}
	}
	return result, rows.Err()
}

func (s *Store) PublicModels(ctx context.Context) ([]PublicModel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,display_name,aliases_json,enabled,expose_upstream_name,rewrite_response_model,capabilities_json,limits_json FROM public_models ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []PublicModel
	for rows.Next() {
		var item PublicModel
		var aliases, caps, limits string
		var enabled, expose, rewrite int
		if err := rows.Scan(&item.ID, &item.DisplayName, &aliases, &enabled, &expose, &rewrite, &caps, &limits); err != nil {
			return nil, err
		}
		item.Enabled, item.ExposeUpstreamName, item.RewriteResponseModel = enabled != 0, expose != 0, rewrite != 0
		_ = json.Unmarshal([]byte(aliases), &item.Aliases)
		_ = json.Unmarshal([]byte(caps), &item.Capabilities)
		_ = json.Unmarshal([]byte(limits), &item.Limits)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) PublicModel(ctx context.Context, modelID string) (*PublicModel, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,display_name,aliases_json,enabled,expose_upstream_name,rewrite_response_model,capabilities_json,limits_json FROM public_models WHERE id=?`, modelID)
	var item PublicModel
	var aliases, caps, limits string
	var enabled, expose, rewrite int
	if err := row.Scan(&item.ID, &item.DisplayName, &aliases, &enabled, &expose, &rewrite, &caps, &limits); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return s.ResolveCombo(ctx, modelID)
		}
		return nil, err
	}
	item.Enabled, item.ExposeUpstreamName, item.RewriteResponseModel = enabled != 0, expose != 0, rewrite != 0
	_ = json.Unmarshal([]byte(aliases), &item.Aliases)
	_ = json.Unmarshal([]byte(caps), &item.Capabilities)
	_ = json.Unmarshal([]byte(limits), &item.Limits)
	return &item, nil
}

func (s *Store) CatalogModels(ctx context.Context) ([]PublicModel, error) {
	models, err := s.PublicModels(ctx)
	if err != nil {
		return nil, err
	}
	combos, err := s.Combos(ctx)
	if err != nil {
		return nil, err
	}
	for _, combo := range combos {
		models = append(models, PublicModel{ID: combo.ID, DisplayName: combo.DisplayName, Enabled: combo.Enabled, RewriteResponseModel: combo.RewriteResponseModel, Capabilities: combo.Capabilities, Limits: combo.Limits, Policy: combo.Policy, ComboItems: combo.Items})
	}
	sort.SliceStable(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

func (s *Store) SaveModelAlias(ctx context.Context, alias ModelAlias) error {
	alias.Alias = strings.TrimSpace(alias.Alias)
	alias.PublicModelID = strings.TrimSpace(alias.PublicModelID)
	alias.APIKeyID = strings.TrimSpace(alias.APIKeyID)
	alias.TeamID = strings.TrimSpace(alias.TeamID)
	if alias.Alias == "" || alias.PublicModelID == "" {
		return errors.New("alias and public model id are required")
	}
	if _, err := s.ResolveCombo(ctx, alias.Alias); err == nil {
		return fmt.Errorf("alias %q conflicts with a combo", alias.Alias)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := s.ResolveModel(ctx, alias.PublicModelID, ""); err != nil {
		return fmt.Errorf("public model %q not found", alias.PublicModelID)
	}
	if alias.APIKeyID != "" {
		var count int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_keys WHERE id=?`, alias.APIKeyID).Scan(&count); err != nil || count == 0 {
			return fmt.Errorf("API key %q not found", alias.APIKeyID)
		}
	}
	if alias.APIKeyID != "" && alias.TeamID != "" {
		return errors.New("an alias cannot be scoped to both an API key and a team")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO model_aliases(alias,public_model_id,scope_api_key_id,scope_team_id,enabled) VALUES(?,?,?,?,?) ON CONFLICT(alias,scope_api_key_id,scope_team_id) DO UPDATE SET public_model_id=excluded.public_model_id,enabled=excluded.enabled`, alias.Alias, alias.PublicModelID, alias.APIKeyID, alias.TeamID, boolInt(alias.Enabled))
	return err
}

func (s *Store) DeleteModelAlias(ctx context.Context, name, apiKeyID string) error {
	return s.DeleteScopedModelAlias(ctx, name, apiKeyID, "")
}

func (s *Store) DeleteScopedModelAlias(ctx context.Context, name, apiKeyID, teamID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM model_aliases WHERE alias=? AND scope_api_key_id=? AND scope_team_id=?`, strings.TrimSpace(name), strings.TrimSpace(apiKeyID), strings.TrimSpace(teamID))
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) ModelAliases(ctx context.Context) ([]ModelAlias, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT alias,public_model_id,scope_api_key_id,scope_team_id,enabled FROM model_aliases ORDER BY alias,scope_api_key_id,scope_team_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ModelAlias
	for rows.Next() {
		var item ModelAlias
		var enabled int
		if err := rows.Scan(&item.Alias, &item.PublicModelID, &item.APIKeyID, &item.TeamID, &enabled); err != nil {
			return nil, err
		}
		item.Enabled = enabled != 0
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ResolveModel(ctx context.Context, requested, apiKeyID string) (*PublicModel, error) {
	return s.ResolveModelScoped(ctx, requested, apiKeyID, "")
}

func (s *Store) ResolveModelScoped(ctx context.Context, requested, apiKeyID, teamID string) (*PublicModel, error) {
	row := s.db.QueryRowContext(ctx, `SELECT pm.id,pm.display_name,pm.aliases_json,pm.enabled,pm.expose_upstream_name,pm.rewrite_response_model,pm.capabilities_json,pm.limits_json
FROM public_models pm LEFT JOIN model_aliases ma ON ma.public_model_id=pm.id AND ma.alias=? AND ma.enabled=1
WHERE pm.enabled=1 AND (pm.id=? OR (ma.alias=? AND ((ma.scope_api_key_id='' AND ma.scope_team_id='') OR (?<>'' AND ma.scope_api_key_id=?) OR (?<>'' AND ma.scope_team_id=?))))
ORDER BY CASE WHEN pm.id=? THEN 0 WHEN ?<>'' AND ma.scope_api_key_id=? THEN 1 WHEN ?<>'' AND ma.scope_team_id=? THEN 2 ELSE 3 END LIMIT 1`, requested, requested, requested, apiKeyID, apiKeyID, teamID, teamID, requested, apiKeyID, apiKeyID, teamID, teamID)
	var item PublicModel
	var aliases, caps, limits string
	var enabled, expose, rewrite int
	if err := row.Scan(&item.ID, &item.DisplayName, &aliases, &enabled, &expose, &rewrite, &caps, &limits); err != nil {
		return nil, err
	}
	item.Enabled, item.ExposeUpstreamName, item.RewriteResponseModel = enabled != 0, expose != 0, rewrite != 0
	_ = json.Unmarshal([]byte(aliases), &item.Aliases)
	_ = json.Unmarshal([]byte(caps), &item.Capabilities)
	_ = json.Unmarshal([]byte(limits), &item.Limits)
	return &item, nil
}

func (s *Store) ResolveProviderModel(ctx context.Context, providerPrefix, requested, apiKeyID string) (*PublicModel, error) {
	return s.ResolveProviderModelScoped(ctx, providerPrefix, requested, apiKeyID, "")
}

func (s *Store) ResolveProviderModelScoped(ctx context.Context, providerPrefix, requested, apiKeyID, teamID string) (*PublicModel, error) {
	row := s.db.QueryRowContext(ctx, `SELECT pm.id,pm.display_name,pm.aliases_json,pm.enabled,pm.expose_upstream_name,pm.rewrite_response_model,pm.capabilities_json,pm.limits_json
FROM model_aliases ma JOIN public_models pm ON pm.id=ma.public_model_id
JOIN route_targets rt ON rt.public_model_id=pm.id AND rt.enabled=1
JOIN providers p ON p.id=rt.provider_id AND p.enabled=1
WHERE ma.alias=? AND ma.enabled=1 AND ((ma.scope_api_key_id='' AND ma.scope_team_id='') OR (?<>'' AND ma.scope_api_key_id=?) OR (?<>'' AND ma.scope_team_id=?))
AND (p.id=? OR p.name=? OR p.type=?)
ORDER BY CASE WHEN ?<>'' AND ma.scope_api_key_id=? THEN 0 WHEN ?<>'' AND ma.scope_team_id=? THEN 1 ELSE 2 END, rt.priority DESC LIMIT 1`, requested, apiKeyID, apiKeyID, teamID, teamID, providerPrefix, providerPrefix, providerPrefix, apiKeyID, apiKeyID, teamID, teamID)
	var item PublicModel
	var aliases, caps, limits string
	var enabled, expose, rewrite int
	if err := row.Scan(&item.ID, &item.DisplayName, &aliases, &enabled, &expose, &rewrite, &caps, &limits); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return s.resolveProviderUpstreamModelScoped(ctx, providerPrefix, requested)
		}
		return nil, err
	}
	item.Enabled, item.ExposeUpstreamName, item.RewriteResponseModel = enabled != 0, expose != 0, rewrite != 0
	_ = json.Unmarshal([]byte(aliases), &item.Aliases)
	_ = json.Unmarshal([]byte(caps), &item.Capabilities)
	_ = json.Unmarshal([]byte(limits), &item.Limits)
	return &item, nil
}

func (s *Store) resolveProviderUpstreamModelScoped(ctx context.Context, providerPrefix, upstreamModel string) (*PublicModel, error) {
	row := s.db.QueryRowContext(ctx, `SELECT pm.id,pm.display_name,pm.aliases_json,pm.enabled,pm.expose_upstream_name,pm.rewrite_response_model,pm.capabilities_json,pm.limits_json
FROM route_targets rt JOIN public_models pm ON pm.id=rt.public_model_id AND pm.enabled=1
JOIN providers p ON p.id=rt.provider_id AND p.enabled=1
WHERE rt.upstream_model=? AND rt.enabled=1 AND (p.id=? OR p.name=? OR p.type=?)
ORDER BY rt.priority DESC LIMIT 1`, upstreamModel, providerPrefix, providerPrefix, providerPrefix)
	var item PublicModel
	var aliases, caps, limits string
	var enabled, expose, rewrite int
	if err := row.Scan(&item.ID, &item.DisplayName, &aliases, &enabled, &expose, &rewrite, &caps, &limits); err != nil {
		return nil, err
	}
	item.Enabled, item.ExposeUpstreamName, item.RewriteResponseModel = enabled != 0, expose != 0, rewrite != 0
	_ = json.Unmarshal([]byte(aliases), &item.Aliases)
	_ = json.Unmarshal([]byte(caps), &item.Capabilities)
	_ = json.Unmarshal([]byte(limits), &item.Limits)
	return &item, nil
}

func (s *Store) ResolveUpstreamModel(ctx context.Context, requested string) (*PublicModel, error) {
	row := s.db.QueryRowContext(ctx, `SELECT pm.id,pm.display_name,pm.aliases_json,pm.enabled,pm.expose_upstream_name,pm.rewrite_response_model,pm.capabilities_json,pm.limits_json FROM route_targets rt JOIN public_models pm ON pm.id=rt.public_model_id WHERE rt.upstream_model=? AND rt.enabled=1 AND pm.enabled=1 ORDER BY rt.priority DESC LIMIT 1`, requested)
	var item PublicModel
	var aliases, caps, limits string
	var enabled, expose, rewrite int
	if err := row.Scan(&item.ID, &item.DisplayName, &aliases, &enabled, &expose, &rewrite, &caps, &limits); err != nil {
		return nil, err
	}
	item.Enabled, item.ExposeUpstreamName, item.RewriteResponseModel = enabled != 0, expose != 0, rewrite != 0
	_ = json.Unmarshal([]byte(aliases), &item.Aliases)
	_ = json.Unmarshal([]byte(caps), &item.Capabilities)
	_ = json.Unmarshal([]byte(limits), &item.Limits)
	return &item, nil
}

func (s *Store) Routes(ctx context.Context, publicModelID string) ([]RouteTarget, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,public_model_id,provider_id,upstream_model,priority,weight,enabled,conditions_json FROM route_targets WHERE public_model_id=? AND enabled=1 ORDER BY priority DESC,id`, publicModelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []RouteTarget
	for rows.Next() {
		var item RouteTarget
		var enabled int
		var conditions string
		if err := rows.Scan(&item.ID, &item.PublicModelID, &item.ProviderID, &item.UpstreamModel, &item.Priority, &item.Weight, &enabled, &conditions); err != nil {
			return nil, err
		}
		item.Enabled = enabled != 0
		_ = json.Unmarshal([]byte(conditions), &item.Conditions)
		if rawPricing, exists := item.Conditions["_tproxy_pricing"]; exists {
			encoded, _ := json.Marshal(rawPricing)
			var pricing config.PricingConfig
			if json.Unmarshal(encoded, &pricing) == nil {
				item.Pricing = &pricing
			}
			delete(item.Conditions, "_tproxy_pricing")
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func routeConditions(route config.RouteTargetConfig) map[string]any {
	conditions := make(map[string]any, len(route.Conditions)+1)
	for key, value := range route.Conditions {
		conditions[key] = value
	}
	if route.Pricing != nil {
		conditions["_tproxy_pricing"] = route.Pricing
	}
	return conditions
}

// allocateRouteTargetID picks a unique route id within one save/seed batch.
// Empty ids default to "{model}-{provider}-{upstream}"; collisions get -2, -3, …
func allocateRouteTargetID(modelID string, route config.RouteTargetConfig, used map[string]struct{}) string {
	base := strings.TrimSpace(route.ID)
	if base == "" {
		base = modelID + "-" + route.Provider + "-" + route.UpstreamModel
	}
	if base == "" {
		base = modelID + "-route"
	}
	id := base
	for n := 2; ; n++ {
		if _, exists := used[id]; !exists {
			used[id] = struct{}{}
			return id
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
}

func (s *Store) Provider(ctx context.Context, id string) (*Provider, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,type,name,base_url,enabled,status,last_error,last_checked_at,headers_json,config_json FROM providers WHERE id=?`, id)
	var item Provider
	var enabled int
	var headers, providerConfig, checked string
	if err := row.Scan(&item.ID, &item.Type, &item.Name, &item.BaseURL, &enabled, &item.Status, &item.LastError, &checked, &headers, &providerConfig); err != nil {
		return nil, err
	}
	item.Enabled = enabled != 0
	item.LastChecked, _ = time.Parse(time.RFC3339Nano, checked)
	_ = json.Unmarshal([]byte(headers), &item.Headers)
	decodeProviderConfig(providerConfig, &item)
	return &item, nil
}

func (s *Store) ProviderByPrefix(ctx context.Context, prefix string) (*Provider, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,type,name,base_url,enabled,status,last_error,last_checked_at,headers_json,config_json FROM providers WHERE id=? OR name=? OR type=? ORDER BY CASE WHEN id=? THEN 0 WHEN name=? THEN 1 ELSE 2 END LIMIT 1`, prefix, prefix, prefix, prefix, prefix)
	var item Provider
	var enabled int
	var headers, providerConfig, checked string
	if err := row.Scan(&item.ID, &item.Type, &item.Name, &item.BaseURL, &enabled, &item.Status, &item.LastError, &checked, &headers, &providerConfig); err != nil {
		return nil, err
	}
	item.Enabled = enabled != 0
	item.LastChecked, _ = time.Parse(time.RFC3339Nano, checked)
	_ = json.Unmarshal([]byte(headers), &item.Headers)
	decodeProviderConfig(providerConfig, &item)
	return &item, nil
}

const credentialSelectColumns = `id,provider_id,auth_type,status,label,email,secret_ciphertext,metadata_json,priority,weight,enabled,cooldown_until,last_error_code,last_error,last_validated_at,last_used_at,consecutive_use_count,created_at`

func (s *Store) Credentials(ctx context.Context, providerID string) ([]Credential, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+credentialSelectColumns+` FROM credentials WHERE provider_id=? ORDER BY priority DESC,id`, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Credential
	for rows.Next() {
		item, err := s.scanCredentialRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) CredentialByID(ctx context.Context, credentialID string) (Credential, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+credentialSelectColumns+` FROM credentials WHERE id=?`, credentialID)
	return s.scanCredentialRow(row.Scan)
}

func (s *Store) scanCredentialRow(scan func(dest ...any) error) (Credential, error) {
	var item Credential
	var secret, metadata, cooldown, validated, lastUsed, created string
	var enabled, consecutiveUseCount int
	if err := scan(&item.ID, &item.ProviderID, &item.AuthType, &item.Status, &item.Label, &item.Email, &secret, &metadata, &item.Priority, &item.Weight, &enabled, &cooldown, &item.LastErrorCode, &item.LastError, &validated, &lastUsed, &consecutiveUseCount, &created); err != nil {
		return Credential{}, err
	}
	item.Enabled = enabled != 0
	item.ConsecutiveUseCount = consecutiveUseCount
	_ = json.Unmarshal([]byte(metadata), &item.Metadata)
	decodeCredentialMetadata(&item)
	if secret != "" {
		plaintext, decryptErr := s.encryptor.Decrypt(secret)
		if decryptErr != nil {
			return Credential{}, fmt.Errorf("decrypt credential %s: %w", item.ID, decryptErr)
		}
		if item.AuthType == "oauth" {
			var token OAuthToken
			if json.Unmarshal([]byte(plaintext), &token) == nil && token.AccessToken != "" {
				item.OAuthToken = &token
				item.Secret = token.AccessToken
				item.TokenType = token.TokenType
			} else {
				item.Secret = plaintext
				item.OAuthToken = &OAuthToken{AccessToken: plaintext, TokenType: "Bearer"}
			}
		} else {
			item.Secret = plaintext
		}
	}
	if cooldown != "" {
		item.CooldownUntil, _ = time.Parse(time.RFC3339Nano, cooldown)
	}
	if validated != "" {
		item.LastValidated, _ = time.Parse(time.RFC3339Nano, validated)
	}
	if lastUsed != "" {
		item.LastUsedAt, _ = time.Parse(time.RFC3339Nano, lastUsed)
	}
	if created != "" {
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	}
	return item, nil
}

func (s *Store) SetCooldown(ctx context.Context, credentialID, code, message string, until time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE credentials SET cooldown_until=?,status=CASE WHEN status='auth_required' THEN status ELSE 'cooldown' END,last_error_code=?,last_error=? WHERE id=?`, until.UTC().Format(time.RFC3339Nano), security.RedactText(code), security.RedactText(message), credentialID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) SetProviderHealth(ctx context.Context, providerID, status, message string, checkedAt time.Time) error {
	if status == "" {
		status = "unknown"
	}
	result, err := s.db.ExecContext(ctx, `UPDATE providers SET status=?,last_error=?,last_checked_at=?,updated_at=? WHERE id=?`, status, security.RedactText(message), checkedAt.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), providerID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SyncProviderHealth recomputes provider status from enabled credential statuses.
func (s *Store) SyncProviderHealth(ctx context.Context, providerID string) error {
	credentials, err := s.Credentials(ctx, providerID)
	if err != nil {
		return err
	}
	status := "healthy"
	message := ""
	enabled := 0
	for _, credential := range credentials {
		if !credential.Enabled {
			continue
		}
		enabled++
		switch credential.Status {
		case "auth_required":
			status = "auth_required"
			if credential.LastError != "" {
				message = credential.LastError
			}
			return s.SetProviderHealth(ctx, providerID, status, message, time.Now())
		case "cooldown":
			if status == "healthy" {
				status = "degraded"
				if credential.LastError != "" {
					message = credential.LastError
				}
			}
		}
	}
	if enabled == 0 {
		status = "unknown"
	}
	return s.SetProviderHealth(ctx, providerID, status, message, time.Now())
}

// ClearCredentialCooldown restores account-level credential health without
// changing model-specific cooldowns for other upstream routes.
func (s *Store) ClearCredentialCooldown(ctx context.Context, credentialID string) error {
	updated, err := s.clearCredentialCooldown(ctx, credentialID)
	if err != nil {
		return err
	}
	if !updated {
		return sql.ErrNoRows
	}
	return nil
}

// ClearCooldown restores account-level credential health and removes every
// model-specific cooldown. It is intended for explicit administrative resets.
func (s *Store) ClearCooldown(ctx context.Context, credentialID string) error {
	if _, err := s.clearCredentialCooldown(ctx, credentialID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM credential_model_cooldowns WHERE credential_id=?`, credentialID)
	return err
}

func (s *Store) clearCredentialCooldown(ctx context.Context, credentialID string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE credentials SET cooldown_until='',status='healthy',last_error_code='',last_error='',last_validated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), credentialID)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

func (s *Store) ClearProviderCooldowns(ctx context.Context, providerID string) (int64, error) {
	credentials, err := s.Credentials(ctx, providerID)
	if err != nil {
		return 0, err
	}
	var cleared int64
	for _, credential := range credentials {
		if err = s.ClearCooldown(ctx, credential.ID); err != nil {
			return cleared, err
		}
		cleared++
	}
	return cleared, nil
}

func (s *Store) UpdateCredentialMetadata(ctx context.Context, credentialID string, metadata map[string]any) error {
	if credentialID == "" {
		return errors.New("credential id is required")
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	data, err := json.Marshal(redactPersistedMetadata(metadata))
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE credentials SET metadata_json=? WHERE id=?`, string(data), credentialID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) SetModelCooldown(ctx context.Context, credentialID, model, code, message string, until time.Time, status int, count int) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO credential_model_cooldowns(credential_id,model,until,code,message,status,count)
VALUES(?,?,?,?,?,?,?) ON CONFLICT(credential_id,model) DO UPDATE SET until=excluded.until,code=excluded.code,message=excluded.message,status=excluded.status,count=excluded.count`,
		credentialID, model, until.UTC().Format(time.RFC3339Nano), security.RedactText(code), security.RedactText(message), status, count)
	return err
}

func (s *Store) ClearModelCooldown(ctx context.Context, credentialID, model string) error {
	if model == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM credential_model_cooldowns WHERE credential_id=?`, credentialID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM credential_model_cooldowns WHERE credential_id=? AND model=?`, credentialID, model)
	return err
}

func (s *Store) ModelCooldownUntil(ctx context.Context, credentialID, model string, now time.Time) (time.Time, error) {
	row := s.db.QueryRowContext(ctx, `SELECT until FROM credential_model_cooldowns WHERE credential_id=? AND model=?`, credentialID, model)
	var untilRaw string
	if err := row.Scan(&untilRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	until, err := time.Parse(time.RFC3339Nano, untilRaw)
	if err != nil || !until.After(now) {
		return time.Time{}, nil
	}
	return until, nil
}

func (s *Store) ModelCooldownCount(ctx context.Context, credentialID, model string) int {
	row := s.db.QueryRowContext(ctx, `SELECT count FROM credential_model_cooldowns WHERE credential_id=? AND model=?`, credentialID, model)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0
	}
	return count
}

func (s *Store) MarkCredentialAuthRequired(ctx context.Context, credentialID, code string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE credentials SET status='auth_required',cooldown_until='',last_error_code=?,last_error=? WHERE id=?`, security.RedactText(code), "OAuth authorization is required", credentialID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// OAuthCredentialIDForLogin picks the credential row that should receive a fresh OAuth login.
// Reconnecting the same account (matching email) updates the existing credential instead of creating a duplicate.
func (s *Store) OAuthCredentialIDForLogin(ctx context.Context, providerID, candidateID, email string, explicitCredential bool) (string, error) {
	candidateID = strings.TrimSpace(candidateID)
	if candidateID == "" {
		return "", errors.New("credential id is required")
	}
	if explicitCredential || normalizeCredentialEmail(email) == "" {
		return candidateID, nil
	}
	existing, err := s.oauthCredentialIDByEmail(ctx, providerID, email)
	if err != nil {
		return "", err
	}
	if existing != "" {
		return existing, nil
	}
	return candidateID, nil
}

func normalizeCredentialEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func mergeOAuthTokenExtra(token *OAuthToken, old *OAuthToken) {
	if token == nil || old == nil || len(old.Extra) == 0 {
		return
	}
	if token.Extra == nil {
		token.Extra = make(map[string]any, len(old.Extra))
	}
	for key, value := range old.Extra {
		if _, exists := token.Extra[key]; !exists {
			token.Extra[key] = value
		}
	}
}

func (s *Store) oauthCredentialIDByEmail(ctx context.Context, providerID, email string) (string, error) {
	normalized := normalizeCredentialEmail(email)
	if normalized == "" {
		return "", nil
	}
	row := s.db.QueryRowContext(ctx, `SELECT id FROM credentials WHERE provider_id=? AND auth_type='oauth' AND lower(trim(email))=? ORDER BY CASE WHEN last_validated_at='' THEN 0 ELSE 1 END DESC, last_validated_at DESC, id LIMIT 1`, providerID, normalized)
	var id string
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return id, nil
}

func (s *Store) oauthCredentialIDsByEmailExcept(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, providerID, email, keepID string) ([]string, error) {
	normalized := normalizeCredentialEmail(email)
	if normalized == "" {
		return nil, nil
	}
	rows, err := queryer.QueryContext(ctx, `SELECT id FROM credentials WHERE provider_id=? AND auth_type='oauth' AND lower(trim(email))=? AND id<>? ORDER BY id`, providerID, normalized, keepID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func reassignCredentialHistoryTx(ctx context.Context, tx *sql.Tx, fromID, toID string) error {
	if fromID == "" || toID == "" || fromID == toID {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE usage_events SET credential_id=? WHERE credential_id=?`, toID, fromID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE request_logs SET credential_id=? WHERE credential_id=?`, toID, fromID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE media_jobs SET credential_id=? WHERE credential_id=?`, toID, fromID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE credential_model_cooldowns SET credential_id=? WHERE credential_id=? AND model NOT IN (SELECT model FROM credential_model_cooldowns WHERE credential_id=?)`, toID, fromID, toID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM credential_model_cooldowns WHERE credential_id=?`, fromID); err != nil {
		return err
	}
	return nil
}

func (s *Store) purgeOAuthCredentialDuplicatesTx(ctx context.Context, tx *sql.Tx, providerID, credentialID, email string) error {
	duplicateIDs, err := s.oauthCredentialIDsByEmailExcept(ctx, tx, providerID, email, credentialID)
	if err != nil {
		return err
	}
	for _, duplicateID := range duplicateIDs {
		if err := reassignCredentialHistoryTx(ctx, tx, duplicateID, credentialID); err != nil {
			return err
		}
		var dupLastUsed string
		var dupConsecutive int
		if err := tx.QueryRowContext(ctx, `SELECT last_used_at, consecutive_use_count FROM credentials WHERE id=?`, duplicateID).Scan(&dupLastUsed, &dupConsecutive); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if dupLastUsed != "" || dupConsecutive > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE credentials SET
last_used_at=CASE
  WHEN ?<>'' AND (last_used_at='' OR ?>last_used_at) THEN ?
  ELSE last_used_at
END,
consecutive_use_count=CASE
  WHEN ?>consecutive_use_count THEN ?
  ELSE consecutive_use_count
END
WHERE id=?`, dupLastUsed, dupLastUsed, dupLastUsed, dupConsecutive, dupConsecutive, credentialID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM credentials WHERE id=?`, duplicateID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SaveOAuthCredential(ctx context.Context, providerID, credentialID, label, email string, token OAuthToken) error {
	if providerID == "" || credentialID == "" || token.AccessToken == "" {
		return errors.New("provider id, credential id and access token are required")
	}
	if token.TokenType == "" {
		token.TokenType = "Bearer"
	}
	existingRow := false
	if existing, err := s.CredentialByID(ctx, credentialID); err == nil {
		existingRow = true
		if existing.OAuthToken != nil {
			mergeOAuthTokenExtra(&token, existing.OAuthToken)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	data, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("encode oauth token: %w", err)
	}
	ciphertext, err := s.encryptor.Encrypt(string(data))
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	rollback := func(cause error) error { _ = tx.Rollback(); return cause }
	var existingProvider, existingAuth string
	err = tx.QueryRowContext(ctx, `SELECT provider_id,auth_type FROM credentials WHERE id=?`, credentialID).Scan(&existingProvider, &existingAuth)
	if err == nil {
		if existingProvider != providerID {
			return rollback(fmt.Errorf("credential %q belongs to provider %q", credentialID, existingProvider))
		}
		if existingAuth != "oauth" {
			return rollback(fmt.Errorf("credential %q is not an oauth credential", credentialID))
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return rollback(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `INSERT INTO credentials(id,provider_id,auth_type,status,label,email,secret_ciphertext,metadata_json,priority,weight,enabled,last_validated_at,created_at)
VALUES(?,?,?,'healthy',?,?,?,'{}',0,1,1,?,?)
ON CONFLICT(id) DO UPDATE SET provider_id=excluded.provider_id,auth_type='oauth',status='healthy',label=CASE WHEN excluded.label<>'' THEN excluded.label ELSE credentials.label END,email=CASE WHEN excluded.email<>'' THEN excluded.email ELSE credentials.email END,secret_ciphertext=excluded.secret_ciphertext,metadata_json=credentials.metadata_json,priority=credentials.priority,weight=credentials.weight,enabled=credentials.enabled,last_used_at=credentials.last_used_at,consecutive_use_count=credentials.consecutive_use_count,created_at=CASE WHEN credentials.created_at<>'' THEN credentials.created_at ELSE excluded.created_at END,cooldown_until='',last_error_code='',last_error='',last_validated_at=excluded.last_validated_at`,
		credentialID, providerID, "oauth", label, email, ciphertext, now, now); err != nil {
		return rollback(err)
	}
	if existingRow {
		if err = s.purgeOAuthCredentialDuplicatesTx(ctx, tx, providerID, credentialID, email); err != nil {
			return rollback(err)
		}
	}
	return tx.Commit()
}

func (s *Store) UpdateOAuthToken(ctx context.Context, credentialID string, token OAuthToken) error {
	if token.AccessToken == "" {
		return errors.New("access token is required")
	}
	if token.TokenType == "" {
		token.TokenType = "Bearer"
	}
	data, err := json.Marshal(token)
	if err != nil {
		return err
	}
	ciphertext, err := s.encryptor.Encrypt(string(data))
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE credentials SET secret_ciphertext=?,status='healthy',cooldown_until='',last_error_code='',last_error='',last_validated_at=? WHERE id=? AND auth_type='oauth'`, ciphertext, time.Now().UTC().Format(time.RFC3339Nano), credentialID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) Providers(ctx context.Context) ([]Provider, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,type,name,base_url,enabled,status,last_error,last_checked_at,headers_json,config_json FROM providers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Provider
	for rows.Next() {
		var item Provider
		var enabled int
		var headers, providerConfig, checked string
		if err := rows.Scan(&item.ID, &item.Type, &item.Name, &item.BaseURL, &enabled, &item.Status, &item.LastError, &checked, &headers, &providerConfig); err != nil {
			return nil, err
		}
		item.Enabled = enabled != 0
		item.LastChecked, _ = time.Parse(time.RFC3339Nano, checked)
		_ = json.Unmarshal([]byte(headers), &item.Headers)
		decodeProviderConfig(providerConfig, &item)
		items = append(items, item)
	}
	return items, rows.Err()
}

// MigrateClaudeOAuthConfigs rewrites legacy Claude OAuth scopes stored in SQLite.
func (s *Store) MigrateClaudeOAuthConfigs(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, config_json FROM providers WHERE type='claude'`)
	if err != nil {
		return 0, err
	}
	type claudeRow struct {
		id          string
		rawConfig   string
	}
	pending := make([]claudeRow, 0, 4)
	for rows.Next() {
		var row claudeRow
		if err := rows.Scan(&row.id, &row.rawConfig); err != nil {
			_ = rows.Close()
			return 0, err
		}
		pending = append(pending, row)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	updated := 0
	for _, row := range pending {
		var stored struct {
			OAuth        *config.OAuthConfig `json:"oauth"`
			ProxyPoolIDs []string            `json:"proxy_pool_ids"`
			Config       map[string]any      `json:"config"`
			Limits       config.LimitPolicy  `json:"limits"`
		}
		if strings.TrimSpace(row.rawConfig) != "" {
			_ = json.Unmarshal([]byte(row.rawConfig), &stored)
		}
		if stored.OAuth == nil {
			stored.OAuth = &config.OAuthConfig{}
		}
		beforeScopes := append([]string(nil), stored.OAuth.Scopes...)
		beforeCode := ""
		if stored.OAuth.ExtraAuthParams != nil {
			beforeCode = stored.OAuth.ExtraAuthParams["code"]
		}
		beforeRedirect := strings.TrimSpace(stored.OAuth.RedirectURL)
		beforeListen := stored.OAuth.ListenForCallback
		config.NormalizeClaudeOAuth(stored.OAuth)
		afterCode := stored.OAuth.ExtraAuthParams["code"]
		if stringSlicesEqual(beforeScopes, stored.OAuth.Scopes) && beforeCode == afterCode && beforeRedirect == strings.TrimSpace(stored.OAuth.RedirectURL) && beforeListen == stored.OAuth.ListenForCallback {
			continue
		}
		encoded, err := json.Marshal(map[string]any{
			"oauth":          stored.OAuth,
			"proxy_pool_ids": stored.ProxyPoolIDs,
			"config":         stored.Config,
			"limits":         stored.Limits,
		})
		if err != nil {
			return updated, err
		}
		if _, err = s.db.ExecContext(ctx, `UPDATE providers SET config_json=?, updated_at=? WHERE id=?`, string(encoded), time.Now().UTC().Format(time.RFC3339Nano), row.id); err != nil {
			return updated, err
		}
		updated++
	}
	return updated, nil
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *Store) OAuthCredentials(ctx context.Context) ([]ProviderCredential, error) {
	providers, err := s.Providers(ctx)
	if err != nil {
		return nil, err
	}
	var result []ProviderCredential
	for _, provider := range providers {
		credentials, err := s.Credentials(ctx, provider.ID)
		if err != nil {
			return nil, err
		}
		for _, credential := range credentials {
			if credential.AuthType == "oauth" && credential.Enabled && credential.Status != "auth_required" {
				result = append(result, ProviderCredential{Provider: provider, Credential: credential})
			}
		}
	}
	return result, nil
}

func credentialStatus(authType string) string {
	if authType == "api_key" || authType == "none" || authType == "service_account" {
		return "healthy"
	}
	return "unknown"
}

func seedCredentialEligible(providerCfg config.ProviderConfig, credentialCfg config.CredentialConfig) bool {
	if !providerCfg.Enabled || !credentialCfg.IsEnabled() {
		return false
	}
	secret := credentialCfg.Secret
	if secret == "" && credentialCfg.SecretEnv != "" {
		secret = config.Env(credentialCfg.SecretEnv)
	}
	switch strings.ToLower(strings.TrimSpace(credentialCfg.AuthType)) {
	case "none":
		return true
	case "oauth":
		return false
	case "api_key", "service_account":
		return secret != ""
	default:
		return secret != ""
	}
}

func decodeProviderConfig(raw string, provider *Provider) {
	if strings.TrimSpace(raw) == "" || provider == nil {
		return
	}
	var stored struct {
		OAuth        *config.OAuthConfig `json:"oauth"`
		ProxyPoolIDs []string            `json:"proxy_pool_ids"`
		Config       map[string]any      `json:"config"`
		Limits       config.LimitPolicy  `json:"limits"`
	}
	if json.Unmarshal([]byte(raw), &stored) == nil {
		provider.OAuth = stored.OAuth
		provider.ProxyPoolIDs = stored.ProxyPoolIDs
		provider.Config = stored.Config
		provider.Limits = stored.Limits
	}
	if provider.Type == "claude" {
		if provider.OAuth == nil {
			provider.OAuth = &config.OAuthConfig{}
		}
		config.NormalizeClaudeOAuth(provider.OAuth)
	}
}

func credentialMetadata(credential config.CredentialConfig) map[string]any {
	metadata := make(map[string]any, len(credential.Metadata)+1)
	for key, value := range credential.Metadata {
		metadata[key] = value
	}
	if len(credential.ProxyPools) > 0 {
		metadata["proxy_pool_ids"] = append([]string(nil), credential.ProxyPools...)
	} else {
		delete(metadata, "proxy_pool_ids")
	}
	return metadata
}

func decodeCredentialMetadata(credential *Credential) {
	if credential == nil || credential.Metadata == nil {
		return
	}
	switch value := credential.Metadata["proxy_pool_ids"].(type) {
	case []any:
		for _, item := range value {
			if id, ok := item.(string); ok && strings.TrimSpace(id) != "" {
				credential.ProxyPoolIDs = append(credential.ProxyPoolIDs, id)
			}
		}
	case []string:
		credential.ProxyPoolIDs = append([]string(nil), value...)
	}
	delete(credential.Metadata, "proxy_pool_ids")
}

func (s *Store) AddUsage(ctx context.Context, event UsageEvent) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO usage_events(request_id,public_model_id,provider_id,upstream_model,credential_id,client_api_key_id,attempt,status,input_tokens,output_tokens,reasoning_tokens,cached_tokens,tokens_saved,estimated_cost_usd,latency_ms,error_code,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, event.RequestID, event.PublicModelID, event.ProviderID, event.UpstreamModel, event.CredentialID, event.ClientAPIKeyID, event.Attempt, event.Status, event.InputTokens, event.OutputTokens, event.ReasoningTokens, event.CachedTokens, event.TokensSaved, event.EstimatedCostUSD, event.LatencyMS, event.ErrorCode, event.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) AddRequestLog(ctx context.Context, item RequestLog) error {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	metadata, _ := json.Marshal(redactPersistedMetadata(item.Metadata))
	_, err := s.db.ExecContext(ctx, `INSERT INTO request_logs(request_id,client_api_key_id,method,path,protocol,public_model_id,provider_id,credential_id,attempt,status,latency_ms,error_code,metadata_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, item.RequestID, item.ClientAPIKeyID, item.Method, item.Path, item.Protocol, item.PublicModelID, item.ProviderID, item.CredentialID, item.Attempt, item.Status, item.LatencyMS, item.ErrorCode, string(metadata), item.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) RecentRequestLogs(ctx context.Context, limit int) ([]RequestLog, error) {
	limit = boundedLimit(limit)
	rows, err := s.db.QueryContext(ctx, `SELECT request_id,client_api_key_id,method,path,protocol,public_model_id,provider_id,credential_id,attempt,status,latency_ms,error_code,metadata_json,created_at FROM request_logs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []RequestLog
	for rows.Next() {
		var item RequestLog
		var metadata, created string
		if err := rows.Scan(&item.RequestID, &item.ClientAPIKeyID, &item.Method, &item.Path, &item.Protocol, &item.PublicModelID, &item.ProviderID, &item.CredentialID, &item.Attempt, &item.Status, &item.LatencyMS, &item.ErrorCode, &metadata, &created); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(metadata), &item.Metadata)
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) AddAuditEvent(ctx context.Context, item AuditEvent) error {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	metadata, _ := json.Marshal(redactPersistedMetadata(item.Metadata))
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_events(actor,action,resource_type,resource_id,status,metadata_json,created_at) VALUES(?,?,?,?,?,?,?)`, item.Actor, item.Action, item.ResourceType, item.ResourceID, item.Status, string(metadata), item.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) RecordConfigVersion(ctx context.Context, source string, cfg *config.Config) error {
	if cfg == nil {
		return errors.New("configuration is required")
	}
	safe, err := s.ExportConfig(ctx, cfg)
	if err != nil {
		return err
	}
	data, err := json.Marshal(safe)
	if err != nil {
		return err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	_, err = s.db.ExecContext(ctx, `INSERT INTO config_versions(source,digest,created_at) VALUES(?,?,?)`, source, digest, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) RecentConfigVersions(ctx context.Context, limit int) ([]ConfigVersion, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,source,digest,created_at FROM config_versions ORDER BY id DESC LIMIT ?`, boundedLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ConfigVersion
	for rows.Next() {
		var item ConfigVersion
		var created string
		if err := rows.Scan(&item.ID, &item.Source, &item.Digest, &created); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) RecentAuditEvents(ctx context.Context, limit int) ([]AuditEvent, error) {
	limit = boundedLimit(limit)
	rows, err := s.db.QueryContext(ctx, `SELECT id,actor,action,resource_type,resource_id,status,metadata_json,created_at FROM audit_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []AuditEvent
	for rows.Next() {
		var item AuditEvent
		var metadata, created string
		if err := rows.Scan(&item.ID, &item.Actor, &item.Action, &item.ResourceType, &item.ResourceID, &item.Status, &metadata, &created); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(metadata), &item.Metadata)
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		items = append(items, item)
	}
	return items, rows.Err()
}

func boundedLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func (s *Store) SaveMediaJob(ctx context.Context, job MediaJob) error {
	if strings.TrimSpace(job.ID) == "" || strings.TrimSpace(job.Kind) == "" {
		return errors.New("media job id and kind are required")
	}
	if job.Status == "" {
		job.Status = "queued"
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = job.CreatedAt
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO media_jobs(id,kind,status,public_model_id,provider_id,credential_id,upstream_id,client_api_key_id,idempotency_key,response_json,error_code,error_message,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,upstream_id=excluded.upstream_id,response_json=excluded.response_json,error_code=excluded.error_code,error_message=excluded.error_message,updated_at=excluded.updated_at`,
		job.ID, job.Kind, job.Status, job.PublicModelID, job.ProviderID, job.CredentialID, job.UpstreamID, job.ClientAPIKeyID, job.IdempotencyKey, job.ResponseJSON, job.ErrorCode, job.ErrorMessage, job.CreatedAt.UTC().Format(time.RFC3339Nano), job.UpdatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) UpdateMediaJob(ctx context.Context, jobID, status, responseJSON, errorCode, errorMessage string, updatedAt time.Time) error {
	if strings.TrimSpace(jobID) == "" {
		return errors.New("media job id is required")
	}
	if strings.TrimSpace(status) == "" {
		status = "unknown"
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `UPDATE media_jobs SET status=?,response_json=?,error_code=?,error_message=?,updated_at=? WHERE id=?`, status, responseJSON, errorCode, errorMessage, updatedAt.UTC().Format(time.RFC3339Nano), jobID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) PruneMediaJobs(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM media_jobs WHERE updated_at < ?`, before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) PruneUsage(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM usage_events WHERE created_at < ?`, before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) PruneRequestLogs(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM request_logs WHERE created_at < ?`, before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) PruneAuditEvents(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM audit_events WHERE created_at < ?`, before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) PruneConfigVersions(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM config_versions WHERE created_at < ?`, before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) MediaJob(ctx context.Context, id string) (*MediaJob, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,kind,status,public_model_id,provider_id,credential_id,upstream_id,client_api_key_id,idempotency_key,response_json,error_code,error_message,created_at,updated_at FROM media_jobs WHERE id=?`, id)
	return scanMediaJob(row)
}

func (s *Store) MediaJobByIdempotency(ctx context.Context, clientAPIKeyID, idempotencyKey string) (*MediaJob, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return nil, sql.ErrNoRows
	}
	row := s.db.QueryRowContext(ctx, `SELECT id,kind,status,public_model_id,provider_id,credential_id,upstream_id,client_api_key_id,idempotency_key,response_json,error_code,error_message,created_at,updated_at FROM media_jobs WHERE client_api_key_id=? AND idempotency_key=?`, clientAPIKeyID, idempotencyKey)
	return scanMediaJob(row)
}

func (s *Store) ActiveMediaJobCount(ctx context.Context, clientAPIKeyID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_jobs WHERE client_api_key_id=? AND lower(status) NOT IN ('completed','succeeded','failed','cancelled','canceled','expired')`, clientAPIKeyID).Scan(&count)
	return count, err
}

func scanMediaJob(row *sql.Row) (*MediaJob, error) {
	var job MediaJob
	var created, updated string
	if err := row.Scan(&job.ID, &job.Kind, &job.Status, &job.PublicModelID, &job.ProviderID, &job.CredentialID, &job.UpstreamID, &job.ClientAPIKeyID, &job.IdempotencyKey, &job.ResponseJSON, &job.ErrorCode, &job.ErrorMessage, &created, &updated); err != nil {
		return nil, err
	}
	job.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	job.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return &job, nil
}

type UsageEventsQuery struct {
	Limit      int
	Offset     int
	ProviderID string
}

func (s *Store) UsageEvents(ctx context.Context, query UsageEventsQuery) ([]UsageEvent, int, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}

	where := ""
	args := []any{}
	if strings.TrimSpace(query.ProviderID) != "" {
		where = ` WHERE provider_id = ?`
		args = append(args, query.ProviderID)
	}

	var total int
	countQuery := `SELECT COUNT(*) FROM usage_events` + where
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	listQuery := `SELECT request_id,public_model_id,provider_id,upstream_model,credential_id,client_api_key_id,attempt,status,input_tokens,output_tokens,reasoning_tokens,cached_tokens,tokens_saved,estimated_cost_usd,latency_ms,error_code,created_at FROM usage_events` + where + ` ORDER BY id DESC LIMIT ? OFFSET ?`
	listArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var items []UsageEvent
	for rows.Next() {
		var item UsageEvent
		var created string
		if err := rows.Scan(&item.RequestID, &item.PublicModelID, &item.ProviderID, &item.UpstreamModel, &item.CredentialID, &item.ClientAPIKeyID, &item.Attempt, &item.Status, &item.InputTokens, &item.OutputTokens, &item.ReasoningTokens, &item.CachedTokens, &item.TokensSaved, &item.EstimatedCostUSD, &item.LatencyMS, &item.ErrorCode, &created); err != nil {
			return nil, 0, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (s *Store) RecentUsage(ctx context.Context, limit int) ([]UsageEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `SELECT request_id,public_model_id,provider_id,upstream_model,credential_id,client_api_key_id,attempt,status,input_tokens,output_tokens,reasoning_tokens,cached_tokens,tokens_saved,estimated_cost_usd,latency_ms,error_code,created_at FROM usage_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []UsageEvent
	for rows.Next() {
		var item UsageEvent
		var created string
		if err := rows.Scan(&item.RequestID, &item.PublicModelID, &item.ProviderID, &item.UpstreamModel, &item.CredentialID, &item.ClientAPIKeyID, &item.Attempt, &item.Status, &item.InputTokens, &item.OutputTokens, &item.ReasoningTokens, &item.CachedTokens, &item.TokensSaved, &item.EstimatedCostUSD, &item.LatencyMS, &item.ErrorCode, &created); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) EstimatedCostSince(ctx context.Context, clientAPIKeyID string, since time.Time) (float64, error) {
	var total float64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(estimated_cost_usd),0) FROM usage_events WHERE client_api_key_id=? AND created_at>=?`, clientAPIKeyID, since.UTC().Format(time.RFC3339Nano)).Scan(&total)
	return total, err
}

func (s *Store) APIKeyUsageSince(ctx context.Context, clientAPIKeyID string, since time.Time) (requests int, cost float64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(estimated_cost_usd),0) FROM usage_events WHERE client_api_key_id=? AND created_at>=?`, clientAPIKeyID, since.UTC().Format(time.RFC3339Nano)).Scan(&requests, &cost)
	return requests, cost, err
}

func (s *Store) ProviderEstimatedCostSince(ctx context.Context, providerID string, since time.Time) (float64, error) {
	var total float64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(estimated_cost_usd),0) FROM usage_events WHERE provider_id=? AND created_at>=?`, providerID, since.UTC().Format(time.RFC3339Nano)).Scan(&total)
	return total, err
}

func (s *Store) ProviderRequestCountSince(ctx context.Context, providerID string, since time.Time) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_events WHERE provider_id=? AND created_at>=?`, providerID, since.UTC().Format(time.RFC3339Nano)).Scan(&count)
	return count, err
}

func (s *Store) TotalEstimatedCostSince(ctx context.Context, since time.Time) (float64, error) {
	var total float64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(estimated_cost_usd),0) FROM usage_events WHERE created_at>=?`, since.UTC().Format(time.RFC3339Nano)).Scan(&total)
	return total, err
}

func (s *Store) TeamEstimatedCostSince(ctx context.Context, teamID string, since time.Time) (float64, error) {
	teamID = strings.TrimSpace(teamID)
	if teamID == "" {
		return 0, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,policy_json FROM api_keys WHERE enabled=1`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var keyIDs []string
	for rows.Next() {
		var keyID, policyJSON string
		if err = rows.Scan(&keyID, &policyJSON); err != nil {
			return 0, err
		}
		var policy config.ClientKeyPolicy
		_ = json.Unmarshal([]byte(policyJSON), &policy)
		if policy.Team == teamID {
			keyIDs = append(keyIDs, keyID)
		}
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}
	var total float64
	for _, keyID := range keyIDs {
		value, sumErr := s.EstimatedCostSince(ctx, keyID, since)
		if sumErr != nil {
			return 0, sumErr
		}
		total += value
	}
	return total, nil
}

type Snapshot struct {
	Providers   []Provider                     `json:"providers"`
	ProxyPools  []ProxyPoolSummary             `json:"proxy_pools"`
	Models      []PublicModel                  `json:"models"`
	Aliases     []ModelAlias                   `json:"aliases"`
	Combos      []Combo                        `json:"combos"`
	Routes      map[string][]RouteTarget       `json:"routes"`
	Credentials map[string][]CredentialSummary `json:"credentials"`
	APIKeys     []APIKeySummary                `json:"api_keys"`
	Usage       UsageSummary                   `json:"usage"`
}
type ProxyPoolSummary struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	URL          string     `json:"url"`
	Enabled      bool       `json:"enabled"`
	Status       string     `json:"status"`
	LastError    string     `json:"last_error,omitempty"`
	LastTestedAt *time.Time `json:"last_tested_at,omitempty"`
	UsageCount   int        `json:"usage_count"`
}
type CredentialSummary struct {
	ID            string     `json:"id"`
	Label         string     `json:"label"`
	Email         string     `json:"email,omitempty"`
	AuthType      string     `json:"auth_type"`
	Status        string     `json:"status"`
	Priority      int        `json:"priority"`
	Weight        int        `json:"weight"`
	Enabled       bool       `json:"enabled"`
	CooldownUntil       *time.Time `json:"cooldown_until,omitempty"`
	LastErrorCode       string     `json:"last_error_code,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
	ProxyPoolIDs        []string   `json:"proxy_pool_ids,omitempty"`
	LastUsedAt          *time.Time `json:"last_used_at,omitempty"`
	LastValidatedAt     *time.Time `json:"last_validated_at,omitempty"`
	ConsecutiveUseCount int        `json:"consecutive_use_count,omitempty"`
	CreatedAt           *time.Time `json:"created_at,omitempty"`
}
type UsageSummary struct {
	Requests         int     `json:"requests"`
	Errors           int     `json:"errors"`
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	TokensSaved      int     `json:"tokens_saved"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
}

func (s *Store) Snapshot(ctx context.Context) (Snapshot, error) {
	models, err := s.PublicModels(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,type,name,base_url,enabled,status,last_error,last_checked_at,headers_json,config_json FROM providers ORDER BY id`)
	if err != nil {
		return Snapshot{}, err
	}
	defer rows.Close()
	var providers []Provider
	for rows.Next() {
		var p Provider
		var enabled int
		var headers, providerConfig, checked string
		if err := rows.Scan(&p.ID, &p.Type, &p.Name, &p.BaseURL, &enabled, &p.Status, &p.LastError, &checked, &headers, &providerConfig); err != nil {
			return Snapshot{}, err
		}
		p.Enabled = enabled != 0
		p.LastChecked, _ = time.Parse(time.RFC3339Nano, checked)
		_ = json.Unmarshal([]byte(headers), &p.Headers)
		decodeProviderConfig(providerConfig, &p)
		p.Headers = redactSnapshotHeaders(p.Headers)
		p.Config = redactSnapshotMap(p.Config)
		providers = append(providers, p)
	}
	if err = rows.Err(); err != nil {
		return Snapshot{}, err
	}
	apiKeys, err := s.APIKeys(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	aliases, err := s.ModelAliases(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	combos, err := s.Combos(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	proxyPools, err := s.ProxyPools(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{Providers: providers, Models: models, Aliases: aliases, Combos: combos, Routes: map[string][]RouteTarget{}, Credentials: map[string][]CredentialSummary{}, APIKeys: apiKeys}
	for _, pool := range proxyPools {
		var lastTested *time.Time
		if !pool.LastTestedAt.IsZero() {
			value := pool.LastTestedAt
			lastTested = &value
		}
		usageCount, _ := s.ProxyPoolUsageCount(ctx, pool.ID)
		snapshot.ProxyPools = append(snapshot.ProxyPools, ProxyPoolSummary{ID: pool.ID, Name: pool.Name, URL: RedactProxyURL(pool.URL), Enabled: pool.Enabled, Status: pool.Status, LastError: pool.LastError, LastTestedAt: lastTested, UsageCount: usageCount})
	}
	for _, model := range models {
		snapshot.Routes[model.ID], err = s.Routes(ctx, model.ID)
		if err != nil {
			return Snapshot{}, err
		}
	}
	for _, provider := range providers {
		creds, e := s.Credentials(ctx, provider.ID)
		if e != nil {
			return Snapshot{}, e
		}
		for _, c := range creds {
			var cooldown *time.Time
			if !c.CooldownUntil.IsZero() {
				value := c.CooldownUntil
				cooldown = &value
			}
			var lastUsed *time.Time
			if !c.LastUsedAt.IsZero() {
				value := c.LastUsedAt
				lastUsed = &value
			}
			var createdAt *time.Time
			if !c.CreatedAt.IsZero() {
				value := c.CreatedAt
				createdAt = &value
			}
			var lastValidated *time.Time
			if !c.LastValidated.IsZero() {
				value := c.LastValidated
				lastValidated = &value
			}
			snapshot.Credentials[provider.ID] = append(snapshot.Credentials[provider.ID], CredentialSummary{
				ID: c.ID, Label: c.Label, Email: c.Email, AuthType: c.AuthType, Status: c.Status,
				Priority: c.Priority, Weight: c.Weight, Enabled: c.Enabled, CooldownUntil: cooldown,
				LastErrorCode: c.LastErrorCode, LastError: c.LastError, ProxyPoolIDs: c.ProxyPoolIDs, LastUsedAt: lastUsed,
				LastValidatedAt: lastValidated, ConsecutiveUseCount: c.ConsecutiveUseCount, CreatedAt: createdAt,
			})
		}
	}
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN status>=400 OR status=0 THEN 1 ELSE 0 END),0),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(tokens_saved),0),COALESCE(SUM(estimated_cost_usd),0) FROM usage_events`)
	_ = row.Scan(&snapshot.Usage.Requests, &snapshot.Usage.Errors, &snapshot.Usage.InputTokens, &snapshot.Usage.OutputTokens, &snapshot.Usage.TokensSaved, &snapshot.Usage.EstimatedCostUSD)
	return snapshot, nil
}

// ExportConfig builds a declarative, secret-free configuration template from
// SQLite. Secret-bearing values are represented by environment placeholders so
// the result can be reviewed or imported only after the operator supplies the
// corresponding environment variables.
func (s *Store) ExportConfig(ctx context.Context, base *config.Config) (*config.Config, error) {
	result := &config.Config{}
	if base != nil {
		copy := *base
		result = &copy
	}
	result.Database.Driver = "sqlite"
	if result.Database.DSN == "" {
		result.Database.DSN = s.path
	}
	result.ProxyPools = nil
	pools, err := s.ProxyPools(ctx)
	if err != nil {
		return nil, err
	}
	for _, pool := range pools {
		enabled := pool.Enabled
		result.ProxyPools = append(result.ProxyPools, config.ProxyPoolConfig{ID: pool.ID, Name: pool.Name, URLEnv: "TPROXY_PROXY_" + exportToken(pool.ID), Enabled: &enabled})
	}
	result.Providers = nil
	providers, err := s.Providers(ctx)
	if err != nil {
		return nil, err
	}
	for _, provider := range providers {
		item := config.ProviderConfig{ID: provider.ID, Type: provider.Type, Name: provider.Name, BaseURL: provider.BaseURL, Enabled: provider.Enabled, Headers: redactExportHeaders(provider.Headers), Config: redactExportMap(provider.Config), Limits: provider.Limits, OAuth: provider.OAuth, ProxyPools: append([]string(nil), provider.ProxyPoolIDs...)}
		credentials, credentialsErr := s.Credentials(ctx, provider.ID)
		if credentialsErr != nil {
			return nil, credentialsErr
		}
		for _, credential := range credentials {
			enabled := credential.Enabled
			item.Credentials = append(item.Credentials, config.CredentialConfig{ID: credential.ID, Label: credential.Label, Email: credential.Email, AuthType: credential.AuthType, SecretEnv: "TPROXY_CREDENTIAL_" + exportToken(credential.ID), Priority: credential.Priority, Weight: credential.Weight, Enabled: &enabled, Metadata: redactExportMap(credential.Metadata), ProxyPools: append([]string(nil), credential.ProxyPoolIDs...)})
		}
		result.Providers = append(result.Providers, item)
	}
	result.Models = nil
	models, err := s.PublicModels(ctx)
	if err != nil {
		return nil, err
	}
	for _, model := range models {
		item := config.PublicModelConfig{ID: model.ID, DisplayName: model.DisplayName, Aliases: append([]string(nil), model.Aliases...), Enabled: model.Enabled, ExposeUpstreamName: model.ExposeUpstreamName, RewriteResponseModel: model.RewriteResponseModel, Capabilities: append([]string(nil), model.Capabilities...), Limits: model.Limits}
		routes, routesErr := s.Routes(ctx, model.ID)
		if routesErr != nil {
			return nil, routesErr
		}
		for _, route := range routes {
			item.Routes = append(item.Routes, config.RouteTargetConfig{ID: route.ID, Provider: route.ProviderID, UpstreamModel: route.UpstreamModel, Priority: route.Priority, Weight: route.Weight, Enabled: boolPointer(route.Enabled), Conditions: route.Conditions, Pricing: route.Pricing})
		}
		result.Models = append(result.Models, item)
	}
	result.Combos = nil
	combos, err := s.Combos(ctx)
	if err != nil {
		return nil, err
	}
	for _, combo := range combos {
		item := config.ComboConfig{ID: combo.ID, DisplayName: combo.DisplayName, Enabled: combo.Enabled, RewriteResponseModel: combo.RewriteResponseModel, Capabilities: append([]string(nil), combo.Capabilities...), Limits: combo.Limits, Policy: combo.Policy}
		for _, comboItem := range combo.Items {
			item.Items = append(item.Items, config.ComboItemConfig{PublicModelID: comboItem.PublicModelID, RouteTargetID: comboItem.RouteTargetID})
		}
		result.Combos = append(result.Combos, item)
	}
	result.ClientAPIKeys = nil
	keys, err := s.APIKeys(ctx)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		result.ClientAPIKeys = append(result.ClientAPIKeys, config.ClientAPIKey{ID: key.ID, Name: key.Name, KeyEnv: "TPROXY_CLIENT_KEY_" + exportToken(key.ID), Models: append([]string(nil), key.Models...), Policy: key.Policy})
	}
	return result, nil
}

func boolPointer(value bool) *bool { return &value }

func exportToken(value string) string {
	var builder strings.Builder
	for _, char := range strings.ToUpper(value) {
		if (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('_')
		}
	}
	return strings.Trim(builder.String(), "_")
}

func redactExportMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		if sensitiveFieldName(key) {
			result[key+"_env"] = "TPROXY_PROVIDER_" + exportToken(key)
			continue
		}
		result[key] = redactExportValue(value)
	}
	return result
}

func redactExportValue(value any) any {
	switch item := value.(type) {
	case map[string]any:
		return redactExportMap(item)
	case []any:
		result := make([]any, len(item))
		for index, entry := range item {
			result[index] = redactExportValue(entry)
		}
		return result
	default:
		return value
	}
}

func redactSnapshotMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		if sensitiveFieldName(key) {
			result[key] = "[REDACTED]"
			continue
		}
		switch item := value.(type) {
		case map[string]any:
			result[key] = redactSnapshotMap(item)
		case []any:
			entries := make([]any, len(item))
			for index, entry := range item {
				if nested, ok := entry.(map[string]any); ok {
					entries[index] = redactSnapshotMap(nested)
				} else {
					entries[index] = entry
				}
			}
			result[key] = entries
		default:
			result[key] = value
		}
	}
	return result
}

func redactSnapshotHeaders(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		if sensitiveFieldName(key) {
			result[key] = "[REDACTED]"
		} else {
			result[key] = value
		}
	}
	return result
}

// redactPersistedMetadata applies the same secret boundary to request and
// audit records that is used for management snapshots. Logs are a durable
// store, so callers must not be able to bypass redaction by writing directly
// through Store.AddRequestLog/AddAuditEvent.
func redactPersistedMetadata(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		if sensitiveFieldName(key) {
			result[key] = "[REDACTED]"
			continue
		}
		result[key] = redactPersistedValue(value)
	}
	return result
}

func redactPersistedValue(value any) any {
	switch item := value.(type) {
	case map[string]any:
		return redactPersistedMetadata(item)
	case []any:
		result := make([]any, len(item))
		for index, nested := range item {
			result[index] = redactPersistedValue(nested)
		}
		return result
	case string:
		return security.RedactText(item)
	default:
		return value
	}
}

func redactExportHeaders(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		if !sensitiveFieldName(key) {
			result[key] = value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func sensitiveFieldName(name string) bool {
	var normalized strings.Builder
	for _, char := range strings.ToLower(name) {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			normalized.WriteRune(char)
		}
	}
	value := normalized.String()
	if strings.HasSuffix(value, "url") || strings.HasSuffix(value, "uri") || strings.HasSuffix(value, "endpoint") {
		return false
	}
	if strings.HasSuffix(value, "tokentype") || strings.HasSuffix(value, "expiresat") || strings.HasSuffix(value, "expiresin") || strings.HasSuffix(value, "expiry") || strings.HasSuffix(value, "expiration") {
		return false
	}
	return strings.Contains(value, "secret") || strings.Contains(value, "password") || strings.Contains(value, "token") || strings.Contains(value, "apikey") || strings.Contains(value, "authorization") || strings.Contains(value, "cookie") || strings.Contains(value, "privatekey")
}

func (s *Store) PublicModelAllowed(apiKey *APIKey, modelID string) bool {
	if apiKey == nil || len(apiKey.Models) == 0 {
		return true
	}
	for _, allowed := range apiKey.Models {
		if allowed == modelID || allowed == "*" {
			return true
		}
	}
	return false
}

func EligibleCredentials(creds []Credential, now time.Time) []Credential {
	items := make([]Credential, 0, len(creds))
	for _, c := range creds {
		if c.Enabled && c.Status != "auth_required" && c.Status != "disabled" && (c.CooldownUntil.IsZero() || !c.CooldownUntil.After(now)) {
			items = append(items, c)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Priority == items[j].Priority {
			return items[i].ID < items[j].ID
		}
		return items[i].Priority > items[j].Priority
	})
	return items
}

func IsRetryableStatus(status int) bool { return status == 408 || status == 429 || status >= 500 }

func ErrorCode(status int) string {
	switch status {
	case 401, 403:
		return "upstream_auth"
	case 408:
		return "upstream_timeout"
	case 429:
		return "upstream_rate_limited"
	default:
		if status >= 500 {
			return "upstream_server_error"
		}
		return "provider_error"
	}
}

func NormalizeBaseURL(base string) string { return strings.TrimRight(strings.TrimSpace(base), "/") }
