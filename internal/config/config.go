package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/routing"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server        ServerConfig        `yaml:"server"`
	Database      DatabaseConfig      `yaml:"database"`
	Security      SecurityConfig      `yaml:"security"`
	Routing       RoutingConfig       `yaml:"routing"`
	Retention     RetentionConfig     `yaml:"retention"`
	Limits        LimitPolicy         `yaml:"limits" json:"limits"`
	Teams         []TeamPolicyConfig  `yaml:"teams" json:"teams"`
	ProxyPools    []ProxyPoolConfig   `yaml:"proxy-pools"`
	ClientAPIKeys []ClientAPIKey      `yaml:"client-api-keys"`
	Providers     []ProviderConfig    `yaml:"providers"`
	Models        []PublicModelConfig `yaml:"models"`
	Combos        []ComboConfig       `yaml:"combos"`
	ClaudeAliases ClaudeAliasConfig   `yaml:"claude-aliases" json:"claude_aliases"`
}

type ClaudeAliasConfig struct {
	Default              string `yaml:"default" json:"default"`
	Opus                 string `yaml:"opus" json:"opus"`
	Sonnet               string `yaml:"sonnet" json:"sonnet"`
	Haiku                string `yaml:"haiku" json:"haiku"`
	Fable                string `yaml:"fable" json:"fable"`
	DefaultCodexProvider string `yaml:"default-codex-provider" json:"default_codex_provider"`
}

type ProxyPoolConfig struct {
	ID      string `yaml:"id" json:"id"`
	Name    string `yaml:"name" json:"name"`
	URL     string `yaml:"url" json:"url,omitempty"`
	URLEnv  string `yaml:"url-env" json:"url_env,omitempty"`
	Enabled *bool  `yaml:"enabled" json:"enabled,omitempty"`
}

func (p ProxyPoolConfig) IsEnabled() bool { return p.Enabled == nil || *p.Enabled }

type RoutingConfig struct {
	Strategy              string                            `yaml:"strategy" json:"strategy"`
	StickyRoundRobinLimit int                               `yaml:"sticky-round-robin-limit" json:"sticky_round_robin_limit"`
	ProviderStrategies    map[string]ProviderRotationConfig `yaml:"provider-strategies" json:"provider_strategies,omitempty"`
	SessionAffinity       bool                              `yaml:"session-affinity" json:"session_affinity"`
	SessionAffinityTTL    string                            `yaml:"session-affinity-ttl" json:"session_affinity_ttl"`
	Cooldown              CooldownConfig                    `yaml:"cooldown" json:"cooldown"`
	Prewarm               PrewarmConfig                     `yaml:"prewarm" json:"prewarm"`
	Failover              FailoverConfig                    `yaml:"failover" json:"failover"`
}

type ProviderRotationConfig struct {
	Strategy              string `yaml:"strategy" json:"strategy,omitempty"`
	StickyRoundRobinLimit int    `yaml:"sticky-round-robin-limit" json:"sticky_round_robin_limit,omitempty"`
}

// FailoverConfig controls when a failing provider is pulled out of a model's
// priority chain so traffic moves on to the next provider.
type FailoverConfig struct {
	// Enabled defaults to true when omitted.
	Enabled *bool `yaml:"enabled" json:"enabled,omitempty"`
	// FailureThreshold is the number of consecutive failed attempts against a
	// provider (for one model) before it is skipped. Defaults to 3.
	FailureThreshold int `yaml:"failure-threshold" json:"failure_threshold,omitempty"`
	// DegradedThreshold flags a provider as unhealthy before it is skipped.
	DegradedThreshold int `yaml:"degraded-threshold" json:"degraded_threshold,omitempty"`
	// ResetTimeout is how long a skipped provider waits before a probe request.
	// Defaults to 5m.
	ResetTimeout string `yaml:"reset-timeout" json:"reset_timeout,omitempty"`
	// MaxResetTimeout caps the exponential backoff applied to repeat offenders.
	// Defaults to 30m.
	MaxResetTimeout string `yaml:"max-reset-timeout" json:"max_reset_timeout,omitempty"`
	// CountAccountErrors includes 429/401/403 in the failure count. Off by
	// default because accounts have their own cooldown and refresh handling.
	CountAccountErrors bool `yaml:"count-account-errors" json:"count_account_errors,omitempty"`
	// Providers overrides the policy for individual providers.
	Providers map[string]ProviderFailoverConfig `yaml:"providers" json:"providers,omitempty"`
}

func (f FailoverConfig) IsEnabled() bool { return f.Enabled == nil || *f.Enabled }

type ProviderFailoverConfig struct {
	FailureThreshold  int    `yaml:"failure-threshold" json:"failure_threshold,omitempty"`
	DegradedThreshold int    `yaml:"degraded-threshold" json:"degraded_threshold,omitempty"`
	ResetTimeout      string `yaml:"reset-timeout" json:"reset_timeout,omitempty"`
	MaxResetTimeout   string `yaml:"max-reset-timeout" json:"max_reset_timeout,omitempty"`
}

type CooldownConfig struct {
	Default       string `yaml:"default" json:"default"`
	Max           string `yaml:"max" json:"max"`
	BackoffBase   string `yaml:"backoff-base" json:"backoff_base"`
	PermanentAuth string `yaml:"permanent-auth" json:"permanent_auth"`
	Status401     string `yaml:"status-401" json:"status_401"`
	Status402     string `yaml:"status-402" json:"status_402"`
	Status403     string `yaml:"status-403" json:"status_403"`
	Status404     string `yaml:"status-404" json:"status_404"`
	Status429     string `yaml:"status-429" json:"status_429"`
	Status5xx     string `yaml:"status-5xx" json:"status_5xx"`
}

type PrewarmConfig struct {
	Enabled              *bool  `yaml:"enabled" json:"enabled"`
	CheckInterval        string `yaml:"check-interval" json:"check_interval"`
	PrewarmBeforeExpiry  string `yaml:"before-expiry" json:"before_expiry"`
	MaxConcurrentPrewarm int    `yaml:"max-concurrent" json:"max_concurrent"`
	TopNAccounts         int    `yaml:"top-n" json:"top_n"`
}

type RetentionConfig struct {
	UsageEvents     string `yaml:"usage-events" json:"usage_events"`
	RequestLogs     string `yaml:"request-logs" json:"request_logs"`
	AuditEvents     string `yaml:"audit-events" json:"audit_events"`
	MediaJobs       string `yaml:"media-jobs" json:"media_jobs"`
	OAuthSessions   string `yaml:"oauth-sessions" json:"oauth_sessions"`
	CleanupInterval string `yaml:"cleanup-interval" json:"cleanup_interval"`
}

type TeamPolicyConfig struct {
	ID     string      `yaml:"id" json:"id"`
	Limits LimitPolicy `yaml:"limits" json:"limits"`
}

