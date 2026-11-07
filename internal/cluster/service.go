package cluster

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tproxy/tproxy/internal/config"
)

type Service struct {
	settings SettingsStore
	hubStore HubStore
	local    Local
	version  string
	client   *http.Client
	logf     func(string, ...any)
	now      func() time.Time
	maxBody  func() int64
	h2       *http.Transport // hub side of every link

	pollInterval time.Duration
	pingInterval time.Duration
	pingTimeout  time.Duration
	minBackoff   time.Duration
	maxBackoff   time.Duration
	stableLink   time.Duration

	opMu      contextMutex // admin operations, Sync and revocation bookkeeping
	loadMu    sync.Mutex
	pollMu    contextMutex // one poll cycle at a time
	peersMu   sync.Mutex   // hub peer recomputation
	catalogMu sync.Mutex   // hub catalog rebuilds

	mu         sync.Mutex
	loaded     bool
	started    bool
	stopped    bool
	dissolving bool
	stopCh     chan struct{}
	baseCtx    context.Context
	run        *roleRun
	cleanupRun *roleRun
	// gen changes with every membership change; work that started under an
	// older generation drops its result.
	gen            uint64
	identity       NodeIdentity
	membership     Membership
	lending        LendingPolicy
	borrowing      BorrowingPolicy
	shared         SharedSelection
	catalog        Catalog
	catalogBuilt   bool
	report         ApplyReport
	appliedCatalog string
	state          State
	stateETag      string
	hubStateVer    string
	peers          []NodeState
	appliedPeers   string
	peersOffline   bool
	failures       int
	inventories    map[string]Inventory
	selfInventory  Inventory
	link           linkStatus

	links  linkRegistry
	linkWG sync.WaitGroup
	bgWG   sync.WaitGroup

	route atomic.Pointer[routeSnapshot]
}

type linkStatus struct {
	State       string
	ConnectedAt time.Time
	LastError   string
	RTTMS       int64
}

// roleRun owns the loops of one role for one membership; a role change
// cancels it and starts a new one.
type roleRun struct {
	role   Role
	gen    uint64 // membership generation the run was started for
	ctx    context.Context
	cancel context.CancelFunc
	kick   chan struct{}
	wg     sync.WaitGroup
}

func (r *roleRun) poke() {
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

type routeSnapshot struct {
	mode    string
	models  map[string]routeModel
	offered map[string]bool
}

type routeModel struct {
	id      string
	pricing *config.PricingConfig
}

func New(opts Options) *Service {
	s := &Service{
		settings:     opts.Settings,
		opMu:         make(contextMutex, 1),
		pollMu:       make(contextMutex, 1),
		stopCh:       make(chan struct{}),
		hubStore:     opts.Hub,
		local:        opts.Local,
		version:      opts.Version,
		client:       opts.HTTPClient,
		logf:         opts.Logf,
		now:          opts.Now,
		maxBody:      opts.MaxRelayBodyBytes,
		pollInterval: opts.PollInterval,
		pingInterval: opts.PingInterval,
		pingTimeout:  opts.PingTimeout,
		minBackoff:   time.Second,
		maxBackoff:   time.Minute,
		stableLink:   time.Minute,
		membership:   Membership{Role: RoleStandalone},
		lending:      DefaultLendingPolicy(),
		borrowing:    DefaultBorrowingPolicy(),
		shared:       normalizeShared(SharedSelection{}),
		inventories:  map[string]Inventory{},
		link:         linkStatus{State: LinkOffline},
		links:        linkRegistry{byNode: map[string]*hubLink{}},
	}
	if s.client == nil {
		s.client = newHTTPClient()
	}
	if s.logf == nil {
		s.logf = log.Printf
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.maxBody == nil {
		s.maxBody = func() int64 { return 0 }
	}
	if s.pollInterval <= 0 {
		s.pollInterval = 15 * time.Second
	}
	if s.pingInterval <= 0 {
		s.pingInterval = 20 * time.Second
	}
	if s.pingTimeout <= 0 {
		s.pingTimeout = 10 * time.Second
	}
	s.h2 = newLinkTransport(s.pingInterval, s.pingTimeout)
	return s
}

func newHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          32,
			MaxIdleConnsPerHost:   8,
			IdleConnTimeout:       90 * time.Second,
			ExpectContinueTimeout: time.Second,
			// Relayed streams must reach the caller as they arrive, not
			// behind a transparent gzip reader.
			DisableCompression: true,
		},
		// Following a redirect would hand the node token to wherever it points.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Start loads the persisted settings and runs the loops for the current role
// until Stop or until ctx ends. It is idempotent.
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started || s.stopped {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.baseCtx = ctx
	s.mu.Unlock()
	if err := s.ensureLoaded(ctx); err != nil {
		s.logf("[cluster] load settings: %v", err)
		return
	}
	s.mu.Lock()
	s.startRunLocked()
	s.mu.Unlock()
}

// Stop ends every loop and link and waits for them. It is idempotent.
func (s *Service) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	close(s.stopCh)
	run := s.run
	s.run = nil
	if s.membership.Role == RoleMember && !s.membership.Revoked {
		s.link = linkStatus{State: LinkOffline}
	}
	s.mu.Unlock()
	if run != nil {
		run.cancel()
		run.wg.Wait()
	}
	s.closeLinks()
	s.linkWG.Wait()
	s.bgWG.Wait()
}

