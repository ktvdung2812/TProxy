package cluster

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memSettings struct {
	mu                   sync.Mutex
	identity             NodeIdentity
	membership           Membership
	lending              LendingPolicy
	lendingSaved         bool
	borrowing            BorrowingPolicy
	borrowingSaved       bool
	shared               SharedSelection
	noMasterKey          bool
	clearMembershipFails bool
	membershipSaves      int
}

func (m *memSettings) NodeIdentity(context.Context) (NodeIdentity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.identity, nil
}

func (m *memSettings) SaveNodeIdentity(_ context.Context, id NodeIdentity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.identity = id
	return nil
}

func (m *memSettings) Membership(context.Context) (Membership, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.membership, nil
}

func (m *memSettings) SaveMembership(_ context.Context, ms Membership) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.noMasterKey && ms.NodeToken != "" {
		return fmt.Errorf("encrypt node token: %w", ErrMasterKeyRequired)
	}
	m.membership = ms
	m.membershipSaves++
	return nil
}

func (m *memSettings) ClearMembership(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.clearMembershipFails {
		return errors.New("clear membership failed")
	}
	m.membership = Membership{}
	return nil
}

func (m *memSettings) Lending(context.Context) (LendingPolicy, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lending, m.lendingSaved, nil
}

func (m *memSettings) SaveLending(_ context.Context, p LendingPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lending, m.lendingSaved = p, true
	return nil
}

func (m *memSettings) Borrowing(context.Context) (BorrowingPolicy, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.borrowing, m.borrowingSaved, nil
}

func (m *memSettings) SaveBorrowing(_ context.Context, p BorrowingPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.borrowing, m.borrowingSaved = p, true
	return nil
}

func (m *memSettings) Shared(context.Context) (SharedSelection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.shared, nil
}

func (m *memSettings) SaveShared(_ context.Context, s SharedSelection) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shared = s
	return nil
}

func (m *memSettings) current() Membership {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.membership
}

type tokenEntry struct {
	record JoinTokenRecord
	hash   string
}

type nodeEntry struct {
	record NodeRecord
	hash   string
}

type memHub struct {
	mu     sync.Mutex
	tokens []*tokenEntry
	nodes  map[string]*nodeEntry
	ledger map[string]LedgerRow
}

func newMemHub() *memHub {
	return &memHub{nodes: map[string]*nodeEntry{}, ledger: map[string]LedgerRow{}}
}

func (h *memHub) CreateJoinToken(_ context.Context, rec JoinTokenRecord, hash string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tokens = append(h.tokens, &tokenEntry{record: rec, hash: hash})
	return nil
}

func (h *memHub) ConsumeJoinToken(_ context.Context, hash string, now time.Time) (JoinTokenRecord, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, token := range h.tokens {
		if token.hash != hash {
			continue
		}
		if !token.record.RevokedAt.IsZero() || token.record.Uses >= token.record.MaxUses {
			return JoinTokenRecord{}, ErrJoinTokenInvalid
		}
		if !now.Before(token.record.ExpiresAt) {
			return JoinTokenRecord{}, ErrJoinTokenExpired
		}
		token.record.Uses++
		return token.record, nil
	}
	return JoinTokenRecord{}, ErrJoinTokenInvalid
}

func (h *memHub) JoinTokens(context.Context) ([]JoinTokenRecord, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []JoinTokenRecord
	for _, token := range h.tokens {
		out = append(out, token.record)
	}
	return out, nil
}

func (h *memHub) RevokeJoinToken(_ context.Context, id string, at time.Time) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, token := range h.tokens {
		if token.record.ID == id {
			token.record.RevokedAt = at
			return nil
		}
	}
	return ErrNotFound
}

func (h *memHub) SaveNode(_ context.Context, rec NodeRecord, hash string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry := h.nodes[rec.ID]
	if entry == nil {
		entry = &nodeEntry{}
		h.nodes[rec.ID] = entry
	}
	entry.record = rec
	if hash != "" {
		entry.hash = hash
	}
	return nil
}

