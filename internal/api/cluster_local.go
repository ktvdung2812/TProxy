package api

import (
	"context"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/cluster"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/router"
	"github.com/tproxy/tproxy/internal/store"
)

type clusterLocal struct{ s *Server }

type managedKeyContextKey struct{}

func (a *clusterLocal) LocalCapacity(ctx context.Context, id string) (int, error) {
	return a.s.router.LocalCapacity(ctx, id)
}
func (a *clusterLocal) LendingLedger(ctx context.Context, day string) ([]cluster.LedgerRow, error) {
	rows, err := a.s.store.ClusterLendingSummary(ctx, day)
	return clusterLedgerRows(rows), err
}
func (a *clusterLocal) BuildCatalog(ctx context.Context, shared cluster.SharedSelection) (cluster.Catalog, error) {
	c, err := a.s.store.BuildClusterCatalog(ctx, store.ClusterShared{Models: shared.Models, Combos: shared.Combos, APIKeys: shared.APIKeys, Prices: shared.Prices})
	if err != nil {
		return cluster.Catalog{}, err
	}
	out := cluster.Catalog{Models: make([]cluster.CatalogModel, 0, len(c.Models)), Combos: make([]cluster.CatalogCombo, 0, len(c.Combos)), APIKeys: make([]cluster.CatalogAPIKey, 0, len(c.APIKeys))}
	for _, m := range c.Models {
		out.Models = append(out.Models, cluster.CatalogModel(m))
	}
	for _, c := range c.Combos {
		items := make([]cluster.CatalogComboItem, 0, len(c.Items))
		for _, i := range c.Items {
			items = append(items, cluster.CatalogComboItem(i))
		}
		out.Combos = append(out.Combos, cluster.CatalogCombo{ID: c.ID, DisplayName: c.DisplayName, Enabled: c.Enabled, RewriteResponseModel: c.RewriteResponseModel, Capabilities: c.Capabilities, Limits: c.Limits, Policy: c.Policy, Items: items})
	}
	for _, k := range c.APIKeys {
		out.APIKeys = append(out.APIKeys, cluster.CatalogAPIKey(k))
	}
	return out, nil
}
func (a *clusterLocal) ApplyCatalog(ctx context.Context, c cluster.Catalog) (cluster.ApplyReport, error) {
	in := store.ClusterCatalog{}
	for _, m := range c.Models {
		in.Models = append(in.Models, store.ClusterCatalogModel(m))
	}
	for _, v := range c.Combos {
		items := make([]store.ClusterCatalogComboItem, 0, len(v.Items))
		for _, i := range v.Items {
			items = append(items, store.ClusterCatalogComboItem(i))
		}
		in.Combos = append(in.Combos, store.ClusterCatalogCombo{ID: v.ID, DisplayName: v.DisplayName, Enabled: v.Enabled, RewriteResponseModel: v.RewriteResponseModel, Capabilities: v.Capabilities, Limits: v.Limits, Policy: v.Policy, Items: items})
	}
	for _, k := range c.APIKeys {
		in.APIKeys = append(in.APIKeys, store.ClusterCatalogAPIKey(k))
	}
	report, err := a.s.store.ApplyClusterCatalog(ctx, in)
	if err != nil {
		return cluster.ApplyReport{}, err
	}
	a.s.refreshManagedKeys(ctx)
	out := cluster.ApplyReport{Version: c.Version, Models: report.Models, Combos: report.Combos, APIKeys: report.APIKeys}
	for _, v := range report.Conflicts {
		out.Conflicts = append(out.Conflicts, cluster.CatalogConflict(v))
	}
	return out, nil
}
func (a *clusterLocal) ApplyPeers(ctx context.Context, nodes []cluster.NodeState) error {
	peers := make([]store.ClusterPeer, 0, len(nodes))
	for _, n := range nodes {
		models := []string{}
		for _, o := range n.Offers {
			if o.EligibleAccounts > 0 {
				models = append(models, o.ModelID)
			}
		}
		sort.Strings(models)
		peers = append(peers, store.ClusterPeer{NodeID: n.ID, Name: n.Name, Models: models, Enabled: n.Online && n.Status == cluster.NodeActive && n.LendingEnabled && len(models) > 0})
	}
	return a.s.store.SyncClusterPeers(ctx, peers)
}
func (a *clusterLocal) ClearClusterData(ctx context.Context) error {
	if err := a.s.store.ClearClusterPeers(ctx); err != nil {
		return err
	}
	if err := a.s.store.ClearClusterManaged(ctx); err != nil {
		return err
	}
	a.s.refreshManagedKeys(ctx)
	return nil
}
func (a *clusterLocal) Audit(ctx context.Context, actor, action, id string, status int, metadata map[string]any) {
	_ = a.s.store.AddAuditEvent(ctx, store.AuditEvent{Actor: actor, Action: action, ResourceType: "cluster_node", ResourceID: id, Status: status, Metadata: metadata, CreatedAt: time.Now().UTC()})
}
func (a *clusterLocal) ServeRelayed() http.Handler {
	return a.s.loggingMiddleware(http.HandlerFunc(a.s.serveClusterRelayed))
}
func (s *Server) serveClusterRelayed(w http.ResponseWriter, r *http.Request) {
	id, name, ok := cluster.OriginFromContext(r.Context())
	if !ok {
		writeError(w, 403, cluster.CodeUnauthorized, "cluster origin required", useClientRequestID(r))
		return
	}
	lending := s.cluster.Lending()
	if !lending.Enabled {
		writeError(w, 403, cluster.CodeLendingDisabled, "cluster lending is disabled", useClientRequestID(r))
		return
	}
	key := clusterEffectiveKey(&store.APIKey{ID: "cluster:" + id, Name: "Cluster node " + name, Enabled: true, Models: []string{"*"}, Policy: config.ClientKeyPolicy{Limits: lending.Limits}}, lending, s.cluster.CatalogIDs())
	r = r.WithContext(context.WithValue(r.Context(), clusterRelayContext, id))
	s.admitClient(w, r, key, http.HandlerFunc(s.proxyIngress))
}
func (s *Server) refreshManagedKeys(ctx context.Context) {
	m, err := s.store.ClusterManagedItems(ctx)
	if err != nil {
		log.Printf("[cluster] refresh managed keys: %v", err)
		return
	}
	ids := make(map[string]struct{}, len(m.APIKeys))
	for _, id := range m.APIKeys {
		ids[id] = struct{}{}
	}
	s.managedKeys.Store(&ids)
}
func (s *Server) effectiveManagedKey(ctx context.Context, key *store.APIKey) (*store.APIKey, *providers.ProviderError) {
	if key == nil {
		return key, nil
	}
	managed, err := s.store.IsClusterManaged(ctx, "api_key", key.ID)
	if err != nil {
		log.Printf("[cluster] check managed key: %v", err)
		return nil, &providers.ProviderError{Status: 500, Code: "store_error", Message: "unable to check cluster key"}
	}
	if state, ok := ctx.Value(managedKeyContextKey{}).(*atomic.Bool); ok {
		state.Store(managed)
	}
	if !managed {
		return key, nil
	}
	if !s.cluster.ActiveMember() {
		return nil, &providers.ProviderError{Status: 403, Code: "cluster_not_active", Message: "this node is not an active cluster member"}
	}
	lending := s.cluster.Lending()
	if !lending.Enabled {
		return nil, &providers.ProviderError{Status: 403, Code: cluster.CodeLendingDisabled, Message: "cluster lending is disabled"}
	}
	return clusterEffectiveKey(key, lending, s.cluster.CatalogIDs()), nil
}
func stripClusterHeaders(r *http.Request) {
	for k := range r.Header {
		if strings.HasPrefix(strings.ToLower(k), "x-tproxy-cluster-") {
			delete(r.Header, k)
		}
	}
}
func markClusterRelay(r *http.Request, request *canonical.Request) {
	if id, ok := r.Context().Value(clusterRelayContext).(string); ok {
		if request.Metadata == nil {
			request.Metadata = map[string]any{}
		}
		request.Metadata[router.ClusterRelayedMetadataKey] = true
		request.Metadata["cluster_origin_node"] = id
		request.Metadata["cluster_borrower"] = "node:" + id
	}
}

func clusterBorrower(r *http.Request, key *store.APIKey) string {
	if id, ok := r.Context().Value(clusterRelayContext).(string); ok {
		return "node:" + id
	}
	if state, ok := r.Context().Value(managedKeyContextKey{}).(*atomic.Bool); ok && state.Load() && key != nil {
		return "key:" + key.ID
	}
	return ""
}
func isClusterRelay(r *http.Request) bool {
	_, ok := r.Context().Value(clusterRelayContext).(string)
	return ok
}
