package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/config"
)

// Antigravity's provider defaults set RequireClientSecret and point at an
// environment variable that a normal installation never sets, so enrolment
// failed at the very first step with "OAuth provider configuration is invalid"
// on a provider the dashboard offered as ready to connect.
func TestAntigravityOAuthStartsWithoutAnEnvironmentSecret(t *testing.T) {
	t.Setenv("TPROXY_ANTIGRAVITY_CLIENT_SECRET", "")

	provider := config.ProviderConfig{ID: "antigravity", Type: "antigravity", Enabled: true}
	config.ApplyProviderDefaults(&provider)
	if !provider.OAuth.RequireClientSecret {
		t.Fatal("expected antigravity to require a client secret")
	}

	manager := &Manager{}
	if secret := manager.clientSecret(*provider.OAuth); secret == "" {
		t.Fatal("no client secret resolved; StartAuthorization would report oauth_configuration_invalid")
	}
}

// An operator-supplied secret still wins, so a private OAuth client can be used.
func TestEnvironmentClientSecretOverridesTheBuiltin(t *testing.T) {
	t.Setenv("TPROXY_ANTIGRAVITY_CLIENT_SECRET", "operator-supplied-secret")

	provider := config.ProviderConfig{ID: "antigravity", Type: "antigravity", Enabled: true}
	config.ApplyProviderDefaults(&provider)

	manager := &Manager{}
	if got := manager.clientSecret(*provider.OAuth); got != "operator-supplied-secret" {
		t.Fatalf("client secret = %q, want the environment value to win", got)
	}
}

// The built-in is tied to its client ID. A provider configured with a different
// OAuth client must not silently borrow Antigravity's secret.
func TestBuiltinSecretIsNotAppliedToOtherClients(t *testing.T) {
	t.Setenv("TPROXY_ANTIGRAVITY_CLIENT_SECRET", "")

	provider := config.ProviderConfig{ID: "antigravity", Type: "antigravity", Enabled: true}
	config.ApplyProviderDefaults(&provider)
	provider.OAuth.ClientID = "999999-someone-elses-client.apps.googleusercontent.com"

	manager := &Manager{}
	if got := manager.clientSecret(*provider.OAuth); got != "" {
		t.Fatalf("client secret = %q, want none for an unknown client ID", got)
	}
}

// The secret must never reach the database or a configuration export.
func TestBuiltinSecretIsNotPersisted(t *testing.T) {
	ctx := context.Background()
	provider := config.ProviderConfig{ID: "antigravity", Type: "antigravity", Enabled: true}
	config.ApplyProviderDefaults(&provider)

	dataStore, _ := newAuthStore(t, &config.Config{Providers: []config.ProviderConfig{provider}})
	exported, err := dataStore.ExportConfig(ctx, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(mustJSON(exported), config.BuiltinOAuthClientSecret(provider.OAuth.ClientID)) {
		t.Fatal("built-in client secret leaked into the exported configuration")
	}

	stored, err := dataStore.Provider(ctx, "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	if stored.OAuth != nil && strings.Contains(mustJSON(stored.OAuth), config.BuiltinOAuthClientSecret(provider.OAuth.ClientID)) {
		t.Fatal("built-in client secret was written to the provider row")
	}
}
