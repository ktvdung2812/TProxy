package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/cluster"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/router"
	"github.com/tproxy/tproxy/internal/security"
	"github.com/tproxy/tproxy/internal/store"
)

func clusterTestServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("TPROXY_SKIP_TUNNEL_AUTO", "1")
	cfg := &config.Config{Server: config.ServerConfig{Host: "127.0.0.1", Port: 8317}}
	db := apiTestStore(t, cfg)
	s := NewServer(cfg, db, router.New(db, providers.NewRegistry()))
	t.Cleanup(s.Close)
	return s
}
func clusterRequest(s *Server, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:1234"
	withDefaultManagementAuth(r)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func TestClusterAdmin(t *testing.T) {
	s := clusterTestServer(t)
	status := clusterRequest(s, "GET", "/api/admin/cluster", "")
	if status.Code != 200 {
		t.Fatal(status.Body.String())
	}
	var state map[string]any
	if err := json.Unmarshal(status.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state["role"] != "standalone" || state["cluster"] != nil || state["link"] != nil || !strings.Contains(status.Body.String(), "http://127.0.0.1:8317") || state["warnings"] == nil {
		t.Fatal(status.Body.String())
	}
	raw := httptest.NewRecorder()
	s.Handler().ServeHTTP(raw, httptest.NewRequest("GET", "/api/cluster/v1/state", nil))
	if raw.Code != 404 || len(s.liveLogs.Recent(100)) != 1 {
		t.Fatalf("state=%d logs=%d", raw.Code, len(s.liveLogs.Recent(100)))
	}
	created := clusterRequest(s, "POST", "/api/admin/cluster/create", `{"name":"test","hub_url":"http://127.0.0.1:1","allow_insecure":true}`)
	if created.Code != 200 || !strings.Contains(created.Body.String(), `"role":"hub"`) {
		t.Fatal(created.Code, created.Body.String())
	}
	again := clusterRequest(s, "POST", "/api/admin/cluster/create", `{"hub_url":"http://127.0.0.1:1","allow_insecure":true}`)
	if again.Code != 409 || !strings.Contains(again.Body.String(), "cluster_already_configured") {
		t.Fatal(again.Code, again.Body.String())
	}
	raw = httptest.NewRecorder()
	before := len(s.liveLogs.Recent(100))
	s.Handler().ServeHTTP(raw, httptest.NewRequest("GET", "/api/cluster/v1/state", nil))
	if raw.Code != 401 || !strings.Contains(raw.Body.String(), "cluster_unauthorized") || len(s.liveLogs.Recent(100)) != before {
		t.Fatal(raw.Code, raw.Body.String())
	}
	token := clusterRequest(s, "POST", "/api/admin/cluster/tokens", `{"label":"test"}`)
	if token.Code != 200 {
		t.Fatal(token.Code, token.Body.String())
	}
	var v map[string]any
	_ = json.Unmarshal(token.Body.Bytes(), &v)
	id, _ := v["id"].(string)
	list := clusterRequest(s, "GET", "/api/admin/cluster/tokens", "")
	if list.Code != 200 || !strings.Contains(list.Body.String(), id) {
		t.Fatal(list.Body.String())
	}
	revoked := clusterRequest(s, "DELETE", "/api/admin/cluster/tokens/"+id, "")
	if revoked.Code != 200 {
		t.Fatal(revoked.Body.String())
	}
	for _, tc := range []struct {
		path, body string
		status     int
	}{{"lending", `{"enabled":true,"models":["m"],"limits":{"requests_per_minute":3}}`, 200}, {"borrowing", `{"enabled":true,"mode":"shared"}`, 200}, {"borrowing", `{"enabled":true,"mode":"invalid"}`, 400}} {
		got := clusterRequest(s, "PUT", "/api/admin/cluster/"+tc.path, tc.body)
		if got.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, got.Code, got.Body.String())
		}
		if tc.status == 200 {
			if tc.path == "lending" && (!strings.Contains(got.Body.String(), `"models":["m"]`) || !strings.Contains(got.Body.String(), `"requests_per_minute":3`)) {
				t.Fatal(got.Body.String())
			}
			if tc.path == "borrowing" && !strings.Contains(got.Body.String(), `"mode":"shared"`) {
				t.Fatal(got.Body.String())
			}
		}
	}
	snap := clusterRequest(s, "GET", "/api/admin/snapshot", "")
	if snap.Code != 200 || !strings.Contains(snap.Body.String(), `"cluster_managed":{"api_keys":[],"combos":[],"models":[]}`) {
		t.Fatal(snap.Code, snap.Body.String())
	}
	standalone := clusterTestServer(t)
	garbage := clusterRequest(standalone, "POST", "/api/admin/cluster/join", `{"token":"garbage"}`)
	if garbage.Code != 400 || !strings.Contains(garbage.Body.String(), "invalid_join_token") {
		t.Fatal(garbage.Code, garbage.Body.String())
	}
}
func TestClusterRelayedAdmission(t *testing.T) {
	s := clusterTestServer(t)
	if err := s.cluster.SetLending(context.Background(), cluster.LendingPolicy{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for _, origin := range []bool{false, true} {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"anything","messages":[]}`))
		if origin {
			r = r.WithContext(cluster.WithOrigin(r.Context(), "peer", "Peer"))
		}
		w := httptest.NewRecorder()
		(&clusterLocal{s: s}).ServeRelayed().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(fmt.Sprintf("origin=%v status=%d body=%s", origin, w.Code, w.Body.String()))
		}
		if !origin && !strings.Contains(w.Body.String(), "cluster_unauthorized") {
			t.Fatal(w.Body.String())
		}
	}
}

func TestClusterSyncExcludedFromAudit(t *testing.T) {
	s := clusterTestServer(t)
	before, err := s.store.RecentAuditEvents(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	_ = clusterRequest(s, "POST", "/api/admin/cluster/sync", "")
	after, err := s.store.RecentAuditEvents(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("sync wrote audit event: %+v", after)
	}
	_ = clusterRequest(s, "PUT", "/api/admin/cluster/borrowing", `{"enabled":true,"mode":"shared"}`)
	after, err = s.store.RecentAuditEvents(context.Background(), 100)
	if err != nil || len(after) != len(before)+1 {
		t.Fatalf("mutation audit %+v: %v", after, err)
	}
}

func TestClusterManagedKeyLoadsPersistedLending(t *testing.T) {
	t.Setenv("TPROXY_SKIP_TUNNEL_AUTO", "1")
	cfg := &config.Config{}
	db := apiTestStore(t, cfg)
	ctx := context.Background()
	_, err := db.ApplyClusterCatalog(ctx, store.ClusterCatalog{Models: []store.ClusterCatalogModel{{ID: "model", DisplayName: "Model", Enabled: true}}, APIKeys: []store.ClusterCatalogAPIKey{{ID: "managed-key", Name: "Managed", KeyHash: security.HashAPIKey("secret"), Enabled: true, Models: []string{"model"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.SaveClusterLending(ctx, store.ClusterLending{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	s := NewServer(cfg, db, router.New(db, providers.NewRegistry()))
	defer s.Close()
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"model"}`))
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "cluster_not_active") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestClusterRelayMetadataAndHeaderHygiene(t *testing.T) {
	s := clusterTestServer(t)
	s.currentConfig().Server.AllowLocalWithoutKey = true
	request := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header["x-tPrOxY-cLuStEr-Origin"] = []string{"spoof"}
	request.Header.Set("X-Tproxy-Cluster-Model", "spoof")
	w := httptest.NewRecorder()
	s.clientAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for h := range r.Header {
			if strings.HasPrefix(strings.ToLower(h), "x-tproxy-cluster-") {
				t.Errorf("cluster header survived: %s", h)
			}
		}
		w.WriteHeader(204)
	})).ServeHTTP(w, request)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r = r.WithContext(context.WithValue(r.Context(), clusterRelayContext, "node-a"))
	var req canonical.Request
	markClusterRelay(r, &req)
	if req.Metadata[router.ClusterRelayedMetadataKey] != true || req.Metadata["cluster_origin_node"] != "node-a" {
		t.Fatal(req.Metadata)
	}
}