type ServerConfig struct {
	Host                  string `yaml:"host"`
	Port                  int    `yaml:"port"`
	AllowLocalWithoutKey  bool   `yaml:"allow-local-without-key"`
	AllowRemoteManagement bool   `yaml:"allow-remote-management"`
	AllowUpstreamModels   bool   `yaml:"allow-upstream-models" json:"allow_upstream_models"`
}

type DatabaseConfig struct {
	Driver string `yaml:"driver"`
	DSN    string `yaml:"dsn"`
}

type SecurityConfig struct {
	MasterKeyEnv        string `yaml:"master-key-env"`
	ManagementSecretEnv string `yaml:"management-secret-env"`
	PluginsEnabled      bool   `yaml:"plugins-enabled" json:"plugins_enabled"`
}

type ClientAPIKey struct {
	ID     string          `yaml:"id"`
	Name   string          `yaml:"name"`
	KeyEnv string          `yaml:"key-env"`
	Models []string        `yaml:"models"`
	Policy ClientKeyPolicy `yaml:"policy" json:"policy"`
}

type ClientKeyPolicy struct {
	Endpoints []string          `yaml:"endpoints" json:"endpoints,omitempty"`
	Team      string            `yaml:"team" json:"team,omitempty"`
	Tags      map[string]string `yaml:"tags" json:"tags,omitempty"`
	Limits    LimitPolicy       `yaml:"limits" json:"limits,omitempty"`
}

type LimitPolicy struct {
	RequestsPerMinute int     `yaml:"requests-per-minute" json:"requests_per_minute,omitempty"`
	ConcurrentStreams int     `yaml:"concurrent-streams" json:"concurrent_streams,omitempty"`
	MaxInputBytes     int64   `yaml:"max-input-bytes" json:"max_input_bytes,omitempty"`
	MaxOutputTokens   int     `yaml:"max-output-tokens" json:"max_output_tokens,omitempty"`
	MediaJobs         int     `yaml:"media-jobs" json:"media_jobs,omitempty"`
	BudgetUSDPerDay   float64 `yaml:"budget-usd-per-day" json:"budget_usd_per_day,omitempty"`
}

type ProviderConfig struct {
	ID          string             `yaml:"id" json:"id"`
	Type        string             `yaml:"type" json:"type"`
	Name        string             `yaml:"name" json:"name"`
	BaseURL     string             `yaml:"base-url" json:"base_url"`
	Enabled     bool               `yaml:"enabled" json:"enabled"`
	Headers     map[string]string  `yaml:"headers" json:"headers"`
	Config      map[string]any     `yaml:"config" json:"config"`
	Limits      LimitPolicy        `yaml:"limits" json:"limits"`
	OAuth       *OAuthConfig       `yaml:"oauth,omitempty" json:"oauth,omitempty"`
	ProxyPools  []string           `yaml:"proxy-pools" json:"proxy_pools"`
	Credentials []CredentialConfig `yaml:"credentials" json:"credentials"`
}

type OAuthConfig struct {
	DiscoveryURL              string            `yaml:"discovery-url" json:"discovery_url"`
	AuthorizationURL          string            `yaml:"authorization-url" json:"authorization_url"`
	TokenURL                  string            `yaml:"token-url" json:"token_url"`
	RefreshURL                string            `yaml:"refresh-url" json:"refresh_url"`
	UserInfoURL               string            `yaml:"user-info-url" json:"user_info_url"`
	DeviceCodeURL             string            `yaml:"device-code-url" json:"device_code_url"`
	DeviceTokenURL            string            `yaml:"device-token-url" json:"device_token_url"`
	DeviceVerificationURL     string            `yaml:"device-verification-url" json:"device_verification_url"`
	DeviceExchangeRedirectURL string            `yaml:"device-exchange-redirect-url" json:"device_exchange_redirect_url"`
	DeviceFlow                string            `yaml:"device-flow" json:"device_flow"`
	DevicePKCE                bool              `yaml:"device-pkce" json:"device_pkce"`
	DeviceRequestFormat       string            `yaml:"device-request-format" json:"device_request_format"`
	ClientID                  string            `yaml:"client-id,omitempty" json:"client_id,omitempty"`
	ClientIDEnv               string            `yaml:"client-id-env" json:"client_id_env"`
	ClientSecretEnv           string            `yaml:"client-secret-env" json:"client_secret_env"`
	RequireClientSecret       bool              `yaml:"require-client-secret" json:"require_client_secret"`
	Scopes                    []string          `yaml:"scopes" json:"scopes"`
	RedirectURL               string            `yaml:"redirect-url" json:"redirect_url"`
	TokenRequestFormat        string            `yaml:"token-request-format" json:"token_request_format"`
	ListenForCallback         bool              `yaml:"listen-for-callback" json:"listen_for_callback"`
	IncludeStateInToken       bool              `yaml:"include-state-in-token-request" json:"include_state_in_token_request"`
	ExtraAuthParams           map[string]string `yaml:"extra-auth-params" json:"extra_auth_params"`
	ExtraTokenParams          map[string]string `yaml:"extra-token-params" json:"extra_token_params"`
	ExtraRefreshParams        map[string]string `yaml:"extra-refresh-params" json:"extra_refresh_params"`
	RefreshSafetyWindow       string            `yaml:"refresh-safety-window" json:"refresh_safety_window"`
	// MaxTokenAge is how long an access token may go without being refreshed
	// before the background loop proactively rotates it, regardless of the
	// expires_at the provider reported. Providers commonly invalidate a refresh
	// token that has sat unused, so a long-lived access token is not a reason to
	// stop refreshing. HardMaxTokenAge is the ceiling enforced on the request
	// path: past it a refresh happens before the credential is used at all.
	MaxTokenAge     string `yaml:"max-token-age" json:"max_token_age"`
	HardMaxTokenAge string `yaml:"hard-max-token-age" json:"hard_max_token_age"`
}

type CredentialConfig struct {
	ID         string         `yaml:"id" json:"id"`
	Label      string         `yaml:"label" json:"label"`
	Email      string         `yaml:"email" json:"email"`
	AuthType   string         `yaml:"auth-type" json:"auth_type"`
	SecretEnv  string         `yaml:"secret-env" json:"secret_env"`
	Secret     string         `yaml:"-" json:"secret,omitempty"`
	Priority   int            `yaml:"priority" json:"priority"`
	Weight     int            `yaml:"weight" json:"weight"`
	Enabled    *bool          `yaml:"enabled" json:"enabled"`
	Metadata   map[string]any `yaml:"metadata" json:"metadata"`
	ProxyPools []string       `yaml:"proxy-pools" json:"proxy_pools"`
}