func (s *Service) Role() Role {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.membership.Role
}

// Identity returns this node's identity once settings are loaded.
func (s *Service) Identity() NodeIdentity {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.identity
}

// Lending returns the current lending policy without I/O, for the code that
// applies its limits to relayed requests.
func (s *Service) Lending() LendingPolicy {
	s.mu.Lock()
	defer s.mu.Unlock()
	return normalizeLending(s.lending)
}

// ActiveMember reports, without I/O, whether this node is a member the hub has
// not revoked. Until settings load it reports false, so callers fail closed.
func (s *Service) ActiveMember() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.membership.Role == RoleMember && !s.membership.Revoked
}

// CatalogIDs returns, without I/O, the ids of the enabled models and combos in
// the catalog this node holds, leaving out entries it could not apply. The
// gateway limits requests relayed from other nodes to these ids.
func (s *Service) CatalogIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.membership.Role != RoleHub && (s.membership.Role != RoleMember || s.membership.Revoked) {
		return []string{}
	}
	conflicted := map[string]bool{}
	for _, conflict := range s.report.Conflicts {
		conflicted[conflict.Kind+":"+conflict.ID] = true
	}
	ids := make([]string, 0, len(s.catalog.Models)+len(s.catalog.Combos))
	for _, model := range s.catalog.Models {
		if model.Enabled && !conflicted["model:"+model.ID] {
			ids = append(ids, model.ID)
		}
	}
	for _, combo := range s.catalog.Combos {
		if combo.Enabled && !conflicted["combo:"+combo.ID] {
			ids = append(ids, combo.ID)
		}
	}
	return ids
}

