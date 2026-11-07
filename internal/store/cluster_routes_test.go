package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/tproxy/tproxy/internal/config"
	"gopkg.in/yaml.v3"
)

func TestClusterRoutesRoundTrip(t *testing.T) {
	ctx := context.Background()
	source, _ := openClusterTestStore(t, true)
	provider := config.ProviderConfig{ID: "local", Type: "openai-compatible", Enabled: true}
	catalog := ClusterCatalog{Models: []ClusterCatalogModel{{ID: "shared", DisplayName: "Hub model", Enabled: true}}}
	if err := source.Seed(ctx, &config.Config{Providers: []config.ProviderConfig{provider}}); err != nil {
		t.Fatal(err)
	}
	if _, err := source.ApplyClusterCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	if err := source.SyncClusterPeers(ctx, []ClusterPeer{{NodeID: "peer", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	disabled := false
	localRoute := config.RouteTargetConfig{ID: "local-route", Provider: "local", UpstreamModel: "upstream", Priority: 7, Weight: 4, Enabled: &disabled, Conditions: map[string]any{"region": "test"}, Pricing: &config.PricingConfig{InputPerMillion: 2}}
	if err := source.SavePublicModel(ctx, config.PublicModelConfig{ID: "shared", DisplayName: "Hub model", Enabled: true, Routes: []config.RouteTargetConfig{localRoute, {ID: "cluster-route", Provider: ClusterProviderID, UpstreamModel: "shared"}}}); err != nil {
		t.Fatal(err)
	}
	for _, export := range []func(context.Context, *config.Config) (*config.Config, error){source.ExportConfig, source.ExportConfigWithOAuthTokens} {
		cfg, err := export(ctx, &config.Config{ClusterRoutes: []config.ClusterRouteAttachment{{Model: "stale"}}})
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Models) != 0 || len(cfg.ClusterRoutes) != 1 || cfg.ClusterRoutes[0].Model != "shared" || !reflect.DeepEqual(cfg.ClusterRoutes[0].Routes, []config.RouteTargetConfig{localRoute}) {
			t.Fatalf("exported models=%+v attachments=%+v", cfg.Models, cfg.ClusterRoutes)
		}
		encoded, err := yaml.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var imported config.Config
		if err = yaml.Unmarshal(encoded, &imported); err != nil {
			t.Fatal(err)
		}
		imported.PrepareImport()
		if err = imported.Validate(); err != nil {
			t.Fatal(err)
		}
		fresh, _ := openClusterTestStore(t, true)
		if err = fresh.Seed(ctx, &imported); err != nil {
			t.Fatal(err)
		}
		var pending string
		if err = fresh.db.QueryRowContext(ctx, `SELECT value_json FROM app_settings WHERE key=?`, clusterPendingRoutesKey).Scan(&pending); err != nil || pending == "" {
			t.Fatalf("pending=%q err=%v", pending, err)
		}
		if _, err = fresh.ApplyClusterCatalog(ctx, catalog); err != nil {
			t.Fatal(err)
		}
		routes, err := fresh.Routes(ctx, "shared")
		if err != nil || len(routes) != 1 || routes[0].ID != localRoute.ID || routes[0].ProviderID != localRoute.Provider || routes[0].UpstreamModel != localRoute.UpstreamModel || routes[0].Priority != 7 || routes[0].Weight != 4 || routes[0].Enabled || !reflect.DeepEqual(routes[0].Conditions, localRoute.Conditions) || !reflect.DeepEqual(routes[0].Pricing, localRoute.Pricing) {
			t.Fatalf("routes=%+v err=%v", routes, err)
		}
		if err = fresh.db.QueryRowContext(ctx, `SELECT value_json FROM app_settings WHERE key=?`, clusterPendingRoutesKey).Scan(&pending); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("pending routes after sync: %q, %v", pending, err)
		}
	}
}

func TestPendingClusterRoutesMergeAndCatalogRollback(t *testing.T) {
	ctx := context.Background()
	s, _ := openClusterTestStore(t, true)
	provider := config.ProviderConfig{ID: "local", Type: "openai-compatible", Enabled: true}
	first := config.ClusterRouteAttachment{Model: "first", Routes: []config.RouteTargetConfig{{ID: "taken", Provider: "local", UpstreamModel: "old"}}}
	second := config.ClusterRouteAttachment{Model: "second", Routes: []config.RouteTargetConfig{{Provider: "local", UpstreamModel: "other"}}}
	if err := s.Seed(ctx, &config.Config{Providers: []config.ProviderConfig{provider}, Models: []config.PublicModelConfig{{ID: "existing", Routes: []config.RouteTargetConfig{{ID: "taken", Provider: "local", UpstreamModel: "existing"}}}}, ClusterRoutes: []config.ClusterRouteAttachment{first, second}}); err != nil {
		t.Fatal(err)
	}
	first.Routes[0].UpstreamModel = "latest"
	if err := s.Seed(ctx, &config.Config{ClusterRoutes: []config.ClusterRouteAttachment{first}}); err != nil {
		t.Fatal(err)
	}
	catalog := ClusterCatalog{Models: []ClusterCatalogModel{{ID: "first", Enabled: true}}}
	if _, err := s.ApplyClusterCatalog(ctx, catalog); err == nil {
		t.Fatal("expected conflicting route to roll back catalog")
	}
	if _, err := s.PublicModel(ctx, "first"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("model persisted after rollback: %v", err)
	}
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT value_json FROM app_settings WHERE key=?`, clusterPendingRoutesKey).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var pending []config.ClusterRouteAttachment
	if err := json.Unmarshal([]byte(raw), &pending); err != nil || len(pending) != 2 || pending[0].Routes[0].UpstreamModel != "latest" {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	if err := s.SavePublicModel(ctx, config.PublicModelConfig{ID: "existing"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyClusterCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	routes, err := s.Routes(ctx, "first")
	if err != nil || len(routes) != 1 || routes[0].UpstreamModel != "latest" {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
	if err = s.db.QueryRowContext(ctx, `SELECT value_json FROM app_settings WHERE key=?`, clusterPendingRoutesKey).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(raw), &pending); err != nil || len(pending) != 1 || pending[0].Model != "second" {
		t.Fatalf("remaining=%+v err=%v", pending, err)
	}
}

func TestClusterRoutesApplyExistingAndRollback(t *testing.T) {
	ctx := context.Background()
	s, _ := openClusterTestStore(t, true)
	catalog := ClusterCatalog{Models: []ClusterCatalogModel{{ID: "shared", DisplayName: "Original", Enabled: true}}}
	if _, err := s.ApplyClusterCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncClusterPeers(ctx, []ClusterPeer{{NodeID: "peer", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	provider := config.ProviderConfig{ID: "local", Type: "openai-compatible", Enabled: true}
	if err := s.Seed(ctx, &config.Config{Providers: []config.ProviderConfig{provider}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePublicModel(ctx, config.PublicModelConfig{ID: "shared", DisplayName: "Original", Enabled: true, Routes: []config.RouteTargetConfig{{ID: "old", Provider: "local", UpstreamModel: "old"}, {ID: "cluster", Provider: ClusterProviderID, UpstreamModel: "shared"}}}); err != nil {
		t.Fatal(err)
	}
	attachment := config.ClusterRouteAttachment{Model: "shared", Routes: []config.RouteTargetConfig{{ID: "new", Provider: "local", UpstreamModel: "new"}}}
	if err := s.Seed(ctx, &config.Config{Providers: []config.ProviderConfig{provider}, ClusterRoutes: []config.ClusterRouteAttachment{attachment}}); err != nil {
		t.Fatal(err)
	}
	routes, err := s.Routes(ctx, "shared")
	if err != nil || len(routes) != 2 || routes[0].ID != "cluster" || routes[1].ID != "new" {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
	model, err := s.PublicModel(ctx, "shared")
	if err != nil || model.DisplayName != "Original" {
		t.Fatalf("model=%+v err=%v", model, err)
	}
	attachment.Routes[0].ID = "cluster"
	if err = s.Seed(ctx, &config.Config{ClusterRoutes: []config.ClusterRouteAttachment{attachment}}); err == nil {
		t.Fatal("expected route ID conflict")
	}
	after, err := s.Routes(ctx, "shared")
	if err != nil || !reflect.DeepEqual(after, routes) {
		t.Fatalf("routes after failed seed=%+v err=%v", after, err)
	}
	if err = s.SavePublicModel(ctx, config.PublicModelConfig{ID: "local-model", DisplayName: "Local", Routes: []config.RouteTargetConfig{{ID: "local-old", Provider: "local", UpstreamModel: "old"}}}); err != nil {
		t.Fatal(err)
	}
	if err = s.Seed(ctx, &config.Config{ClusterRoutes: []config.ClusterRouteAttachment{{Model: "local-model", Routes: []config.RouteTargetConfig{}}}}); err != nil {
		t.Fatal(err)
	}
	localRoutes, err := s.Routes(ctx, "local-model")
	localModel, modelErr := s.PublicModel(ctx, "local-model")
	if err != nil || len(localRoutes) != 0 || modelErr != nil || localModel.DisplayName != "Local" {
		t.Fatalf("local model=%+v routes=%+v errors=%v, %v", localModel, localRoutes, modelErr, err)
	}
}