func (c CredentialConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

type PublicModelConfig struct {
	ID                   string              `yaml:"id" json:"id"`
	DisplayName          string              `yaml:"display-name" json:"display_name"`
	Aliases              []string            `yaml:"aliases" json:"aliases"`
	Enabled              bool                `yaml:"enabled" json:"enabled"`
	ExposeUpstreamName   bool                `yaml:"expose-upstream-name" json:"expose_upstream_name"`
	RewriteResponseModel bool                `yaml:"rewrite-response-model" json:"rewrite_response_model"`
	Capabilities         []string            `yaml:"capabilities" json:"capabilities"`
	Limits               map[string]any      `yaml:"limits" json:"limits"`
	Routes               []RouteTargetConfig `yaml:"routes" json:"routes"`
}

type ComboConfig struct {
	ID                   string            `yaml:"id" json:"id"`
	DisplayName          string            `yaml:"display-name" json:"display_name"`
	Enabled              bool              `yaml:"enabled" json:"enabled"`
	RewriteResponseModel bool              `yaml:"rewrite-response-model" json:"rewrite_response_model"`
	Capabilities         []string          `yaml:"capabilities" json:"capabilities"`
	Limits               map[string]any    `yaml:"limits" json:"limits"`
	Policy               map[string]any    `yaml:"policy" json:"policy"`
	Items                []ComboItemConfig `yaml:"items" json:"items"`
}

type ComboItemConfig struct {
	PublicModelID string `yaml:"public-model" json:"public_model_id"`
	RouteTargetID string `yaml:"route-target" json:"route_target_id,omitempty"`
}

type RouteTargetConfig struct {
	ID            string         `yaml:"id" json:"id"`
	Provider      string         `yaml:"provider" json:"provider"`
	UpstreamModel string         `yaml:"upstream-model" json:"upstream_model"`
	Priority      int            `yaml:"priority" json:"priority"`
	Weight        int            `yaml:"weight" json:"weight"`
	Enabled       *bool          `yaml:"enabled" json:"enabled"`
	Conditions    map[string]any `yaml:"conditions" json:"conditions"`
	Pricing       *PricingConfig `yaml:"pricing,omitempty" json:"pricing,omitempty"`
}

type PricingConfig struct {
	InputPerMillion     float64 `yaml:"input-per-million" json:"input_per_million"`
	OutputPerMillion    float64 `yaml:"output-per-million" json:"output_per_million"`
	ReasoningPerMillion float64 `yaml:"reasoning-per-million" json:"reasoning_per_million"`
	Request             float64 `yaml:"request" json:"request"`
}

func (r RouteTargetConfig) IsEnabled() bool {
	return r.Enabled == nil || *r.Enabled
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err = yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	applyDefaults(&cfg)
	if cfg.Database.Driver == "sqlite" && !filepath.IsAbs(cfg.Database.DSN) {
		cfg.Database.DSN = filepath.Join(filepath.Dir(path), cfg.Database.DSN)
	}
	if err = cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	cfg.Server.Host = strings.TrimSpace(cfg.Server.Host)
	if cfg.Server.Host == "" {
		cfg.Server.Host = "127.0.0.1"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 28120
	}
	if cfg.Database.Driver == "" {
		cfg.Database.Driver = "sqlite"
	}
	if cfg.Database.DSN == "" {
		cfg.Database.DSN = "tproxy.db"
	}
	if cfg.Security.MasterKeyEnv == "" {
		cfg.Security.MasterKeyEnv = "TPROXY_MASTER_KEY"
	}
	if cfg.Security.ManagementSecretEnv == "" {
		cfg.Security.ManagementSecretEnv = "TPROXY_MANAGEMENT_SECRET"
	}
	if cfg.Routing.Strategy == "" {
		cfg.Routing.Strategy = "round-robin"
	}
	if cfg.Routing.StickyRoundRobinLimit <= 0 {
		cfg.Routing.StickyRoundRobinLimit = 3
	}
	if cfg.Routing.SessionAffinityTTL == "" {
		cfg.Routing.SessionAffinityTTL = "1h"
	}
	if cfg.Retention.UsageEvents == "" {
		cfg.Retention.UsageEvents = "2160h"
	}
	if cfg.Retention.RequestLogs == "" {
		cfg.Retention.RequestLogs = "720h"
	}
	if cfg.Retention.AuditEvents == "" {
		cfg.Retention.AuditEvents = "2160h"
	}
	if cfg.Retention.MediaJobs == "" {
		cfg.Retention.MediaJobs = "720h"
	}
	if cfg.Retention.OAuthSessions == "" {
		cfg.Retention.OAuthSessions = "24h"
	}
	if cfg.Retention.CleanupInterval == "" {
		cfg.Retention.CleanupInterval = "1h"
	}
	for index := range cfg.Providers {
		ApplyProviderDefaults(&cfg.Providers[index])
	}
}

func applyClineProviderDefaults(provider *ProviderConfig, name string) {
	if provider.Name == "" {
		provider.Name = name
	}
	if provider.BaseURL == "" {
		// OpenAI-compatible root; chat uses /chat/completions, discovery differs by type.
		provider.BaseURL = "https://api.cline.bot/api/v1"
	}
	if len(provider.Headers) == 0 {
		provider.Headers = map[string]string{
			"HTTP-Referer": "https://cline.bot",
			"X-Title":      "Cline",
		}
	}
	if provider.OAuth == nil {
		provider.OAuth = &OAuthConfig{}
	}
	// Shared WorkOS / AuthKit flow for both Cline account and ClinePass.
	if provider.OAuth.AuthorizationURL == "" {
		provider.OAuth.AuthorizationURL = "https://api.cline.bot/api/v1/auth/authorize"
	}
	if provider.OAuth.TokenURL == "" {
		provider.OAuth.TokenURL = "https://api.cline.bot/api/v1/auth/token"
	}
	if provider.OAuth.RefreshURL == "" {
		provider.OAuth.RefreshURL = "https://api.cline.bot/api/v1/auth/refresh"
	}
	if provider.OAuth.RefreshSafetyWindow == "" {
		provider.OAuth.RefreshSafetyWindow = "20m"
	}
	if provider.OAuth.ClientID == "" {
		provider.OAuth.ClientID = "extension"
	}
	// Browser callback paste flow (authkit.cline.bot) — no fixed loopback listener.
	provider.OAuth.ListenForCallback = false
}

func ApplyProviderDefaults(provider *ProviderConfig) {
	if provider == nil {
		return
	}
	if provider.ID == "cursor" && provider.Type == "openai-compatible" {
		provider.Type = "cursor"
	}
	switch provider.Type {
	case "codex":
		if provider.Name == "" {
			provider.Name = "OpenAI Codex"
		}
		if provider.BaseURL == "" {
			provider.BaseURL = "https://chatgpt.com/backend-api/codex"
		}
		if provider.OAuth == nil {
			provider.OAuth = &OAuthConfig{}
		}
		setOAuthDefault(provider.OAuth, "https://auth.openai.com/oauth/authorize", "https://auth.openai.com/oauth/token", "app_EMoamEEZ73f0CkXaXp7hrann", "http://localhost:1455/auth/callback")
		if provider.OAuth.DeviceCodeURL == "" {
			provider.OAuth.DeviceCodeURL = "https://auth.openai.com/api/accounts/deviceauth/usercode"
		}
		if provider.OAuth.DeviceTokenURL == "" {
			provider.OAuth.DeviceTokenURL = "https://auth.openai.com/api/accounts/deviceauth/token"
		}
		if provider.OAuth.DeviceVerificationURL == "" {
			provider.OAuth.DeviceVerificationURL = "https://auth.openai.com/codex/device"
		}
		if provider.OAuth.DeviceExchangeRedirectURL == "" {
			provider.OAuth.DeviceExchangeRedirectURL = "https://auth.openai.com/deviceauth/callback"
		}
		provider.OAuth.DeviceFlow = "codex"
		provider.OAuth.DeviceRequestFormat = "json"
		if len(provider.OAuth.Scopes) == 0 {
			provider.OAuth.Scopes = []string{"openid", "email", "profile", "offline_access"}
		}
		if provider.OAuth.ExtraAuthParams == nil {
			provider.OAuth.ExtraAuthParams = map[string]string{}
		}
		for key, value := range map[string]string{"prompt": "login", "id_token_add_organizations": "true", "codex_cli_simplified_flow": "true"} {
			if _, exists := provider.OAuth.ExtraAuthParams[key]; !exists {
				provider.OAuth.ExtraAuthParams[key] = value
			}
		}
		if provider.OAuth.ExtraRefreshParams == nil {
			provider.OAuth.ExtraRefreshParams = map[string]string{"scope": "openid profile email"}
		}
		provider.OAuth.ListenForCallback = true
		if provider.OAuth.RefreshSafetyWindow == "" {
			provider.OAuth.RefreshSafetyWindow = "120h"
		}
	case "claude":
		if provider.Name == "" {
			provider.Name = "Anthropic Claude"
		}
		if provider.BaseURL == "" {
			provider.BaseURL = "https://api.anthropic.com"
		}
		if provider.OAuth == nil {
			provider.OAuth = &OAuthConfig{}
		}
		setOAuthDefault(provider.OAuth, "https://claude.ai/oauth/authorize", "https://api.anthropic.com/v1/oauth/token", "9d1c250a-e61b-44d9-88ed-5944d1962f5e", "")
		NormalizeClaudeOAuth(provider.OAuth)
		provider.OAuth.TokenRequestFormat = "json"
		provider.OAuth.IncludeStateInToken = true
		provider.OAuth.ListenForCallback = false
		if provider.OAuth.RefreshSafetyWindow == "" {
			provider.OAuth.RefreshSafetyWindow = "4h"
		}
	case "kimi":
		if provider.Name == "" {
			provider.Name = "Kimi Code"
		}
		if provider.BaseURL == "" {
			provider.BaseURL = "https://api.kimi.com/coding"
		}
		if provider.OAuth == nil {
			provider.OAuth = &OAuthConfig{}
		}
		setOAuthDefault(provider.OAuth, "", "https://auth.kimi.com/api/oauth/token", "17e5f671-d194-4dfb-9706-5516cb48c098", "")
		if provider.OAuth.DeviceCodeURL == "" {
			provider.OAuth.DeviceCodeURL = "https://auth.kimi.com/api/oauth/device_authorization"
		}
		provider.OAuth.DeviceFlow = "rfc8628"
		provider.OAuth.DeviceRequestFormat = "form"
		if provider.OAuth.RefreshSafetyWindow == "" {
			provider.OAuth.RefreshSafetyWindow = "5m"
		}
	case "xai":
		if provider.Name == "" {
			provider.Name = "xAI Grok"
		}
		if provider.BaseURL == "" {
			if provider.ID == "xai-api" {
				provider.BaseURL = "https://api.x.ai/v1"
				if provider.Name == "xAI Grok" {
					provider.Name = "xAI Developer API"
				}
			} else {
				provider.BaseURL = "https://cli-chat-proxy.grok.com/v1"
			}
		}
		if provider.OAuth == nil {
			provider.OAuth = &OAuthConfig{}
		}
		if provider.OAuth.DiscoveryURL == "" {
			provider.OAuth.DiscoveryURL = "https://auth.x.ai/.well-known/openid-configuration"
		}
		if provider.OAuth.ClientID == "" && provider.OAuth.ClientIDEnv == "" {
			provider.OAuth.ClientID = "b1a00492-073a-47ea-816f-4c329264a828"
		}
		if len(provider.OAuth.Scopes) == 0 {
			provider.OAuth.Scopes = []string{"openid", "profile", "email", "offline_access", "grok-cli:access", "api:access"}
		}
		provider.OAuth.DeviceFlow = "rfc8628"
		provider.OAuth.DeviceRequestFormat = "form"
		if provider.OAuth.RefreshSafetyWindow == "" {
			provider.OAuth.RefreshSafetyWindow = "5m"
		}
		if provider.OAuth.ExtraAuthParams == nil {
			provider.OAuth.ExtraAuthParams = map[string]string{}
		}
		if _, exists := provider.OAuth.ExtraAuthParams["referrer"]; !exists {
			provider.OAuth.ExtraAuthParams["referrer"] = "tproxy"
		}
	case "grok-web":
		if provider.Name == "" {
			provider.Name = "Grok Web (Subscription)"
		}
		if provider.BaseURL == "" {
			provider.BaseURL = "https://grok.com/rest/app-chat/conversations/new"
		}
	case "perplexity-web":
		if provider.Name == "" {
			provider.Name = "Perplexity Web (Pro/Max)"
		}
		if provider.BaseURL == "" {
			provider.BaseURL = "https://www.perplexity.ai/rest/sse/perplexity_ask"
		}
	case "copilot":
		if provider.Name == "" {
			provider.Name = "GitHub Copilot"
		}
		if provider.BaseURL == "" {
			provider.BaseURL = "https://api.githubcopilot.com"
		}
		if provider.OAuth == nil {
			provider.OAuth = &OAuthConfig{}
		}
		setOAuthDefault(provider.OAuth, "", "https://github.com/login/oauth/access_token", "Iv1.b507a08c87ecfe98", "")
		if provider.OAuth.DeviceCodeURL == "" {
			provider.OAuth.DeviceCodeURL = "https://github.com/login/device/code"
		}
		provider.OAuth.DeviceFlow = "rfc8628"
		provider.OAuth.DeviceRequestFormat = "form"
		if len(provider.OAuth.Scopes) == 0 {
			provider.OAuth.Scopes = []string{"read:user"}
		}
		if provider.OAuth.RefreshSafetyWindow == "" {
			provider.OAuth.RefreshSafetyWindow = "12h"
		}
	case "qwen":
		if provider.Name == "" {
			provider.Name = "Qwen Code"
		}
		if provider.BaseURL == "" {
			provider.BaseURL = "https://portal.qwen.ai/v1"
		}
		if provider.OAuth == nil {
			provider.OAuth = &OAuthConfig{}
		}
		setOAuthDefault(provider.OAuth, "", "https://chat.qwen.ai/api/v1/oauth2/token", "f0304373b74a44d2b584a3fb70ca9e56", "")
		if provider.OAuth.DeviceCodeURL == "" {
			provider.OAuth.DeviceCodeURL = "https://chat.qwen.ai/api/v1/oauth2/device/code"
		}
		provider.OAuth.DeviceFlow = "rfc8628"
		provider.OAuth.DevicePKCE = true
		provider.OAuth.DeviceRequestFormat = "form"
		if len(provider.OAuth.Scopes) == 0 {
			provider.OAuth.Scopes = []string{"openid", "profile", "email", "model.completion"}
		}
		if provider.OAuth.RefreshSafetyWindow == "" {
			provider.OAuth.RefreshSafetyWindow = "20m"
		}
	case "cline":
		applyClineProviderDefaults(provider, "Cline")
	case "clinepass":
		applyClineProviderDefaults(provider, "ClinePass")
	case "kiro":
		if provider.Name == "" {
			provider.Name = "Kiro AI"
		}
		if provider.BaseURL == "" {
			provider.BaseURL = "https://runtime.us-east-1.kiro.dev"
		}
		if provider.OAuth == nil {
			provider.OAuth = &OAuthConfig{}
		}
		provider.OAuth.DeviceFlow = "kiro"
		if provider.OAuth.RefreshSafetyWindow == "" {
			provider.OAuth.RefreshSafetyWindow = "5m"
		}
	case "iflow":
		if provider.Name == "" {
			provider.Name = "iFlow AI"
		}
		if provider.BaseURL == "" {
			provider.BaseURL = "https://apis.iflow.cn/v1"
		}
		if len(provider.Headers) == 0 {
			provider.Headers = map[string]string{"User-Agent": "iFlow-Cli"}
		}
		if provider.OAuth == nil {
			provider.OAuth = &OAuthConfig{}
		}
		setOAuthDefault(provider.OAuth, "https://iflow.cn/oauth", "https://iflow.cn/oauth/token", "10009311001", "")
		if provider.OAuth.UserInfoURL == "" {
			provider.OAuth.UserInfoURL = "https://iflow.cn/api/oauth/getUserInfo"
		}
		provider.OAuth.ListenForCallback = true
		if provider.OAuth.RefreshSafetyWindow == "" {
			provider.OAuth.RefreshSafetyWindow = "24h"
		}
	case "codebuddy-cn":
		if provider.Name == "" {
			provider.Name = "CodeBuddy CN"
		}
		if provider.BaseURL == "" {
			provider.BaseURL = "https://copilot.tencent.com/v2"
		}
		if len(provider.Headers) == 0 {
			provider.Headers = map[string]string{
				"User-Agent":          "CLI/2.108.1 CodeBuddy/2.108.1",
				"X-Product":           "SaaS",
				"X-IDE-Type":          "CLI",
				"X-IDE-Name":          "CLI",
				"x-requested-with":    "XMLHttpRequest",
				"x-codebuddy-request": "1",
			}
		}
		if provider.OAuth == nil {
			provider.OAuth = &OAuthConfig{}
		}
		if provider.OAuth.DeviceCodeURL == "" {
			provider.OAuth.DeviceCodeURL = "https://copilot.tencent.com/v2/plugin/auth/state"
		}
		if provider.OAuth.TokenURL == "" {
			provider.OAuth.TokenURL = "https://copilot.tencent.com/v2/plugin/auth/token"
		}
		if provider.OAuth.RefreshURL == "" {
			provider.OAuth.RefreshURL = "https://copilot.tencent.com/v2/plugin/auth/token/refresh"
		}
		provider.OAuth.DeviceFlow = "codebuddy-cn"
		if provider.OAuth.RefreshSafetyWindow == "" {
			provider.OAuth.RefreshSafetyWindow = "12h"
		}
	case "kilocode":
		if provider.Name == "" {
			provider.Name = "Kilo Code"
		}
		if provider.BaseURL == "" {
			provider.BaseURL = "https://api.kilo.ai/api/openrouter"
		}
		if provider.OAuth == nil {
			provider.OAuth = &OAuthConfig{}
		}
		if provider.OAuth.DeviceCodeURL == "" {
			provider.OAuth.DeviceCodeURL = "https://api.kilo.ai/api/device-auth/codes"
		}
		provider.OAuth.DeviceFlow = "kilocode"
	case "gitlab":
		if provider.Name == "" {
			provider.Name = "GitLab Duo"
		}
		if provider.BaseURL == "" {
			provider.BaseURL = "https://gitlab.com/api/v4"
		}
		if provider.OAuth == nil {
			provider.OAuth = &OAuthConfig{}
		}
		if provider.OAuth.AuthorizationURL == "" {
			provider.OAuth.AuthorizationURL = "https://gitlab.com/oauth/authorize"
		}
		if provider.OAuth.TokenURL == "" {
			provider.OAuth.TokenURL = "https://gitlab.com/oauth/token"
		}
		if len(provider.OAuth.Scopes) == 0 {
			provider.OAuth.Scopes = []string{"api", "read_user"}
		}
		provider.OAuth.ListenForCallback = true
		if provider.OAuth.RefreshSafetyWindow == "" {
			provider.OAuth.RefreshSafetyWindow = "5m"
		}
	case "kimchi":
		if provider.Name == "" {
			provider.Name = "Kimchi"
		}
		if provider.BaseURL == "" {
			provider.BaseURL = "https://llm.kimchi.dev/openai/v1"
		}
		if len(provider.Headers) == 0 {
			provider.Headers = map[string]string{"User-Agent": "kimchi/0.1.50"}
		}
		if provider.OAuth == nil {
			provider.OAuth = &OAuthConfig{}
		}
		if provider.OAuth.AuthorizationURL == "" {
			provider.OAuth.AuthorizationURL = "https://app.kimchi.dev/cli-auth"
		}
		if provider.OAuth.UserInfoURL == "" {
			provider.OAuth.UserInfoURL = "https://app.kimchi.dev/api/v1/me"
		}
		provider.OAuth.ListenForCallback = true
	case "cursor":
		if provider.Name == "" {
			provider.Name = "Cursor IDE"
		}
		if provider.BaseURL == "" {
			provider.BaseURL = "https://api2.cursor.sh"
		}
	case "qoder":
		if provider.Name == "" {
			provider.Name = "Qoder"
		}
		if provider.BaseURL == "" {
			provider.BaseURL = "https://api3.qoder.sh"
		}
		if provider.OAuth == nil {
			provider.OAuth = &OAuthConfig{}
		}
		provider.OAuth.DeviceFlow = "qoder"
		if provider.OAuth.RefreshSafetyWindow == "" {
			provider.OAuth.RefreshSafetyWindow = "720h"
		}
	case "vertex-partner":
		if provider.Name == "" {
			provider.Name = "Vertex Partner"
		}
		if provider.BaseURL == "" {
			provider.BaseURL = "https://aiplatform.googleapis.com/v1"
		}
	case "antigravity":
		if provider.Name == "" {
			provider.Name = "Google Antigravity"
		}
		if provider.BaseURL == "" {
			provider.BaseURL = "https://cloudcode-pa.googleapis.com"
		}
		if provider.OAuth == nil {
			provider.OAuth = &OAuthConfig{}
		}
		setOAuthDefault(provider.OAuth, "https://accounts.google.com/o/oauth2/v2/auth", "https://oauth2.googleapis.com/token", "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com", "http://localhost:51121/oauth-callback")
		if provider.OAuth.UserInfoURL == "" {
			provider.OAuth.UserInfoURL = "https://www.googleapis.com/oauth2/v2/userinfo?alt=json"
		}
		if provider.OAuth.ClientSecretEnv == "" {
			provider.OAuth.ClientSecretEnv = "TPROXY_ANTIGRAVITY_CLIENT_SECRET"
		}
		provider.OAuth.RequireClientSecret = true
		if len(provider.OAuth.Scopes) == 0 {
			provider.OAuth.Scopes = []string{
				"https://www.googleapis.com/auth/cloud-platform",
				"https://www.googleapis.com/auth/userinfo.email",
				"https://www.googleapis.com/auth/userinfo.profile",
				"https://www.googleapis.com/auth/cclog",
				"https://www.googleapis.com/auth/experimentsandconfigs",
			}
		}
		if provider.OAuth.ExtraAuthParams == nil {
			provider.OAuth.ExtraAuthParams = map[string]string{}
		}
		for key, value := range map[string]string{"access_type": "offline", "prompt": "consent"} {
			if _, exists := provider.OAuth.ExtraAuthParams[key]; !exists {
				provider.OAuth.ExtraAuthParams[key] = value
			}
		}
		provider.OAuth.ListenForCallback = true
		if provider.OAuth.RefreshSafetyWindow == "" {
			provider.OAuth.RefreshSafetyWindow = "5m"
		}
	case "tavily":
		if provider.Name == "" {
			provider.Name = "Tavily Search"
		}
		if provider.BaseURL == "" {
			provider.BaseURL = "https://api.tavily.com"
		}
	case "elevenlabs":
		if provider.Name == "" {
			provider.Name = "ElevenLabs Audio"
		}
		if provider.BaseURL == "" {
			provider.BaseURL = "https://api.elevenlabs.io"
		}
	case "image":
		if provider.Name == "" {
			provider.Name = "Image generation provider"
		}
	case "video":
		if provider.Name == "" {
			provider.Name = "Video generation provider"
		}
	case "vertex":
		if provider.Name == "" {
			provider.Name = "Google Vertex AI"
		}
		if provider.BaseURL == "" {
			provider.BaseURL = "https://aiplatform.googleapis.com"
		}
	}
}

var claudeOAuthScopes = []string{
	"org:create_api_key",
	"user:profile",
	"user:inference",
}

// ClaudeOAuthScopes returns the canonical Claude OAuth scopes (9router-compatible).
func ClaudeOAuthScopes() []string {
	return append([]string(nil), claudeOAuthScopes...)
}

// NormalizeClaudeOAuth aligns stored Claude OAuth settings with the dashboard browser flow.
func NormalizeClaudeOAuth(oauth *OAuthConfig) {
	if oauth == nil {
		return
	}
	if len(oauth.Scopes) == 0 || claudeOAuthScopesDiffer(oauth.Scopes) {
		oauth.Scopes = ClaudeOAuthScopes()
	}
	if oauth.ExtraAuthParams == nil {
		oauth.ExtraAuthParams = map[string]string{}
	}
	if _, exists := oauth.ExtraAuthParams["code"]; !exists {
		oauth.ExtraAuthParams["code"] = "true"
	}
	if strings.TrimSpace(oauth.RedirectURL) == "http://localhost:54545/callback" {
		oauth.RedirectURL = ""
	}
	oauth.ListenForCallback = false
}

func claudeOAuthScopesDiffer(scopes []string) bool {
	canonical := claudeOAuthScopes
	if len(scopes) != len(canonical) {
		return true
	}
	for i := range scopes {
		if scopes[i] != canonical[i] {
			return true
		}
	}
	return false
}

// UsesLegacyClaudeOAuthScopes reports non-canonical Claude OAuth scope sets stored in older configs.
func UsesLegacyClaudeOAuthScopes(scopes []string) bool {
	return claudeOAuthScopesDiffer(scopes)
}

func setOAuthDefault(oauth *OAuthConfig, authorizationURL, tokenURL, clientID, redirectURL string) {
	if oauth.AuthorizationURL == "" {
		oauth.AuthorizationURL = authorizationURL
	}
	if oauth.TokenURL == "" {
		oauth.TokenURL = tokenURL
	}
	if oauth.ClientID == "" && oauth.ClientIDEnv == "" {
		oauth.ClientID = clientID
	}
	if oauth.RedirectURL == "" {
		oauth.RedirectURL = redirectURL
	}
	if oauth.TokenRequestFormat == "" {
		oauth.TokenRequestFormat = "form"
	}
}

func (cfg *Config) Validate() error {
	applyDefaults(cfg)
	if err := validateServerBinding(cfg.Server); err != nil {
		return err
	}
	if err := validateLimitPolicy("global", cfg.Limits); err != nil {
		return err
	}
	teamIDs := make(map[string]struct{}, len(cfg.Teams))
	for _, team := range cfg.Teams {
		if strings.TrimSpace(team.ID) == "" {
			return errors.New("team policy id is required")
		}
		if _, exists := teamIDs[team.ID]; exists {
			return fmt.Errorf("duplicate team policy %q", team.ID)
		}
		teamIDs[team.ID] = struct{}{}
		if err := validateLimitPolicy("team "+team.ID, team.Limits); err != nil {
			return err
		}
	}
	if cfg.Server.AllowRemoteManagement && Env(cfg.Security.ManagementSecretEnv) == "" {
		return errors.New("remote management requires a configured management secret")
	}
	for _, provider := range cfg.Providers {
		if err := validateLimitPolicy("provider "+provider.ID, provider.Limits); err != nil {
			return err
		}
		if provider.Type == "plugin-http" && !cfg.Security.PluginsEnabled {
			return fmt.Errorf("provider %q uses plugin-http but security.plugins-enabled is false", provider.ID)
		}
	}
	if cfg.Database.Driver != "sqlite" {
		return fmt.Errorf("unsupported database driver %q", cfg.Database.Driver)
	}
	strategy := cfg.Routing.Strategy
	if strategy == "" {
		strategy = "round-robin"
	}
	if !routing.IsValidStrategy(strategy) {
		return fmt.Errorf("unsupported routing strategy %q", strategy)
	}
	for providerID, providerStrategy := range cfg.Routing.ProviderStrategies {
		if providerStrategy.Strategy != "" && !routing.IsValidStrategy(providerStrategy.Strategy) {
			return fmt.Errorf("unsupported routing strategy %q for provider %q", providerStrategy.Strategy, providerID)
		}
	}
	ttl := cfg.Routing.SessionAffinityTTL
	if ttl == "" {
		ttl = "1h"
	}
	if _, err := time.ParseDuration(ttl); err != nil {
		return fmt.Errorf("invalid session affinity ttl: %w", err)
	}
	for name, value := range map[string]string{"usage-events": cfg.Retention.UsageEvents, "request-logs": cfg.Retention.RequestLogs, "audit-events": cfg.Retention.AuditEvents, "media-jobs": cfg.Retention.MediaJobs, "oauth-sessions": cfg.Retention.OAuthSessions, "cleanup-interval": cfg.Retention.CleanupInterval} {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			return fmt.Errorf("invalid retention %s duration %q", name, value)
		}
	}
	proxyPoolIDs := make(map[string]struct{}, len(cfg.ProxyPools))
	for _, pool := range cfg.ProxyPools {
		if strings.TrimSpace(pool.ID) == "" {
			return errors.New("proxy pool id is required")
		}
		if _, exists := proxyPoolIDs[pool.ID]; exists {
			return fmt.Errorf("duplicate proxy pool id %q", pool.ID)
		}
		proxyPoolIDs[pool.ID] = struct{}{}
		proxyURL := pool.URL
		if proxyURL == "" && pool.URLEnv != "" {
			proxyURL = Env(pool.URLEnv)
		}
		if err := validateProxyURL(proxyURL); err != nil {
			return fmt.Errorf("proxy pool %q: %w", pool.ID, err)
		}
	}
	providerIDs := make(map[string]struct{}, len(cfg.Providers))
	for _, provider := range cfg.Providers {
		if strings.TrimSpace(provider.ID) == "" {
			return errors.New("provider id is required")
		}
		if _, exists := providerIDs[provider.ID]; exists {
			return fmt.Errorf("duplicate provider id %q", provider.ID)
		}
		providerIDs[provider.ID] = struct{}{}
		for _, poolID := range provider.ProxyPools {
			if _, exists := proxyPoolIDs[poolID]; !exists {
				return fmt.Errorf("provider %q references unknown proxy pool %q", provider.ID, poolID)
			}
		}
		switch provider.Type {
		case "openai-compatible", "anthropic-compatible", "gemini", "vertex", "vertex-partner", "ollama", "codex", "claude", "kimi", "xai", "grok-web", "perplexity-web", "antigravity", "tavily", "elevenlabs", "image", "video", "plugin-http", "copilot", "qwen", "kiro", "qoder", "cursor", "cline", "clinepass", "iflow", "codebuddy-cn", "kilocode", "gitlab", "kimchi":
		default:
			return fmt.Errorf("provider %q has unsupported type %q", provider.ID, provider.Type)
		}
		if provider.OAuth != nil {
			if err := validateOAuth(provider.ID, provider.Type, provider.OAuth); err != nil {
				return err
			}
		}
		if provider.Type == "vertex" && !strings.Contains(provider.BaseURL, "/projects/") && !strings.Contains(provider.BaseURL, "/publishers/") {
			if strings.TrimSpace(fmt.Sprint(provider.Config["project"])) == "" {
				return fmt.Errorf("provider %q vertex config.project is required when base-url is not project scoped", provider.ID)
			}
		}
		credentialIDs := make(map[string]struct{}, len(provider.Credentials))
		for _, credential := range provider.Credentials {
			if strings.TrimSpace(credential.ID) == "" {
				return fmt.Errorf("provider %q credential id is required", provider.ID)
			}
			if _, exists := credentialIDs[credential.ID]; exists {
				return fmt.Errorf("provider %q has duplicate credential id %q", provider.ID, credential.ID)
			}
			credentialIDs[credential.ID] = struct{}{}
			for _, poolID := range credential.ProxyPools {
				if _, exists := proxyPoolIDs[poolID]; !exists {
					return fmt.Errorf("provider %q credential %q references unknown proxy pool %q", provider.ID, credential.ID, poolID)
				}
			}
			switch credential.AuthType {
			case "", "api_key", "oauth", "service_account", "none":
			default:
				return fmt.Errorf("provider %q credential %q has unsupported auth type %q", provider.ID, credential.ID, credential.AuthType)
			}
			if credential.AuthType == "oauth" && provider.OAuth == nil {
				return fmt.Errorf("provider %q credential %q requires oauth configuration", provider.ID, credential.ID)
			}
		}
	}
	modelIDs := make(map[string]struct{}, len(cfg.Models))
	aliasIDs := make(map[string]string)
	for _, model := range cfg.Models {
		if strings.TrimSpace(model.ID) == "" {
			return errors.New("public model id is required")
		}
		if _, exists := modelIDs[model.ID]; exists {
			return fmt.Errorf("duplicate public model id %q", model.ID)
		}
		modelIDs[model.ID] = struct{}{}
		for _, alias := range model.Aliases {
			if owner, exists := aliasIDs[alias]; exists && owner != model.ID {
				return fmt.Errorf("alias %q is shared by %q and %q", alias, owner, model.ID)
			}
			aliasIDs[alias] = model.ID
		}
		for _, route := range model.Routes {
			if _, exists := providerIDs[route.Provider]; !exists {
				return fmt.Errorf("model %q route references unknown provider %q", model.ID, route.Provider)
			}
			if strings.TrimSpace(route.UpstreamModel) == "" {
				return fmt.Errorf("model %q route upstream model is required", model.ID)
			}
			if route.Pricing != nil && (route.Pricing.InputPerMillion < 0 || route.Pricing.OutputPerMillion < 0 || route.Pricing.ReasoningPerMillion < 0 || route.Pricing.Request < 0) {
				return fmt.Errorf("model %q route pricing values must be non-negative", model.ID)
			}
		}
	}
	for _, key := range cfg.ClientAPIKeys {
		if err := validateLimitPolicy("client API key "+key.ID, key.Policy.Limits); err != nil {
			return err
		}
	}
	comboIDs := make(map[string]struct{}, len(cfg.Combos))
	for _, combo := range cfg.Combos {
		if strings.TrimSpace(combo.ID) == "" {
			return errors.New("combo id is required")
		}
		if _, exists := modelIDs[combo.ID]; exists {
			return fmt.Errorf("combo %q conflicts with a public model", combo.ID)
		}
		if _, exists := comboIDs[combo.ID]; exists {
			return fmt.Errorf("duplicate combo id %q", combo.ID)
		}
		comboIDs[combo.ID] = struct{}{}
		if len(combo.Items) == 0 {
			return fmt.Errorf("combo %q must contain at least one item", combo.ID)
		}
		for _, item := range combo.Items {
			if item.PublicModelID == "" {
				return fmt.Errorf("combo %q item public-model is required", combo.ID)
			}
			if _, exists := modelIDs[item.PublicModelID]; !exists {
				return fmt.Errorf("combo %q references unknown public model %q", combo.ID, item.PublicModelID)
			}
		}
	}
	return nil
}

