package config

import (
	"path/filepath"
	"testing"
)

func TestValidateRejectsInvalidServerBinding(t *testing.T) {
	cases := []struct {
		name string
		cfg  ServerConfig
	}{
		{name: "port below range", cfg: ServerConfig{Host: "127.0.0.1", Port: -1}},
		{name: "port above range", cfg: ServerConfig{Host: "127.0.0.1", Port: 65536}},
		{name: "host contains scheme", cfg: ServerConfig{Host: "http://0.0.0.0", Port: 28120}},
		{name: "host includes port", cfg: ServerConfig{Host: "0.0.0.0:28120", Port: 28120}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			cfg := Config{Server: test.cfg}
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected invalid server binding to be rejected")
			}
		})
	}
}

func TestValidateAllowsIPv6ServerBinding(t *testing.T) {
	cfg := Config{Server: ServerConfig{Host: "  ::1  ", Port: 28120}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid IPv6 bind rejected: %v", err)
	}
	if cfg.Server.Host != "::1" {
		t.Fatalf("server host was not normalized: %q", cfg.Server.Host)
	}
}

func TestLoadConfigExampleDoesNotRequireEncryptedBootstrapSecrets(t *testing.T) {
	for _, name := range []string{"TPROXY_MASTER_KEY", "TPROXY_API_KEY", "OPENAI_API_KEY"} {
		t.Setenv(name, "")
	}
	cfg, err := Load(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ProxyPools) != 0 {
		t.Fatalf("config example bootstraps encrypted proxy pools without a master key: %+v", cfg.ProxyPools)
	}
}

func TestValidateRejectsNonSQLiteDatabase(t *testing.T) {
	cfg := Config{Database: DatabaseConfig{Driver: "postgres"}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected non-SQLite database driver to be rejected")
	}
}

func TestValidateRejectsAliasCollision(t *testing.T) {
	cfg := Config{
		Models: []PublicModelConfig{
			{ID: "first", Aliases: []string{"shared"}},
			{ID: "second", Aliases: []string{"shared"}},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected duplicate alias error")
	}
}

func TestValidateOAuthConfiguration(t *testing.T) {
	cfg := Config{Database: DatabaseConfig{Driver: "sqlite"}, Providers: []ProviderConfig{{
		ID: "oauth", Type: "openai-compatible", Enabled: true,
		OAuth:       &OAuthConfig{AuthorizationURL: "https://login.example/authorize", TokenURL: "https://login.example/token", ClientIDEnv: "OAUTH_CLIENT_ID", RefreshSafetyWindow: "90s"},
		Credentials: []CredentialConfig{{ID: "account", AuthType: "oauth"}},
	}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.Providers[0].OAuth.TokenURL = "not-a-url"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid OAuth URL error")
	}
}

func TestValidateProxyPoolsAndBindings(t *testing.T) {
	enabled := true
	cfg := Config{
		Database:   DatabaseConfig{Driver: "sqlite"},
		ProxyPools: []ProxyPoolConfig{{ID: "egress", URL: "socks5://user:pass@127.0.0.1:1080", Enabled: &enabled}},
		Providers:  []ProviderConfig{{ID: "provider", Type: "openai-compatible", Enabled: true, ProxyPools: []string{"egress"}, Credentials: []CredentialConfig{{ID: "credential", AuthType: "none", ProxyPools: []string{"egress"}}}}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.Providers[0].ProxyPools = []string{"missing"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unknown proxy pool binding to fail validation")
	}
	cfg.Providers[0].ProxyPools = []string{"egress"}
	cfg.ProxyPools[0].URL = "ftp://proxy.example.com"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unsupported proxy scheme to fail validation")
	}
}

func TestProviderDefaultsForCodexAndClaude(t *testing.T) {
	codex := ProviderConfig{ID: "codex", Type: "codex"}
	ApplyProviderDefaults(&codex)
	if codex.BaseURL != "https://chatgpt.com/backend-api/codex" || codex.OAuth == nil || codex.OAuth.ClientID == "" || !codex.OAuth.ListenForCallback {
		t.Fatalf("codex defaults = %+v", codex)
	}
	claude := ProviderConfig{ID: "claude", Type: "claude"}
	ApplyProviderDefaults(&claude)
	if claude.BaseURL != "https://api.anthropic.com" || claude.OAuth == nil || claude.OAuth.TokenRequestFormat != "json" || !claude.OAuth.IncludeStateInToken {
		t.Fatalf("claude defaults = %+v", claude)
	}
}

func TestProviderDefaultsForAntigravity(t *testing.T) {
	provider := ProviderConfig{ID: "antigravity", Type: "antigravity"}
	ApplyProviderDefaults(&provider)
	if provider.Name != "Google Antigravity" || provider.BaseURL != "https://cloudcode-pa.googleapis.com" {
		t.Fatalf("antigravity provider defaults = %+v", provider)
	}
	if provider.OAuth == nil || provider.OAuth.AuthorizationURL != "https://accounts.google.com/o/oauth2/v2/auth" || provider.OAuth.TokenURL != "https://oauth2.googleapis.com/token" {
		t.Fatalf("antigravity OAuth defaults = %+v", provider.OAuth)
	}
	if !provider.OAuth.RequireClientSecret || provider.OAuth.ClientSecretEnv != "TPROXY_ANTIGRAVITY_CLIENT_SECRET" || !provider.OAuth.ListenForCallback {
		t.Fatalf("antigravity OAuth security defaults = %+v", provider.OAuth)
	}
	if provider.OAuth.ExtraAuthParams["access_type"] != "offline" || provider.OAuth.ExtraAuthParams["prompt"] != "consent" || len(provider.OAuth.Scopes) < 5 {
		t.Fatalf("antigravity OAuth parameters = %+v", provider.OAuth)
	}
}

func TestProviderDefaultsForTavily(t *testing.T) {
	provider := ProviderConfig{ID: "search", Type: "tavily"}
	ApplyProviderDefaults(&provider)
	if provider.Name != "Tavily Search" || provider.BaseURL != "https://api.tavily.com" {
		t.Fatalf("tavily defaults = %+v", provider)
	}
}

func TestProviderDefaultsForElevenLabs(t *testing.T) {
	provider := ProviderConfig{ID: "audio", Type: "elevenlabs"}
	ApplyProviderDefaults(&provider)
	if provider.Name != "ElevenLabs Audio" || provider.BaseURL != "https://api.elevenlabs.io" {
		t.Fatalf("ElevenLabs defaults = %+v", provider)
	}
}

func TestPluginProviderRequiresExplicitSecurityOptIn(t *testing.T) {
	cfg := &Config{Providers: []ProviderConfig{{ID: "plugin", Type: "plugin-http", BaseURL: "http://127.0.0.1:9999", Enabled: true}}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected plugin provider to be rejected while disabled")
	}
	cfg.Security.PluginsEnabled = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("plugin provider with opt-in rejected: %v", err)
	}
}
