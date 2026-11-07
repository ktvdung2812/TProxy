package cluster

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tproxy/tproxy/internal/security"
)

const maxJoinBodyBytes = 16 << 10

type joinRequest struct {
	Protocol  string `json:"protocol"`
	ClusterID string `json:"cluster_id"`
	Secret    string `json:"secret"`
	NodeID    string `json:"node_id"`
	NodeName  string `json:"node_name"`
	Owner     string `json:"owner"`
	Version   string `json:"version"`
}

type joinResponse struct {
	ClusterID   string `json:"cluster_id"`
	ClusterName string `json:"cluster_name"`
	HubNodeID   string `json:"hub_node_id"`
	HubName     string `json:"hub_name"`
	NodeToken   string `json:"node_token"`
}

type ledgerResponse struct {
	Rows []LedgerRow `json:"rows"`
}

// Handler serves the hub API under HubPathPrefix. The API layer mounts it at
// "/api/cluster/" without stripping the prefix; every path answers 404 unless
// this node is the hub.
func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(s.serveHub)
}

func (s *Service) serveHub(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.Path, HubPathPrefix)
	if !ok {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}
	if err := s.ensureLoaded(r.Context()); err != nil {
		s.logf("[cluster] load settings: %v", err)
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "cluster settings are unavailable")
		return
	}
	if !s.isActiveHub() {
		writeError(w, http.StatusNotFound, CodeNotFound, "this node is not a cluster hub")
		return
	}
	switch {
	case rest == "join":
		if requireMethod(w, r, http.MethodPost) {
			s.hubJoin(w, r)
		}
	case rest == "link":
		if requireMethod(w, r, http.MethodGet) {
			s.hubLinkHandler(w, r)
		}
	case rest == "state":
		if requireMethod(w, r, http.MethodGet) {
			s.hubStateHandler(w, r)
		}
	case rest == "catalog":
		if requireMethod(w, r, http.MethodGet) {
			s.hubCatalogHandler(w, r)
		}
	case rest == "ledger":
		if requireMethod(w, r, http.MethodGet) {
			s.hubLedgerHandler(w, r)
		}
	case rest == "leave":
		if requireMethod(w, r, http.MethodPost) {
			s.hubLeaveHandler(w, r)
		}
	case strings.HasPrefix(rest, "relay/"):
		s.hubRelay(w, r)
	default:
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
	}
}

// authNode resolves the calling member from its bearer node token.
func (s *Service) authNode(w http.ResponseWriter, r *http.Request) (NodeRecord, bool) {
	token := security.BearerToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "node token required")
		return NodeRecord{}, false
	}
	node, err := s.hubStore.NodeByTokenHash(r.Context(), HashSecret(token))
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "unknown node token")
		return NodeRecord{}, false
	}
	if err != nil {
		s.logf("[cluster] node auth: %v", err)
		writeError(w, http.StatusInternalServerError, CodeInternal, "node lookup failed")
		return NodeRecord{}, false
	}
	if node.Status != NodeActive {
		writeError(w, http.StatusUnauthorized, CodeNodeRevoked, "this node is no longer part of the cluster")
		return NodeRecord{}, false
	}
	if claimed := strings.TrimSpace(r.Header.Get(HeaderNode)); claimed != "" && claimed != node.ID {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "node token does not match node id")
		return NodeRecord{}, false
	}
	return node, true
}