func validateServerBinding(server ServerConfig) error {
	host := strings.TrimSpace(server.Host)
	if host == "" {
		return errors.New("server host is required")
	}
	if strings.Contains(host, "://") || strings.ContainsAny(host, "/?# \t\r\n") {
		return fmt.Errorf("server host %q must not include a scheme, path or port", host)
	}
	if strings.Contains(host, ":") && net.ParseIP(host) == nil {
		return fmt.Errorf("server host %q must not include a port", host)
	}
	if server.Port < 1 || server.Port > 65535 {
		return fmt.Errorf("server port %d must be between 1 and 65535", server.Port)
	}
	return nil
}

func validateLimitPolicy(scope string, limits LimitPolicy) error {
	if limits.RequestsPerMinute < 0 || limits.ConcurrentStreams < 0 || limits.MaxInputBytes < 0 || limits.MaxOutputTokens < 0 || limits.MediaJobs < 0 || limits.BudgetUSDPerDay < 0 {
		return fmt.Errorf("%s limits must be non-negative", scope)
	}
	return nil
}

func validateProxyURL(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("proxy URL is required")
	}
	if strings.EqualFold(value, "direct") || strings.EqualFold(value, "none") {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return errors.New("proxy URL must include a scheme and host")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "socks5", "socks5h":
		return nil
	default:
		return fmt.Errorf("unsupported proxy scheme %q", parsed.Scheme)
	}
}