func (s *Service) ensureLoaded(ctx context.Context) error {
	s.mu.Lock()
	loaded := s.loaded
	s.mu.Unlock()
	if loaded {
		return nil
	}
	s.loadMu.Lock()
	defer s.loadMu.Unlock()
	s.mu.Lock()
	loaded = s.loaded
	s.mu.Unlock()
	if loaded {
		return nil
	}
	identity, err := s.settings.NodeIdentity(ctx)
	if err != nil {
		return fmt.Errorf("load node identity: %w", err)
	}
	membership, err := s.settings.Membership(ctx)
	if err != nil {
		return fmt.Errorf("load cluster membership: %w", err)
	}
	lending, saved, err := s.settings.Lending(ctx)
	if err != nil {
		return fmt.Errorf("load lending policy: %w", err)
	}
	if !saved {
		lending = DefaultLendingPolicy()
	}
	borrowing, saved, err := s.settings.Borrowing(ctx)
	if err != nil {
		return fmt.Errorf("load borrowing policy: %w", err)
	}
	if !saved {
		borrowing = DefaultBorrowingPolicy()
	}
	shared, err := s.settings.Shared(ctx)
	if err != nil {
		return fmt.Errorf("load shared selection: %w", err)
	}
	if membership.Role != RoleHub && membership.Role != RoleMember {
		membership = Membership{Role: RoleStandalone}
	}
	s.mu.Lock()
	s.identity = identity
	s.membership = membership
	s.lending = normalizeLending(lending)
	s.borrowing = normalizeBorrowing(borrowing)
	s.shared = normalizeShared(shared)
	s.loaded = true
	s.gen++
	if membership.Role == RoleMember && membership.Revoked {
		s.link = linkStatus{State: LinkRevoked}
	}
	s.startRunLocked()
	s.publishRouteLocked()
	gen := s.gen
	s.mu.Unlock()
	if membership.Role == RoleMember && membership.Revoked {
		s.goBackground(func() { s.retryRevokedCleanup(membership, gen) })
	}
	return nil
}

func (s *Service) startRunLocked() {
	if !s.started || s.stopped || !s.loaded || s.run != nil {
		return
	}
	m := s.membership
	if m.Role != RoleHub && (m.Role != RoleMember || m.Revoked) {
		return
	}
	ctx, cancel := context.WithCancel(s.baseCtx)
	run := &roleRun{role: m.Role, gen: s.gen, ctx: ctx, cancel: cancel, kick: make(chan struct{}, 1)}
	s.run = run
	if m.Role == RoleHub {
		run.wg.Add(1)
		go s.hubLoop(run)
		return
	}
	s.link = linkStatus{State: LinkConnecting}
	run.wg.Add(2)
	go s.memberLinkLoop(run)
	go s.memberStateLoop(run)
}

// stopRun cancels the current role loops and waits for them; it must not be
// called from one of those loops.
func (s *Service) stopRun() {
	s.mu.Lock()
	run := s.run
	s.run = nil
	s.mu.Unlock()
	if run == nil {
		return
	}
	run.cancel()
	run.wg.Wait()
}

// waitRevokedRun prevents cleanup from racing a catalog apply from the old run.
func (s *Service) waitRevokedRun() {
	s.mu.Lock()
	run := s.cleanupRun
	s.mu.Unlock()
	if run == nil {
		return
	}
	run.wg.Wait()
	s.mu.Lock()
	if s.cleanupRun == run {
		s.cleanupRun = nil
	}
	s.mu.Unlock()
}

func (s *Service) currentRun() *roleRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.run
}

// kick asks the current role loop for an immediate poll without waiting.
func (s *Service) kick() {
	if run := s.currentRun(); run != nil {
		run.poke()
	}
}

// goBackground runs fn on a goroutine that Stop waits for. Nothing starts
// once the service is stopping.
func (s *Service) goBackground(fn func()) bool {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return false
	}
	s.bgWG.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.bgWG.Done()
		fn()
	}()
	return true
}

func (s *Service) resetClusterStateLocked() {
	s.catalog = Catalog{}
	s.catalogBuilt = false
	s.report = ApplyReport{}
	s.appliedCatalog = ""
	s.state = State{}
	s.stateETag = ""
	s.hubStateVer = ""
	s.peers = nil
	s.appliedPeers = ""
	s.peersOffline = false
	s.failures = 0
	s.inventories = map[string]Inventory{}
	s.selfInventory = Inventory{}
}

