package api

import (
	"context"
	"testing"

	"github.com/tproxy/tproxy/internal/store"
)

func TestConfigImportAllowsManagedModelRoutes(t *testing.T) {
	s := clusterTestServer(t)
	ctx := context.Background()
	if _, err := s.store.ApplyClusterCatalog(ctx, store.ClusterCatalog{Models: []store.ClusterCatalogModel{{ID: "shared", DisplayName: "Managed", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	w := clusterRequest(s, "POST", "/api/admin/config/import", `{"providers":[{"id":"local","type":"openai-compatible","enabled":true}],"cluster_routes":[{"model":"shared","routes":[{"id":"route","provider":"local","upstream_model":"up"}]}]}`)
	if w.Code != 200 {
		t.Fatalf("import status %d: %s", w.Code, w.Body.String())
	}
	routes, err := s.store.Routes(ctx, "shared")
	if err != nil || len(routes) != 1 || routes[0].ID != "route" || routes[0].ProviderID != "local" {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
}