func validateOAuth(providerID, providerType string, oauth *OAuthConfig) error {
	hasTokenEndpoint := strings.TrimSpace(oauth.TokenURL) != "" || strings.TrimSpace(oauth.DiscoveryURL) != ""
	if !hasTokenEndpoint && providerType != "kimchi" && !isCustomDeviceFlow(oauth.DeviceFlow) {
		return fmt.Errorf("provider %q oauth token-url or discovery-url is required", providerID)
	}
	if oauthRequiresClientID(providerType, oauth) && strings.TrimSpace(oauth.ClientIDEnv) == "" && strings.TrimSpace(oauth.ClientID) == "" {
		return fmt.Errorf("provider %q oauth client-id or client-id-env is required", providerID)
	}
	if oauth.RequireClientSecret && strings.TrimSpace(oauth.ClientSecretEnv) == "" {
		return fmt.Errorf("provider %q oauth client-secret-env is required", providerID)
	}
	if strings.TrimSpace(oauth.AuthorizationURL) == "" && strings.TrimSpace(oauth.DeviceCodeURL) == "" && strings.TrimSpace(oauth.DiscoveryURL) == "" {
		if !isCustomOAuthProviderType(providerType) {
			return fmt.Errorf("provider %q oauth requires authorization-url, device-code-url or discovery-url", providerID)
		}
	}
	for name, value := range map[string]string{
		"discovery-url":                oauth.DiscoveryURL,
		"authorization-url":            oauth.AuthorizationURL,
		"token-url":                    oauth.TokenURL,
		"user-info-url":                oauth.UserInfoURL,
		"device-code-url":              oauth.DeviceCodeURL,
		"device-token-url":             oauth.DeviceTokenURL,
		"device-verification-url":      oauth.DeviceVerificationURL,
		"device-exchange-redirect-url": oauth.DeviceExchangeRedirectURL,
		"redirect-url":                 oauth.RedirectURL,
	} {
		if strings.TrimSpace(value) == "" {
			continue
		}
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("provider %q oauth %s must be an absolute http(s) URL", providerID, name)
		}
	}
	if oauth.RefreshSafetyWindow != "" {
		window, err := time.ParseDuration(oauth.RefreshSafetyWindow)
		if err != nil || window < 0 {
			return fmt.Errorf("provider %q oauth refresh-safety-window is invalid", providerID)
		}
	}
	format := strings.ToLower(strings.TrimSpace(oauth.TokenRequestFormat))
	if format != "" && format != "form" && format != "json" {
		return fmt.Errorf("provider %q oauth token-request-format must be form or json", providerID)
	}
	deviceFormat := strings.ToLower(strings.TrimSpace(oauth.DeviceRequestFormat))
	if deviceFormat != "" && deviceFormat != "form" && deviceFormat != "json" {
		return fmt.Errorf("provider %q oauth device-request-format must be form or json", providerID)
	}
	deviceFlow := strings.ToLower(strings.TrimSpace(oauth.DeviceFlow))
	if deviceFlow != "" && !isSupportedDeviceFlow(deviceFlow) {
		return fmt.Errorf("provider %q oauth device-flow is unsupported", providerID)
	}
	return nil
}