func (h *memHub) NodeByTokenHash(_ context.Context, hash string) (NodeRecord, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, entry := range h.nodes {
		if entry.hash == hash {
			return entry.record, nil
		}
	}
	return NodeRecord{}, ErrNotFound
}

func (h *memHub) Nodes(context.Context) ([]NodeRecord, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]NodeRecord, 0, len(h.nodes))
	for _, entry := range h.nodes {
		out = append(out, entry.record)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (h *memHub) UpdateNode(_ context.Context, id string, name, owner *string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry := h.nodes[id]
	if entry == nil {
		return ErrNotFound
	}
	if name != nil {
		entry.record.Name = *name
	}
	if owner != nil {
		entry.record.Owner = *owner
	}
	return nil
}

func (h *memHub) SetNodeStatus(_ context.Context, id, status string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry := h.nodes[id]
	if entry == nil {
		return ErrNotFound
	}
	entry.record.Status = status
	return nil
}

func (h *memHub) TouchNode(_ context.Context, id, addr, version string, at time.Time) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry := h.nodes[id]
	if entry == nil {
		return ErrNotFound
	}
	entry.record.LastAddr, entry.record.LastSeenAt = addr, at
	if version != "" {
		entry.record.Version = version
	}
	return nil
}

func (h *memHub) UpsertLedger(_ context.Context, rows []LedgerRow) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, row := range rows {
		h.ledger[row.Day+"|"+row.Lender+"|"+row.Borrower] = row
	}
	return nil
}

func (h *memHub) Ledger(_ context.Context, since string) ([]LedgerRow, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []LedgerRow
	for _, row := range h.ledger {
		if row.Day >= since {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Lender+out[i].Borrower < out[j].Lender+out[j].Borrower })
	return out, nil
}

func (h *memHub) ResetHub(context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tokens, h.nodes, h.ledger = nil, map[string]*nodeEntry{}, map[string]LedgerRow{}
	return nil
}

func (h *memHub) node(id string) (nodeEntry, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry := h.nodes[id]
	if entry == nil {
		return nodeEntry{}, false
	}
	return *entry, true
}

type fakeLocal struct {
	mu             sync.Mutex
	models         []CatalogModel
	capacity       map[string]int
	ledger         []LedgerRow
	handler        http.Handler
	applied        []Catalog
	peers          [][]NodeState
	cleared        int
	clearFailures  int
	clearSuccesses int
	audits         []string
}

func newFakeLocal() *fakeLocal {
	return &fakeLocal{capacity: map[string]int{}}
}

func (f *fakeLocal) LocalCapacity(_ context.Context, model string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.capacity[model], nil
}

func (f *fakeLocal) LendingLedger(context.Context, string) ([]LedgerRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.ledger), nil
}

func (f *fakeLocal) BuildCatalog(context.Context, SharedSelection) (Catalog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return Catalog{Models: slices.Clone(f.models)}, nil
}

func (f *fakeLocal) ApplyCatalog(_ context.Context, c Catalog) (ApplyReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, c)
	report := ApplyReport{Version: c.Version}
	for _, model := range c.Models {
		report.Models = append(report.Models, model.ID)
	}
	return report, nil
}

func (f *fakeLocal) ApplyPeers(_ context.Context, peers []NodeState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.peers = append(f.peers, slices.Clone(peers))
	return nil
}

func (f *fakeLocal) ServeRelayed() http.Handler {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.handler == nil {
		return http.NotFoundHandler()
	}
	return f.handler
}

func (f *fakeLocal) ClearClusterData(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared++
	if f.clearFailures > 0 {
		f.clearFailures--
		return errors.New("cleanup failed")
	}
	f.clearSuccesses++
	return nil
}