func (s *Service) hubJoin(w http.ResponseWriter, r *http.Request) {
	var in joinRequest
	if err := decodeBody(http.MaxBytesReader(w, r.Body, maxJoinBodyBytes), maxJoinBodyBytes, &in); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid join request")
		return
	}
	if in.Protocol != ProtocolVersion {
		writeError(w, http.StatusUpgradeRequired, CodeProtocolMismatch, "cluster protocol "+ProtocolVersion+" is required")
		return
	}
	s.mu.Lock()
	m, self := s.membership, s.identity
	s.mu.Unlock()
	if in.ClusterID != m.ClusterID || strings.TrimSpace(in.Secret) == "" {
		writeError(w, http.StatusBadRequest, CodeInvalidJoinToken, "join token is not valid for this cluster")
		return
	}
	nodeID := strings.TrimSpace(in.NodeID)
	if !validNodeID(nodeID) || nodeID == self.ID {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid node id")
		return
	}
	name, nameOK := cleanText(in.NodeName)
	owner, ownerOK := cleanText(in.Owner)
	version, versionOK := cleanText(in.Version)
	if !nameOK || !ownerOK || !versionOK {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "node name, owner and version must be at most 100 printable characters")
		return
	}
	// Serialize node identity checks with joins, leave, and revocation.
	s.links.mu.Lock()
	defer s.links.mu.Unlock()
	if !s.isActiveHub() {
		writeError(w, http.StatusConflict, CodeNotConfigured, "this node is not a cluster hub")
		return
	}
	existing, lookupErr := s.findNode(r.Context(), nodeID)
	if lookupErr != nil && !errors.Is(lookupErr, ErrNotFound) {
		writeError(w, http.StatusInternalServerError, CodeInternal, "join failed")
		return
	}
	if lookupErr == nil && existing.Status != NodeRevoked && existing.Status != NodeLeft {
		writeError(w, http.StatusConflict, CodeNodeExists, "cluster node already exists")
		return
	}
	// Validation comes first so a malformed request does not burn a use.
	token, err := s.hubStore.ConsumeJoinToken(r.Context(), HashSecret(in.Secret), s.now())
	switch {
	case errors.Is(err, ErrJoinTokenExpired):
		writeError(w, http.StatusBadRequest, CodeJoinTokenExpired, "join token has expired")
		return
	case errors.Is(err, ErrJoinTokenInvalid), errors.Is(err, ErrNotFound):
		writeError(w, http.StatusBadRequest, CodeInvalidJoinToken, "join token is invalid or used up")
		return
	case err != nil:
		s.logf("[cluster] consume join token: %v", err)
		writeError(w, http.StatusInternalServerError, CodeInternal, "join failed")
		return
	}
	if name == "" || owner == "" {
		if lookupErr == nil {
			name = cmp.Or(name, existing.Name)
			owner = cmp.Or(owner, existing.Owner)
		}
	}
	if name == "" {
		name = "node-" + nodeID[:min(8, len(nodeID))]
	}
	nodeToken := NewSecret()
	now := s.now().UTC()
	record := NodeRecord{ID: nodeID, Name: name, Owner: owner, Status: NodeActive, Version: version, LastAddr: remoteHost(r), JoinedAt: now, LastSeenAt: now}
	if err := s.hubStore.SaveNode(r.Context(), record, HashSecret(nodeToken)); err != nil {
		s.logf("[cluster] save node %s: %v", nodeID, err)
		writeError(w, http.StatusInternalServerError, CodeInternal, "join failed")
		return
	}
	s.local.Audit(r.Context(), PrincipalPrefix+nodeID, "cluster.join", nodeID, http.StatusOK, map[string]any{
		"name": name, "owner": owner, "version": version, "join_token_id": token.ID, "addr": record.LastAddr,
	})
	s.logf("[cluster] node %s (%s) joined", nodeID, name)
	writeJSON(w, http.StatusOK, joinResponse{ClusterID: m.ClusterID, ClusterName: m.ClusterName, HubNodeID: self.ID, HubName: self.Name, NodeToken: nodeToken})
	s.kick()
}

func (s *Service) hubStateHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authNode(w, r); !ok {
		return
	}
	state, err := s.hubState(r.Context())
	if err != nil {
		s.logf("[cluster] state: %v", err)
		writeError(w, http.StatusInternalServerError, CodeInternal, "cluster state unavailable")
		return
	}
	w.Header().Set("ETag", quoteETag(state.Version))
	w.Header().Set("Cache-Control", "no-store")
	if etagMatch(r.Header.Get("If-None-Match"), state.Version) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Service) hubCatalogHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authNode(w, r); !ok {
		return
	}
	if err := s.ensureCatalog(r.Context()); err != nil {
		s.logf("[cluster] build catalog: %v", err)
		writeError(w, http.StatusInternalServerError, CodeInternal, "catalog unavailable")
		return
	}
	s.mu.Lock()
	catalog := s.catalog
	s.mu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, catalog)
}

func (s *Service) hubLedgerHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authNode(w, r); !ok {
		return
	}
	rows, err := s.hubStore.Ledger(r.Context(), sinceDay(s.now(), parseDays(r.URL.Query().Get("days"))))
	if err != nil {
		s.logf("[cluster] ledger: %v", err)
		writeError(w, http.StatusInternalServerError, CodeInternal, "ledger unavailable")
		return
	}
	if rows == nil {
		rows = []LedgerRow{}
	}
	writeJSON(w, http.StatusOK, ledgerResponse{Rows: rows})
}