// publishRouteLocked rebuilds what ClusterRoute answers from, so the request
// path never takes a lock or does I/O.
func (s *Service) publishRouteLocked() {
	m := s.membership
	if (m.Role != RoleHub && m.Role != RoleMember) || m.Revoked || !s.borrowing.Enabled {
		s.route.Store(nil)
		return
	}
	conflicted := map[string]bool{}
	for _, conflict := range s.report.Conflicts {
		conflicted[conflict.Kind+":"+conflict.ID] = true
	}
	snap := &routeSnapshot{mode: s.borrowing.Mode, models: map[string]routeModel{}, offered: map[string]bool{}}
	for _, model := range s.catalog.Models {
		if !model.Enabled || conflicted["model:"+model.ID] {
			continue
		}
		snap.models[model.ID] = routeModel{id: model.ID, pricing: model.Pricing}
	}
	for _, model := range s.catalog.Models {
		entry, ok := snap.models[model.ID]
		if !ok {
			continue
		}
		for _, alias := range model.Aliases {
			if _, taken := snap.models[alias]; !taken && !conflicted["alias:"+alias] {
				snap.models[alias] = entry
			}
		}
	}
	if !s.peersOffline {
		for _, node := range s.peers {
			if node.ID == s.identity.ID || !node.Online || node.Status != NodeActive || !node.LendingEnabled {
				continue
			}
			for _, offer := range node.Offers {
				if offer.EligibleAccounts > 0 {
					snap.offered[offer.ModelID] = true
				}
			}
		}
	}
	s.route.Store(snap)
}

// ClusterRoute reports whether the router should add a virtual cluster route
// for modelID, with the borrowing mode and the catalog price.
func (s *Service) ClusterRoute(modelID string) (mode string, pricing *config.PricingConfig, ok bool) {
	snap := s.route.Load()
	if snap == nil {
		return "", nil, false
	}
	model, found := snap.models[modelID]
	if !found || !snap.offered[model.id] {
		return "", nil, false
	}
	if model.pricing != nil {
		price := *model.pricing
		pricing = &price
	}
	return snap.mode, pricing, true
}

// Do sends req to nodeID's gateway and returns the response unbuffered; the
// caller closes its body. req supplies Method, URL.Path, URL.RawQuery, Header
// and Body. Conditions the caller should treat like an upstream error
// (offline node, no cluster) come back as synthetic 503 JSON responses.
func (s *Service) Do(ctx context.Context, nodeID string, req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		closeRequestBody(req)
		return nil, errors.New("cluster: request URL is required")
	}
	if err := s.ensureLoaded(ctx); err != nil {
		closeRequestBody(req)
		return nil, err
	}
	s.mu.Lock()
	m, self := s.membership, s.identity
	s.mu.Unlock()
	if (m.Role != RoleHub && m.Role != RoleMember) || m.Revoked {
		closeRequestBody(req)
		return syntheticResponse(req, http.StatusServiceUnavailable, CodeNotConfigured, "this node is not an active cluster member"), nil
	}
	nodeID = strings.TrimSpace(nodeID)
	if !validNodeID(nodeID) || nodeID == self.ID {
		closeRequestBody(req)
		return nil, fmt.Errorf("cluster: cannot relay a request to node %q", nodeID)
	}
	path := req.URL.EscapedPath()
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	query := ""
	if req.URL.RawQuery != "" {
		query = "?" + req.URL.RawQuery
	}
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	if m.Role == RoleHub {
		link := s.links.get(nodeID)
		if link == nil {
			closeRequestBody(req)
			return syntheticResponse(req, http.StatusServiceUnavailable, CodeNodeOffline, "cluster node is offline"), nil
		}
		out, err := http.NewRequestWithContext(ctx, method, "http://"+linkHost(nodeID)+LinkPathPrefix+"serve"+path+query, nil)
		if err != nil {
			closeRequestBody(req)
			return nil, err
		}
		attachBody(out, req)
		out.Header = forwardHeaders(req.Header)
		out.Header.Set(HeaderOrigin, self.ID)
		out.Header.Set(HeaderOriginName, self.Name)
		return link.RoundTrip(out)
	}
	out, err := http.NewRequestWithContext(ctx, method, m.HubURL+HubPathPrefix+"relay/"+url.PathEscape(nodeID)+path+query, nil)
	if err != nil {
		closeRequestBody(req)
		return nil, err
	}
	attachBody(out, req)
	out.Header = forwardHeaders(req.Header)
	s.authorizeHubRequest(out, m, self.ID)
	resp, err := s.client.Do(out)
	if err != nil {
		return nil, err
	}
	if revokedResponse(resp.StatusCode, resp.Header) {
		// The state poll owns revocation handling; let it confirm now.
		s.kick()
	}
	return resp, nil
}

