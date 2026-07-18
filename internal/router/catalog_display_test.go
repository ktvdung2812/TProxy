package router

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/pricing"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/security"
	"github.com/tproxy/tproxy/internal/store"
)

func TestCatalogDisplayEntryUsesUpstreamWhenModelsRegistryEnabled(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{ID: "codex-main", Type: "codex", Enabled: true, Credentials: []config.CredentialConfig{{ID: "codex-credential", AuthType: "none"}}}},
		Models: []config.PublicModelConfig{{
			ID: "codex-gpt-5.6-luna", DisplayName: "gpt-5.6-luna", Enabled: true,
			Routes: []config.RouteTargetConfig{{ID: "route", Provider: "codex-main", UpstreamModel: "gpt-5.6-luna", Priority: 10}},
		}},
	}
	dataStore := openTestStore(t, cfg)
	requestRouter := New(dataStore, providers.NewRegistry())
	registry := pricing.NewModelsRegistry(pricing.ModelsRegistryOptions{})
	full, bare, names := pricing.BuildModelsRegistryIndexForTest(map[string]any{
		"openai/gpt-5.6-luna": map[string]any{"id": "openai/gpt-5.6-luna", "name": "GPT-5.6 Luna"},
	})
	registry.SetIndexForTest(full, bare, names)
	requestRouter.SetModelsRegistry(registry)

	model, err := dataStore.PublicModel(context.Background(), "codex-gpt-5.6-luna")
	if err != nil {
		t.Fatal(err)
	}
	display := requestRouter.CatalogDisplayEntry(context.Background(), *model)
	if display.ID != "gpt-5.6-luna" {
		t.Fatalf("display id=%q want gpt-5.6-luna", display.ID)
	}
	if display.Name != "GPT-5.6 Luna" {
		t.Fatalf("display name=%q want GPT-5.6 Luna", display.Name)
	}
	resolved, err := requestRouter.Resolve(context.Background(), "gpt-5.6-luna", nil)
	if err != nil {
		t.Fatalf("resolve err=%v", err)
	}
	if resolved == nil || resolved.ID != "codex-gpt-5.6-luna" {
		t.Fatalf("resolve upstream=%+v", resolved)
	}
}

func openTestStore(t *testing.T, cfg *config.Config) *store.Store {
	t.Helper()
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := store.OpenSQLite(filepath.Join(t.TempDir(), "test.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	if err = dataStore.Seed(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	return dataStore
}