func TestClusterPeersAndCatalogAdapters(t *testing.T) {
	s := clusterTestServer(t)
	local := &clusterLocal{s: s}
	ctx := context.Background()
	nodes := []cluster.NodeState{{ID: "active", Name: "Active", Status: cluster.NodeActive, Online: true, LendingEnabled: true, Offers: []cluster.Offer{{ModelID: "z", EligibleAccounts: 1}, {ModelID: "a", EligibleAccounts: 2}, {ModelID: "skip"}}}, {ID: "revoked", Status: cluster.NodeRevoked, Online: true, LendingEnabled: true, Offers: []cluster.Offer{{ModelID: "a", EligibleAccounts: 1}}}}
	if err := local.ApplyPeers(ctx, nodes); err != nil {
		t.Fatal(err)
	}
	active, err := s.store.CredentialByID(ctx, "cluster-node-active")
	if err != nil || !active.Enabled || !reflect.DeepEqual(active.Metadata["serves_models"], []any{"a", "z"}) {
		t.Fatalf("active=%+v err=%v", active, err)
	}
	revoked, err := s.store.CredentialByID(ctx, "cluster-node-revoked")
	if err != nil || revoked.Enabled {
		t.Fatalf("revoked=%+v err=%v", revoked, err)
	}
	catalog := cluster.Catalog{Version: "v1", Models: []cluster.CatalogModel{{ID: "managed", DisplayName: "Managed", Enabled: true}}, APIKeys: []cluster.CatalogAPIKey{{ID: "managed-key", Name: "Managed", KeyHash: security.HashAPIKey("secret"), Enabled: true, Models: []string{"managed"}}}}
	report, err := local.ApplyCatalog(ctx, catalog)
	if err != nil || report.Version != "v1" || !reflect.DeepEqual(report.Models, []string{"managed"}) {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if ids := s.managedKeys.Load(); ids == nil {
		t.Fatal("missing cache")
	} else if _, ok := (*ids)["managed-key"]; !ok {
		t.Fatal("key missing from cache")
	}
	if err = local.ClearClusterData(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := (*s.managedKeys.Load())["managed-key"]; ok {
		t.Fatal("key still cached")
	}
}

func TestClusterManagedKeyCacheMissFailsClosed(t *testing.T) {
	s := clusterTestServer(t)
	ctx := context.Background()
	_, err := s.store.ApplyClusterCatalog(ctx, store.ClusterCatalog{APIKeys: []store.ClusterCatalogAPIKey{{ID: "new-key", Name: "Key", KeyHash: security.HashAPIKey("new-secret"), Enabled: true, Models: []string{"*"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if ids := s.managedKeys.Load(); ids != nil {
		if _, ok := (*ids)["new-key"]; ok {
			t.Fatal("expected a stale cache")
		}
	}
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"model"}`))
	r.Header.Set("Authorization", "Bearer new-secret")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "cluster_not_active") {
		t.Fatal(w.Code, w.Body.String())
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, policyErr := s.effectiveManagedKey(canceled, &store.APIKey{ID: "new-key"}); policyErr == nil || policyErr.Code != "store_error" {
		t.Fatalf("managed lookup failure must deny: %v", policyErr)
	}
}

func TestClusterSettingsEncryptionAvailable(t *testing.T) {
	encrypted := apiTestStore(t, &config.Config{})
	if !(clusterSettingsAdapter{store: encrypted}).EncryptionAvailable() {
		t.Fatal("expected encryption with master key")
	}
	unencrypted, err := store.OpenSQLite(t.TempDir()+"/no-master-key.db", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer unencrypted.Close()
	if (clusterSettingsAdapter{store: unencrypted}).EncryptionAvailable() {
		t.Fatal("encryption available without master key")
	}
}