func (s *Service) hubLeaveHandler(w http.ResponseWriter, r *http.Request) {
	node, ok := s.authNode(w, r)
	if !ok {
		return
	}
	s.links.mu.Lock()
	current, err := s.hubStore.NodeByTokenHash(r.Context(), HashSecret(security.BearerToken(r)))
	if err != nil || current.ID != node.ID || current.Status != NodeActive {
		s.links.mu.Unlock()
		writeError(w, http.StatusUnauthorized, CodeNodeRevoked, "this node is no longer part of the cluster")
		return
	}
	if err := s.hubStore.SetNodeStatus(r.Context(), node.ID, NodeLeft); err != nil {
		s.links.mu.Unlock()
		s.logf("[cluster] leave %s: %v", node.ID, err)
		writeError(w, http.StatusInternalServerError, CodeInternal, "leave failed")
		return
	}
	l := s.links.byNode[node.ID]
	delete(s.links.byNode, node.ID)
	s.links.mu.Unlock()
	if l != nil {
		l.close()
	}
	s.local.Audit(r.Context(), PrincipalPrefix+node.ID, "cluster.leave", node.ID, http.StatusOK, map[string]any{"name": node.Name})
	s.logf("[cluster] node %s (%s) left", node.ID, node.Name)
	writeJSON(w, http.StatusOK, map[string]string{"status": NodeLeft})
	s.kick()
}

// hubRelay forwards /api/cluster/v1/relay/{nodeID}/{rest} from one node to
// another, or serves it here when the hub itself is the target.
func (s *Service) hubRelay(w http.ResponseWriter, r *http.Request) {
	origin, ok := s.authNode(w, r)
	if !ok {
		return
	}
	tail, ok := strings.CutPrefix(r.URL.EscapedPath(), HubPathPrefix+"relay/")
	if !ok {
		writeError(w, http.StatusNotFound, CodeNodeUnknown, "unknown cluster node")
		return
	}
	targetEscaped, restEscaped, _ := strings.Cut(tail, "/")
	targetID, err := url.PathUnescape(targetEscaped)
	if err != nil || !validNodeID(targetID) {
		writeError(w, http.StatusNotFound, CodeNodeUnknown, "unknown cluster node")
		return
	}
	if limit := s.maxBody(); limit > 0 {
		if r.ContentLength > limit {
			writeError(w, http.StatusRequestEntityTooLarge, CodeRequestTooLarge, "request body exceeds the cluster relay limit")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
	}
	if targetID == origin.ID {
		writeError(w, http.StatusBadRequest, CodeRelayLoop, "a node cannot relay to itself")
		return
	}
	if targetID == s.Identity().ID {
		s.serveLocal(w, r, origin.ID, origin.Name, restEscaped)
		return
	}
	target, err := s.findNode(r.Context(), targetID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		s.logf("[cluster] relay lookup %s: %v", targetID, err)
		writeError(w, http.StatusInternalServerError, CodeInternal, "node lookup failed")
		return
	}
	if err != nil || target.Status != NodeActive {
		writeError(w, http.StatusNotFound, CodeNodeUnknown, "unknown or inactive cluster node")
		return
	}
	transport, ok := s.links.RoundTripper(targetID)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, CodeNodeOffline, "cluster node is offline")
		return
	}
	outPath, outRawPath := LinkPathPrefix+"serve/"+restEscaped, ""
	if unescaped, err := url.PathUnescape(outPath); err == nil {
		outPath, outRawPath = unescaped, LinkPathPrefix+"serve/"+restEscaped
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = linkHost(targetID)
			pr.Out.URL.Path = outPath
			pr.Out.URL.RawPath = outRawPath
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			pr.Out.Host = ""
			pr.Out.Header.Del("Authorization")
			stripClusterHeaders(pr.Out.Header, HeaderModel)
			pr.Out.Header.Set(HeaderOrigin, origin.ID)
			pr.Out.Header.Set(HeaderOriginName, origin.Name)
		},
		Transport:     transport,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if r.Context().Err() == nil {
				s.logf("[cluster] relay %s -> %s: %v", origin.ID, targetID, err)
			}
			writeError(w, http.StatusBadGateway, CodeRelayFailed, "cluster relay failed")
		},
	}
	proxy.ServeHTTP(w, r)
}

func (s *Service) findNode(ctx context.Context, id string) (NodeRecord, error) {
	nodes, err := s.hubStore.Nodes(ctx)
	if err != nil {
		return NodeRecord{}, err
	}
	for _, node := range nodes {
		if node.ID == id {
			return node, nil
		}
	}
	return NodeRecord{}, ErrNotFound
}