func isCustomDeviceFlow(flow string) bool {
	switch strings.ToLower(strings.TrimSpace(flow)) {
	case "qoder", "kilocode", "codebuddy-cn", "kiro":
		return true
	default:
		return false
	}
}

func isSupportedDeviceFlow(flow string) bool {
	switch flow {
	case "rfc8628", "codex", "qoder", "kilocode", "codebuddy-cn", "kiro":
		return true
	default:
		return false
	}
}

func oauthRequiresClientID(providerType string, oauth *OAuthConfig) bool {
	switch providerType {
	case "cline", "clinepass", "kimchi", "kilocode", "codebuddy-cn", "kiro":
		return false
	case "gitlab":
		return false
	}
	return !isCustomDeviceFlow(oauth.DeviceFlow)
}

func isCustomOAuthProviderType(providerType string) bool {
	switch providerType {
	case "qoder", "kilocode", "codebuddy-cn", "kiro", "kimchi", "iflow", "gitlab":
		return true
	default:
		return false
	}
}

func Env(name string) string {
	if name == "" {
		return ""
	}
	return strings.TrimSpace(os.Getenv(name))
}

// APIKeySecretCandidates returns plaintext client API keys resolvable from config key-env and common env vars.
func APIKeySecretCandidates(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, key := range cfg.ClientAPIKeys {
		add(Env(key.KeyEnv))
	}
	add(Env("TPROXY_API_KEY"))
	return out
}

func Save(path string, cfg *Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err = os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// RemoveModel deletes a public model from the declarative config file and scrubs combo references.
func RemoveModel(path, modelID string) (*Config, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil, errors.New("model id is required")
	}

	kept := make([]PublicModelConfig, 0, len(cfg.Models))
	found := false
	for _, model := range cfg.Models {
		if model.ID == modelID {
			found = true
			continue
		}
		kept = append(kept, model)
	}
	cfg.Models = kept

	for index := range cfg.Combos {
		items := make([]ComboItemConfig, 0, len(cfg.Combos[index].Items))
		for _, item := range cfg.Combos[index].Items {
			if item.PublicModelID == modelID {
				found = true
				continue
			}
			items = append(items, item)
		}
		cfg.Combos[index].Items = items
	}

	keptCombos := make([]ComboConfig, 0, len(cfg.Combos))
	for _, combo := range cfg.Combos {
		if len(combo.Items) == 0 {
			if combo.ID != "" {
				found = true
			}
			continue
		}
		keptCombos = append(keptCombos, combo)
	}
	cfg.Combos = keptCombos

	if !found {
		return cfg, nil
	}
	if err := Save(path, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
