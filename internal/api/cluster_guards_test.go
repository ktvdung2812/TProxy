package api

import (
	"context"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/store"
)

func TestClusterManagedGuards(t *testing.T) {
	s := clusterTestServer(t)
	ctx := context.Background()
	catalog := store.ClusterCatalog{Models: []store.ClusterCatalogModel{{ID: "managed", DisplayName: "Managed", Aliases: []string{"alias"}, Enabled: true}}, Combos: []store.ClusterCatalogCombo{{ID: "combo", DisplayName: "Combo", Enabled: true, Items: []store.ClusterCatalogComboItem{{PublicModelID: "managed"}}}}, APIKeys: []store.ClusterCatalogAPIKey{{ID: "key", Name: "Key", KeyHash: "hash", Enabled: true, Models: []string{"managed"}}}}
	if _, err := s.store.ApplyClusterCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SyncClusterPeers(ctx, []store.ClusterPeer{{NodeID: "peer", Name: "Peer"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SaveProvider(ctx, config.ProviderConfig{ID: "local", Type: "openai-compatible", Name: "Local", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		method, path, body string
		status             int
	}{
		{"DELETE", "/api/admin/models/managed", "", 409},
		{"PUT", "/api/admin/models", `{"id":"managed","display_name":"Changed","aliases":["alias"],"enabled":true}`, 409},
		{"PUT", "/api/admin/models", `{"id":"managed","display_name":"Managed","aliases":["alias"],"enabled":true,"routes":[{"provider":"local","upstream_model":"up"}]}`, 200},
		{"DELETE", "/api/admin/combos/combo", "", 409},
		{"DELETE", "/api/admin/api-keys/key", "", 409},
		{"POST", "/api/admin/providers", `{"id":"tproxy-cluster","type":"openai-compatible"}`, 409},
		{"POST", "/api/admin/credentials", `{"provider_id":"tproxy-cluster","credential":{"id":"custom"}}`, 409},
		{"POST", "/api/admin/aliases", `{"alias":"managed","public_model_id":"managed"}`, 409},
		{"DELETE", "/api/admin/aliases/alias", "", 409},
		{"DELETE", "/api/admin/aliases/managed", "", 409},
		{"PATCH", "/api/admin/combos/combo", "{}", 409},
		{"POST", "/api/admin/combos/combo/enable", "", 409},
		{"PUT", "/api/admin/providers/tproxy-cluster/credentials/order", "{}", 409},
		{"DELETE", "/api/admin/providers/tproxy-cluster", "", 409},
		{"DELETE", "/api/admin/credentials/cluster-node-peer", "", 409},
	}
	for _, tc := range cases {
		w := clusterRequest(s, tc.method, tc.path, tc.body)
		if w.Code != tc.status {
			t.Errorf("%s %s: status %d body %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	routes, err := s.store.Routes(ctx, "managed")
	if err != nil || len(routes) != 1 || routes[0].ProviderID != "local" {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
	m, err := s.store.PublicModel(ctx, "managed")
	if err != nil || len(m.Aliases) != 1 || m.Aliases[0] != "alias" {
		t.Fatalf("model=%+v err=%v", m, err)
	}
}

func TestClusterManagedImports(t *testing.T) {
	s := clusterTestServer(t)
	ctx := context.Background()
	_, err := s.store.ApplyClusterCatalog(ctx, store.ClusterCatalog{
		Models:  []store.ClusterCatalogModel{{ID: "codex-gpt", DisplayName: "Managed", Aliases: []string{"shared-alias"}, Enabled: true}},
		Combos:  []store.ClusterCatalogCombo{{ID: "shared-combo", DisplayName: "Combo", Enabled: true, Items: []store.ClusterCatalogComboItem{{PublicModelID: "codex-gpt"}}}},
		APIKeys: []store.ClusterCatalogAPIKey{{ID: "shared-key", Name: "Key", KeyHash: "hash", Enabled: true, Models: []string{"codex-gpt"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SyncClusterPeers(ctx, []store.ClusterPeer{{NodeID: "peer", Name: "Peer"}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"cliproxy-codex-member", "sub2api-openai-member"} {
		if err := s.store.SaveCredential(ctx, store.ClusterProviderID, config.CredentialConfig{ID: id, AuthType: "none"}); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct{ path, body, id string }{
		{"/api/admin/config/import", `{"models":[{"id":"codex-gpt"}]}`, "model:codex-gpt"},
		{"/api/admin/config/import", `{"models":[{"id":"other","aliases":["shared-alias"]}]}`, "alias:shared-alias"},
		{"/api/admin/config/import", `{"providers":[{"id":"tproxy-cluster"}]}`, "provider:tproxy-cluster"},
		{"/api/admin/import/9router", `{"apiKeys":[{"id":"shared-key","key":"secret"}]}`, "shared-key"},
		{"/api/admin/import/9router", `{"combos":[{"name":"shared combo","models":["codex/gpt"]}]}`, "combo:shared-combo"},
		{"/api/admin/import", `{"combos":[{"name":"other","models":["codex/gpt"]}]}`, "model:codex-gpt"},
		{"/api/admin/import/9router", `{"providerConnections":[{"id":"cluster-node-peer","provider":"codex"}]}`, "credential:cluster-node-peer"},
		{"/api/admin/import/cliproxyapi", `{"type":"tproxy-cluster","access_token":"x"}`, "provider:tproxy-cluster"},
		{"/api/admin/import/cliproxyapi", `{"type":"codex","account_id":"member","access_token":"x"}`, "credential:cliproxy-codex-member"},
		{"/api/admin/import/sub2api", `{"accounts":[{"platform":"openai","type":"oauth","name":"member","credentials":{"access_token":"x"}}]}`, "credential:sub2api-openai-member"},
		{"/api/admin/import", `{"accounts":[{"platform":"openai","type":"oauth","name":"member","credentials":{"access_token":"x"}}]}`, "credential:sub2api-openai-member"},
		{"/api/admin/import", `{"version":1,"credentials":[{"id":"x","provider_id":"tproxy-cluster"}]}`, "provider:tproxy-cluster"},
		{"/api/admin/auth/import", `{"version":1,"credentials":[{"id":"x","provider_id":"tproxy-cluster"}]}`, "provider:tproxy-cluster"},
	}
	for _, tc := range cases {
		for _, query := range []string{"", "?dry_run=true"} {
			w := clusterRequest(s, "POST", tc.path+query, tc.body)
			if w.Code != 409 || !strings.Contains(w.Body.String(), "cluster_managed") || !strings.Contains(w.Body.String(), tc.id) {
				t.Errorf("%s%s: %d %s", tc.path, query, w.Code, w.Body.String())
			}
		}
	}
}