// hubState assembles the cluster view members poll: the hub itself first,
// then every recorded node with live link and inventory data.
func (s *Service) hubState(ctx context.Context) (State, error) {
	if err := s.ensureCatalog(ctx); err != nil && ctx.Err() == nil {
		s.logf("[cluster] build catalog: %v", err)
	}
	records, err := s.hubStore.Nodes(ctx)
	if err != nil {
		return State{}, err
	}
	s.mu.Lock()
	m, self, selfInventory := s.membership, s.identity, s.selfInventory
	catalogVersion := s.catalog.Version
	inventories := maps.Clone(s.inventories)
	s.mu.Unlock()
	now := s.now().UTC()
	nodes := make([]NodeState, 0, len(records)+1)
	nodes = append(nodes, NodeState{
		ID: self.ID, Name: self.Name, Role: RoleHub, Status: NodeActive, Online: true, Version: s.version, LastSeenAt: now,
		LendingEnabled: selfInventory.Lending.Enabled, Offers: cloneOffers(selfInventory.Offers),
	})
	members := make([]NodeState, 0, len(records))
	for _, record := range records {
		if record.ID == self.ID {
			continue
		}
		node := NodeState{
			ID: record.ID, Name: record.Name, Owner: record.Owner, Role: RoleMember, Status: record.Status,
			Version: record.Version, LastSeenAt: record.LastSeenAt, Offers: []Offer{},
		}
		if link := s.links.get(record.ID); link != nil && record.Status == NodeActive {
			node.Online = true
			node.RTTMS = link.rttMS.Load()
			if inventory, ok := inventories[record.ID]; ok {
				node.LendingEnabled = inventory.Lending.Enabled
				node.Offers = cloneOffers(inventory.Offers)
				node.Version = cmp.Or(inventory.Version, node.Version)
			}
		}
		members = append(members, node)
	}
	slices.SortFunc(members, func(a, b NodeState) int {
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	state := State{
		ClusterID: m.ClusterID, ClusterName: m.ClusterName, HubNodeID: self.ID, CatalogVersion: catalogVersion,
		Nodes: append(nodes, members...), GeneratedAt: now,
	}
	state.Version = stateVersion(state)
	return state, nil
}

func (s *Service) ensureCatalog(ctx context.Context) error {
	s.mu.Lock()
	built := s.catalogBuilt
	s.mu.Unlock()
	if built {
		return nil
	}
	_, err := s.rebuildCatalog(ctx)
	return err
}

// rebuildCatalog asks the gateway for the shared catalog and reports whether
// its version changed.
func (s *Service) rebuildCatalog(ctx context.Context) (bool, error) {
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	s.mu.Lock()
	shared, gen := s.shared, s.gen
	s.mu.Unlock()
	catalog, err := s.local.BuildCatalog(ctx, shared)
	if err != nil {
		return false, err
	}
	catalog = normalizeCatalog(catalog)
	catalog.Version = catalogVersion(catalog)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen != gen || s.membership.Role != RoleHub {
		return false, nil
	}
	changed := s.catalog.Version != catalog.Version
	s.catalog = catalog
	s.catalogBuilt = true
	s.publishRouteLocked()
	return changed, nil
}

// refreshHubPeers recomputes the node list, hands peers to the gateway when
// something other than RTT or last-seen changed, and reports whether the
// state version moved.
func (s *Service) refreshHubPeers(ctx context.Context) (bool, error) {
	s.peersMu.Lock()
	defer s.peersMu.Unlock()
	if !s.isActiveHub() {
		return false, nil
	}
	state, err := s.hubState(ctx)
	if err != nil {
		return false, err
	}
	peers := make([]NodeState, 0, len(state.Nodes))
	for _, node := range state.Nodes {
		if node.ID != state.HubNodeID {
			peers = append(peers, node)
		}
	}
	key := peersKey(peers)
	s.mu.Lock()
	if s.stopped || s.membership.Role != RoleHub {
		s.mu.Unlock()
		return false, nil
	}
	gen := s.gen
	changed := s.hubStateVer != state.Version
	s.hubStateVer = state.Version
	s.peers = peers
	apply := key != s.appliedPeers
	s.publishRouteLocked()
	s.mu.Unlock()
	if !apply {
		return changed, nil
	}
	if err := s.local.ApplyPeers(ctx, peers); err != nil {
		return changed, fmt.Errorf("apply peers: %w", err)
	}
	s.mu.Lock()
	if s.gen == gen {
		s.appliedPeers = key
	}
	s.mu.Unlock()
	return changed, nil
}

func (s *Service) refreshAndNotify(ctx context.Context) {
	changed, err := s.refreshHubPeers(ctx)
	if err != nil && ctx.Err() == nil {
		s.logf("[cluster] refresh peers: %v", err)
	}
	if changed {
		s.notifyMembers("state")
	}
}

func (s *Service) hubLoop(run *roleRun) {
	defer run.wg.Done()
	// Links outlive no hub loop: this covers the base context ending without
	// Stop as well as role changes.
	defer s.closeLinks()
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		if s.isActiveHub() {
			ctx, cancel := context.WithTimeout(run.ctx, syncTimeout)
			err := s.pollHub(ctx)
			cancel()
			if err != nil && run.ctx.Err() == nil {
				s.logf("[cluster] hub poll: %v", err)
			}
		}
		select {
		case <-run.ctx.Done():
			return
		case <-ticker.C:
		case <-run.kick:
		}
	}
}

// pollHub runs one hub cycle: rebuild the catalog, collect every member's
// inventory over its link, refresh the hub's own entry and the peers, and
// tell members to poll when anything they see changed.
func (s *Service) pollHub(ctx context.Context) error {
	if err := s.pollMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.pollMu.Unlock()
	if !s.isActiveHub() {
		return newError(http.StatusConflict, CodeNotConfigured, "this node is not a cluster hub")
	}
	var firstErr error
	catalogChanged, err := s.rebuildCatalog(ctx)
	if err != nil {
		firstErr = fmt.Errorf("build catalog: %w", err)
	}
	var wg sync.WaitGroup
	for _, l := range s.links.all() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.pollInventory(ctx, l)
		}()
	}
	self := s.buildInventory(ctx)
	wg.Wait()
	s.mu.Lock()
	if s.membership.Role == RoleHub {
		s.selfInventory = self
	}
	s.mu.Unlock()
	if len(self.Ledger) > 0 {
		if err := s.hubStore.UpsertLedger(ctx, self.Ledger); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("record ledger: %w", err)
		}
	}
	stateChanged, err := s.refreshHubPeers(ctx)
	if err != nil && firstErr == nil {
		firstErr = err
	}
	switch {
	case catalogChanged:
		s.notifyMembers("catalog")
	case stateChanged:
		s.notifyMembers("state")
	}
	return firstErr
}