func attachBody(out, in *http.Request) {
	if in.Body == nil || in.Body == http.NoBody {
		return
	}
	out.Body = in.Body
	out.ContentLength = in.ContentLength
	out.GetBody = in.GetBody
}

func (s *Service) authorizeHubRequest(req *http.Request, m Membership, selfID string) {
	if m.NodeToken != "" {
		req.Header.Set("Authorization", "Bearer "+m.NodeToken)
	}
	req.Header.Set(HeaderProtocol, ProtocolVersion)
	if selfID != "" {
		req.Header.Set(HeaderNode, selfID)
	}
	req.Header.Set("User-Agent", "tproxy-cluster/"+s.version)
}

func (s *Service) newHubRequest(ctx context.Context, m Membership, method, path string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, m.HubURL+HubPathPrefix+path, nil)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	selfID := s.identity.ID
	s.mu.Unlock()
	s.authorizeHubRequest(req, m, selfID)
	return req, nil
}

// buildInventory describes what this node lends right now. The hub calls it
// for its own entry; members serve it over the link.
func (s *Service) buildInventory(ctx context.Context) Inventory {
	s.mu.Lock()
	self, lending, catalog := s.identity, normalizeLending(s.lending), s.catalog
	conflicted := map[string]bool{}
	for _, conflict := range s.report.Conflicts {
		if conflict.Kind == "model" {
			conflicted[conflict.ID] = true
		}
	}
	s.mu.Unlock()
	inventory := Inventory{NodeID: self.ID, Name: self.Name, Version: s.version, Lending: lending, Offers: []Offer{}, Ledger: []LedgerRow{}}
	if lending.Enabled {
		allowed := map[string]bool{}
		for _, id := range lending.Models {
			allowed[id] = true
		}
		for _, model := range catalog.Models {
			if !model.Enabled || conflicted[model.ID] || (len(allowed) > 0 && !allowed[model.ID]) {
				continue
			}
			count, err := s.local.LocalCapacity(ctx, model.ID)
			if err != nil {
				if ctx.Err() == nil {
					s.logf("[cluster] capacity for %s: %v", model.ID, err)
				}
				count = 0
			}
			count = max(count, 0)
			inventory.Offers = append(inventory.Offers, Offer{ModelID: model.ID, EligibleAccounts: count, Healthy: count > 0})
		}
	}
	rows, err := s.local.LendingLedger(ctx, sinceDay(s.now(), defaultLedgerDays))
	if err != nil && ctx.Err() == nil {
		s.logf("[cluster] lending ledger: %v", err)
	}
	for _, row := range rows {
		row.Lender = self.ID
		inventory.Ledger = append(inventory.Ledger, row)
	}
	return inventory
}

// serveLocal hands a request relayed from another node to the local gateway.
func (s *Service) serveLocal(w http.ResponseWriter, r *http.Request, originID, originName, restEscaped string) {
	lending := s.Lending()
	if !lending.Enabled {
		writeError(w, http.StatusForbidden, CodeLendingDisabled, "this node does not lend capacity")
		return
	}
	if model := strings.TrimSpace(r.Header.Get(HeaderModel)); model != "" && len(lending.Models) > 0 && !slices.Contains(lending.Models, model) {
		writeError(w, http.StatusForbidden, CodeModelNotLent, "this node does not lend "+model)
		return
	}
	path, rawPath, ok := relayPath(restEscaped)
	if !ok {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid relay path")
		return
	}
	out := r.Clone(WithOrigin(r.Context(), originID, originName))
	out.URL.Path = path
	out.URL.RawPath = rawPath
	out.RequestURI = out.URL.RequestURI()
	out.Header.Del("Authorization")
	stripClusterHeaders(out.Header)
	s.local.ServeRelayed().ServeHTTP(w, out)
}
