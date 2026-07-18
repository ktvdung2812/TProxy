package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/security"
	"github.com/tproxy/tproxy/internal/store"
	"gopkg.in/yaml.v3"
)

func TestImportAuthSeedsConfiguredProvidersIntoFreshDatabase(t *testing.T) {
	masterKey, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TPROXY_IMPORT_TEST_MASTER_KEY", masterKey)
	tempDir := t.TempDir()
	providerConfig := config.ProviderConfig{
		ID:      "oauth-provider",
		Type:    "openai-compatible",
		BaseURL: "https://provider.example",
		Enabled: true,
		OAuth: &config.OAuthConfig{
			AuthorizationURL: "https://provider.example/oauth/authorize",
			TokenURL:         "https://provider.example/oauth/token",
			ClientID:         "test-client",
		},
	}
	encryptor, err := security.NewEncryptor(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	sourceStore, err := store.OpenSQLite(filepath.Join(tempDir, "source.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	if err = sourceStore.Seed(context.Background(), &config.Config{Providers: []config.ProviderConfig{providerConfig}}); err != nil {
		_ = sourceStore.Close()
		t.Fatal(err)
	}
	if err = sourceStore.SaveOAuthCredential(context.Background(), providerConfig.ID, "restored-account", "Restored", "restored@example.test", store.OAuthToken{
		AccessToken:  "restored-access",
		RefreshToken: "restored-refresh",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(time.Hour),
	}); err != nil {
		_ = sourceStore.Close()
		t.Fatal(err)
	}
	bundlePath := filepath.Join(tempDir, "auth-bundle.json")
	if err = sourceStore.ExportAuthFile(context.Background(), bundlePath); err != nil {
		_ = sourceStore.Close()
		t.Fatal(err)
	}
	if err = sourceStore.Close(); err != nil {
		t.Fatal(err)
	}

	targetDB := filepath.Join(tempDir, "target.db")
	configPath := filepath.Join(tempDir, "config.yaml")
	configData, err := yaml.Marshal(config.Config{
		Database:  config.DatabaseConfig{Driver: "sqlite", DSN: targetDB},
		Security:  config.SecurityConfig{MasterKeyEnv: "TPROXY_IMPORT_TEST_MASTER_KEY"},
		Providers: []config.ProviderConfig{providerConfig},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath, configData, 0o600); err != nil {
		t.Fatal(err)
	}

	previousArgs := os.Args
	previousFlags := flag.CommandLine
	os.Args = []string{"tproxy", "--config", configPath, "--import-auth", bundlePath}
	flag.CommandLine = flag.NewFlagSet("tproxy-test", flag.ContinueOnError)
	t.Cleanup(func() {
		os.Args = previousArgs
		flag.CommandLine = previousFlags
	})
	if err = run(); err != nil {
		t.Fatalf("fresh database auth import failed: %v", err)
	}

	targetStore, err := store.OpenSQLite(targetDB, encryptor)
	if err != nil {
		t.Fatal(err)
	}
	defer targetStore.Close()
	credential, err := targetStore.CredentialByID(context.Background(), "restored-account")
	if err != nil {
		t.Fatal(err)
	}
	if credential.OAuthToken == nil || credential.OAuthToken.AccessToken != "restored-access" || credential.OAuthToken.RefreshToken != "restored-refresh" {
		t.Fatalf("restored credential token=%+v", credential.OAuthToken)
	}
}