func (f *fakeLocal) Audit(_ context.Context, actor, action, resourceID string, status int, _ map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audits = append(f.audits, action+" "+actor+" "+resourceID)
}

func (f *fakeLocal) setHandler(h http.Handler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handler = h
}

func (f *fakeLocal) setModels(models ...CatalogModel) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.models = models
}

func (f *fakeLocal) setCapacity(model string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.capacity[model] = n
}

func (f *fakeLocal) appliedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.applied)
}

func (f *fakeLocal) clearedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cleared
}

func (f *fakeLocal) lastPeers() []NodeState {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.peers) == 0 {
		return nil
	}
	return f.peers[len(f.peers)-1]
}

func (f *fakeLocal) auditActions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.audits)
}

type testClock struct{ offset atomic.Int64 }

func (c *testClock) Now() time.Time { return time.Now().Add(time.Duration(c.offset.Load())) }

func (c *testClock) advance(d time.Duration) { c.offset.Add(int64(d)) }

// requestCounter counts hub requests by path when they arrive, and by path
// and status once answered; a client may act on an answer before the second
// count lands.
type requestCounter struct {
	mu     sync.Mutex
	counts map[string]int
	// waf answers every request with a bare 403, like a firewall in front of
	// the hub would.
	waf atomic.Bool
}

func (c *requestCounter) add(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts == nil {
		c.counts = map[string]int{}
	}
	c.counts[key]++
}

func (c *requestCounter) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.add(r.URL.Path)
		if c.waf.Load() {
			http.Error(w, "blocked", http.StatusForbidden)
			return
		}
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		c.add(fmt.Sprintf("%s %d", r.URL.Path, recorder.status))
	})
}

func (c *requestCounter) count(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[key]
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Hijack is forwarded explicitly because gorilla's upgrader type-asserts it.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	r.status = http.StatusSwitchingProtocols
	return r.ResponseWriter.(http.Hijacker).Hijack()
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

type testNode struct {
	svc      *Service
	settings *memSettings
	hub      *memHub
	local    *fakeLocal
	clock    *testClock
	server   *httptest.Server
	requests *requestCounter
}

// testLogf keeps background goroutines from logging into a finished test.
func testLogf(t *testing.T) func(string, ...any) {
	var mu sync.Mutex
	done := false
	t.Cleanup(func() {
		mu.Lock()
		done = true
		mu.Unlock()
	})
	return func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		if !done {
			t.Logf(format, args...)
		}
	}
}

func newTestNode(t *testing.T, id, name string, tune ...func(*Service)) *testNode {
	t.Helper()
	n := &testNode{
		settings: &memSettings{identity: NodeIdentity{ID: id, Name: name}},
		hub:      newMemHub(),
		local:    newFakeLocal(),
		clock:    &testClock{},
		requests: &requestCounter{},
	}
	n.svc = New(Options{
		Settings: n.settings, Hub: n.hub, Local: n.local, Version: "test-" + id,
		Logf: testLogf(t), Now: n.clock.Now,
		PollInterval: 50 * time.Millisecond, PingInterval: 100 * time.Millisecond, PingTimeout: 2 * time.Second,
	})
	n.svc.minBackoff = 20 * time.Millisecond
	n.svc.maxBackoff = 200 * time.Millisecond
	for _, fn := range tune {
		fn(n.svc)
	}
	n.server = httptest.NewServer(n.requests.wrap(n.svc.Handler()))
	n.svc.Start(context.Background())
	t.Cleanup(func() {
		n.svc.Stop()
		n.server.Close()
	})
	return n
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func nodeView(t *testing.T, svc *Service, id string) (StatusNodeView, bool) {
	t.Helper()
	view, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, node := range view.Nodes {
		if node.ID == id {
			return node, true
		}
	}
	return StatusNodeView{}, false
}

func nodeOnline(t *testing.T, svc *Service, id string) bool {
	node, ok := nodeView(t, svc, id)
	return ok && node.Online
}