func (s *Service) pollInventory(ctx context.Context, l *hubLink) {
	ctx, cancel := context.WithTimeout(ctx, inventoryTimeout)
	defer cancel()
	resp, err := s.linkRequest(ctx, l, http.MethodGet, "inventory", nil)
	if err != nil {
		if ctx.Err() == nil {
			s.logf("[cluster] inventory from node %s: %v", l.nodeID, err)
		}
		return
	}
	defer drain(resp.Body)
	if resp.StatusCode != http.StatusOK {
		s.logf("[cluster] inventory from node %s: status %d", l.nodeID, resp.StatusCode)
		return
	}
	var inventory Inventory
	if err := decodeBody(resp.Body, 8<<20, &inventory); err != nil {
		s.logf("[cluster] inventory from node %s: %v", l.nodeID, err)
		return
	}
	// The link, not the payload, says which node this is.
	inventory.NodeID = l.nodeID
	inventory.Lending = normalizeLending(inventory.Lending)
	inventory.Offers = cloneOffers(inventory.Offers)
	if len(inventory.Ledger) > maxLedgerRows {
		inventory.Ledger = inventory.Ledger[:maxLedgerRows]
	}
	for i := range inventory.Ledger {
		inventory.Ledger[i].Lender = l.nodeID
	}
	s.mu.Lock()
	if s.membership.Role == RoleHub && s.links.get(l.nodeID) == l {
		s.inventories[l.nodeID] = inventory
	}
	s.mu.Unlock()
	if err := s.hubStore.TouchNode(ctx, l.nodeID, l.addr, inventory.Version, s.now()); err != nil {
		s.logf("[cluster] touch node %s: %v", l.nodeID, err)
	}
	if len(inventory.Ledger) > 0 {
		if err := s.hubStore.UpsertLedger(ctx, inventory.Ledger); err != nil {
			s.logf("[cluster] record ledger from node %s: %v", l.nodeID, err)
		}
	}
}
