package cluster

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/config"
)

func newHub(t *testing.T, tune ...func(*Service)) *testNode {
	t.Helper()
	hub := newTestNode(t, "hub-node", "Hub", tune...)
	hub.local.setModels(CatalogModel{ID: "gpt-test", DisplayName: "GPT Test", Enabled: true, Pricing: &config.PricingConfig{InputPerMillion: 1}})
	hub.local.setCapacity("gpt-test", 2)
	if err := hub.svc.CreateCluster(context.Background(), CreateInput{Name: "Test cluster", HubURL: hub.server.URL, AllowInsecure: true}); err != nil {
		t.Fatal(err)
	}
	return hub
}

func joinMember(t *testing.T, hub *testNode, id, name string, tune ...func(*Service)) *testNode {
	t.Helper()
	token, _, err := hub.svc.CreateJoinToken(context.Background(), "for "+name, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	member := newTestNode(t, id, name, tune...)
	member.local.setCapacity("gpt-test", 1)
	if err := member.svc.Join(context.Background(), JoinInput{Token: token, AllowInsecure: true}); err != nil {
		t.Fatal(err)
	}
	eventually(t, name+" online at the hub", func() bool { return nodeOnline(t, hub.svc, id) })
	return member
}

func errorCode(err error) string {
	var clusterErr *Error
	if errors.As(err, &clusterErr) {
		return clusterErr.Code
	}
	return ""
}

func relayRequest(t *testing.T, target string, body string) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(http.MethodPost, target, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

type relayed struct {
	originID, originName, uri, authorization string
	clusterHeaders                           []string
}

// sseHandler writes three events and waits for the test before each event
// after the first, so a client that sees an event early proves streaming.
func sseHandler(release <-chan struct{}, seen chan<- relayed) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, name, _ := OriginFromContext(r.Context())
		info := relayed{originID: id, originName: name, uri: r.URL.RequestURI(), authorization: r.Header.Get("Authorization")}
		for key := range r.Header {
			if strings.HasPrefix(http.CanonicalHeaderKey(key), "X-Tproxy-Cluster-") {
				info.clusterHeaders = append(info.clusterHeaders, key)
			}
		}
		select {
		case seen <- info:
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for i := 1; i <= 3; i++ {
			if i > 1 {
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			fmt.Fprintf(w, "data: chunk-%d\n\n", i)
			flusher.Flush()
		}
	})
}

func expectSSE(t *testing.T, resp *http.Response, release chan<- struct{}) {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	reader := bufio.NewReader(resp.Body)
	for i := 1; i <= 3; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("reading event %d: %v", i, err)
		}
		if want := fmt.Sprintf("data: chunk-%d\n", i); line != want {
			t.Fatalf("event %d = %q, want %q", i, line, want)
		}
		if _, err := reader.ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		if i < 3 {
			release <- struct{}{}
		}
	}
}

func testContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestJoinStoresHashesAndEnforcesTokenLimits(t *testing.T) {
	ctx := testContext(t)
	hub := newHub(t)
	token, record, err := hub.svc.CreateJoinToken(ctx, "laptop", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if record.MaxUses != 1 || record.ExpiresAt.Sub(record.CreatedAt) != 24*time.Hour || record.ID == "" {
		t.Fatalf("token record = %+v", record)
	}
	decoded, err := DecodeJoinToken(token)
	if err != nil || decoded.HubURL != hub.server.URL {
		t.Fatalf("decoded = %+v, %v", decoded, err)
	}
	hub.hub.mu.Lock()
	storedHash := hub.hub.tokens[0].hash
	hub.hub.mu.Unlock()
	if storedHash != HashSecret(decoded.Secret) || storedHash == decoded.Secret {
		t.Fatal("join secret must be stored only as its hash")
	}

	a := newTestNode(t, "member-a", "Member A")
	if err := a.svc.Join(ctx, JoinInput{Token: token, NodeName: "Alice laptop", Owner: "alice", AllowInsecure: true}); err != nil {
		t.Fatal(err)
	}
	membership := a.settings.current()
	if membership.Role != RoleMember || membership.NodeToken == "" || membership.HubNodeID != "hub-node" || membership.ClusterName != "Test cluster" {
		t.Fatalf("membership = %+v", membership)
	}
	entry, ok := hub.hub.node("member-a")
	if !ok || entry.hash != HashSecret(membership.NodeToken) || entry.hash == membership.NodeToken {
		t.Fatal("node token must be stored only as its hash")
	}
	if entry.record.Name != "Alice laptop" || entry.record.Owner != "alice" || entry.record.Status != NodeActive {
		t.Fatalf("node record = %+v", entry.record)
	}
	if a.svc.Identity().Name != "Alice laptop" {
		t.Fatal("joining with a node name renames the local node")
	}
	if !slices.Contains(hub.local.auditActions(), "cluster.join cluster:member-a member-a") {
		t.Fatalf("audits = %v", hub.local.auditActions())
	}

	b := newTestNode(t, "member-b", "Member B")
	if err := b.svc.Join(ctx, JoinInput{Token: token, AllowInsecure: true}); errorCode(err) != CodeInvalidJoinToken {
		t.Fatalf("reusing a single-use token: %v", err)
	}
	if b.svc.Role() != RoleStandalone {
		t.Fatal("failed join changed the role")
	}
	if err := a.svc.Join(ctx, JoinInput{Token: token, AllowInsecure: true}); errorCode(err) != CodeAlreadyConfigured {
		t.Fatalf("joining twice: %v", err)
	}

	expiring, _, err := hub.svc.CreateJoinToken(ctx, "later", time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.svc.Join(ctx, JoinInput{Token: expiring}); errorCode(err) != CodeInsecureHubURL {
		t.Fatalf("plain http hub without allow_insecure: %v", err)
	}
	hub.clock.advance(2 * time.Hour)
	if err := b.svc.Join(ctx, JoinInput{Token: expiring, AllowInsecure: true}); errorCode(err) != CodeJoinTokenExpired {
		t.Fatalf("expired token: %v", err)
	}
	if err := b.svc.Join(ctx, JoinInput{Token: "tpc1_garbage", AllowInsecure: true}); errorCode(err) != CodeInvalidJoinToken {
		t.Fatalf("malformed token: %v", err)
	}
}

func TestJoinWithoutMasterKeyUndoesHubRegistration(t *testing.T) {
	ctx := testContext(t)
	hub := newHub(t)
	token, _, err := hub.svc.CreateJoinToken(ctx, "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	a := newTestNode(t, "member-a", "Member A")
	a.settings.noMasterKey = true
	if err := a.svc.Join(ctx, JoinInput{Token: token, AllowInsecure: true}); errorCode(err) != CodeMasterKeyRequired {
		t.Fatalf("join without master key: %v", err)
	}
	if entry, ok := hub.hub.node("member-a"); !ok || entry.record.Status != NodeLeft {
		t.Fatalf("hub record after failed save = %+v", entry.record)
	}
	if a.svc.Role() != RoleStandalone {
		t.Fatal("node must stay standalone")
	}
}

func TestLinkStreamsAndCancels(t *testing.T) {
	hub := newHub(t)
	a := joinMember(t, hub, "member-a", "Member A")

	release, seen := make(chan struct{}), make(chan relayed, 1)
	a.local.setHandler(sseHandler(release, seen))
	req := relayRequest(t, "/v1/chat/completions?stream=true", `{"stream":true}`)
	req.Header.Set("Authorization", "Bearer client-secret")
	req.Header.Set(HeaderOrigin, "spoofed")
	resp, err := hub.svc.Do(testContext(t), "member-a", req)
	if err != nil {
		t.Fatal(err)
	}
	expectSSE(t, resp, release)
	got := <-seen
	if got.originID != "hub-node" || got.originName != "Hub" || got.uri != "/v1/chat/completions?stream=true" {
		t.Fatalf("member saw %+v", got)
	}
	if got.authorization != "" || len(got.clusterHeaders) != 0 {
		t.Fatalf("credentials or cluster headers leaked: %+v", got)
	}

	started, cancelled := make(chan struct{}), make(chan struct{})
	a.local.setHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err = hub.svc.Do(ctx, "member-a", relayRequest(t, "/v1/messages", ""))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never started")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the caller did not cancel the member's handler")
	}

	eventually(t, "hub can route to the member", func() bool {
		_, _, ok := hub.svc.ClusterRoute("gpt-test")
		return ok
	})
	if mode, pricing, ok := hub.svc.ClusterRoute("gpt-test"); !ok || mode != ModeFallback || pricing == nil || pricing.InputPerMillion != 1 {
		t.Fatalf("hub ClusterRoute = %q, %v, %v", mode, pricing, ok)
	}
	eventually(t, "member can route to the hub", func() bool {
		_, _, ok := a.svc.ClusterRoute("gpt-test")
		return ok
	})
	peers := a.local.lastPeers()
	if len(peers) != 1 || peers[0].ID != "hub-node" || !peers[0].Online {
		t.Fatalf("member peers = %+v", peers)
	}
	if node, _ := nodeView(t, hub.svc, "member-a"); node.RTTMS < 0 || !node.LendingEnabled || len(node.Offers) != 1 || node.Offers[0].EligibleAccounts != 1 {
		t.Fatalf("hub view of member = %+v", node)
	}
}

func TestRelayBetweenMembersStreams(t *testing.T) {
	hub := newHub(t)
	a := joinMember(t, hub, "member-a", "Member A")
	b := joinMember(t, hub, "member-b", "Member B")
	release, seen := make(chan struct{}), make(chan relayed, 1)
	b.local.setHandler(sseHandler(release, seen))
	req := relayRequest(t, "/v1/chat/completions?x=1", `{"model":"gpt-test"}`)
	req.Header.Set("Authorization", "Bearer client-secret")
	req.Header.Set(HeaderOriginName, "spoofed")
	resp, err := a.svc.Do(testContext(t), "member-b", req)
	if err != nil {
		t.Fatal(err)
	}
	expectSSE(t, resp, release)
	got := <-seen
	if got.originID != "member-a" || got.originName != "Member A" || got.uri != "/v1/chat/completions?x=1" {
		t.Fatalf("member B saw %+v", got)
	}
	if got.authorization != "" || len(got.clusterHeaders) != 0 {
		t.Fatalf("credentials or cluster headers leaked: %+v", got)
	}
}

func TestMemberDoToHubServesLocally(t *testing.T) {
	hub := newHub(t)
	a := joinMember(t, hub, "member-a", "Member A")
	seen := make(chan relayed, 1)
	hub.local.setHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, name, ok := OriginFromContext(r.Context())
		body, _ := io.ReadAll(r.Body)
		if !ok || string(body) != `{"model":"gpt-test"}` {
			http.Error(w, "bad relay", http.StatusTeapot)
			return
		}
		seen <- relayed{originID: id, originName: name, uri: r.URL.RequestURI(), authorization: r.Header.Get("Authorization")}
		w.WriteHeader(http.StatusCreated)
	}))
	resp, err := a.svc.Do(testContext(t), "hub-node", relayRequest(t, "/v1/messages?beta=true", `{"model":"gpt-test"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got := <-seen
	if got.originID != "member-a" || got.originName != "Member A" || got.uri != "/v1/messages?beta=true" || got.authorization != "" {
		t.Fatalf("hub saw %+v", got)
	}
}

func TestDoErrors(t *testing.T) {
	ctx := testContext(t)
	hub := newHub(t, func(s *Service) { s.maxBody = func() int64 { return 64 } })
	a := joinMember(t, hub, "member-a", "Member A")
	expectCode := func(resp *http.Response, err error, status int, code string) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != status || resp.Header.Get("X-TProxy-Error-Code") != code {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("got %d %s (%s), want %d %s", resp.StatusCode, resp.Header.Get("X-TProxy-Error-Code"), body, status, code)
		}
	}
	if _, err := a.svc.Do(ctx, "member-a", relayRequest(t, "/v1/models", "")); err == nil {
		t.Fatal("a member relayed to itself")
	}
	if _, err := hub.svc.Do(ctx, "hub-node", relayRequest(t, "/v1/models", "")); err == nil {
		t.Fatal("the hub relayed to itself")
	}
	resp, err := a.svc.Do(ctx, "ghost", relayRequest(t, "/v1/models", ""))
	expectCode(resp, err, http.StatusNotFound, CodeNodeUnknown)
	resp, err = hub.svc.Do(ctx, "ghost", relayRequest(t, "/v1/models", ""))
	expectCode(resp, err, http.StatusServiceUnavailable, CodeNodeOffline)
	resp, err = a.svc.Do(ctx, "hub-node", relayRequest(t, "/v1/models", strings.Repeat("x", 100)))
	expectCode(resp, err, http.StatusRequestEntityTooLarge, CodeRequestTooLarge)
	standalone := newTestNode(t, "lonely", "Lonely")
	resp, err = standalone.svc.Do(ctx, "hub-node", relayRequest(t, "/v1/models", ""))
	expectCode(resp, err, http.StatusServiceUnavailable, CodeNotConfigured)

	hub.local.setHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	if err := hub.svc.SetLending(ctx, LendingPolicy{Enabled: true, Models: []string{"gpt-test"}}); err != nil {
		t.Fatal(err)
	}
	req := relayRequest(t, "/v1/models", "")
	req.Header.Set(HeaderModel, "other-model")
	resp, err = a.svc.Do(ctx, "hub-node", req)
	expectCode(resp, err, http.StatusForbidden, CodeModelNotLent)
	if err := hub.svc.SetLending(ctx, LendingPolicy{}); err != nil {
		t.Fatal(err)
	}
	resp, err = a.svc.Do(ctx, "hub-node", relayRequest(t, "/v1/models", ""))
	expectCode(resp, err, http.StatusForbidden, CodeLendingDisabled)
	eventually(t, "member stops routing to a hub that no longer lends", func() bool {
		_, _, ok := a.svc.ClusterRoute("gpt-test")
		return !ok
	})

	loop, _ := http.NewRequest(http.MethodGet, hub.server.URL+HubPathPrefix+"relay/member-a/v1/models", nil)
	loop.Header.Set("Authorization", "Bearer "+a.settings.current().NodeToken)
	loopResp, err := http.DefaultClient.Do(loop)
	expectCode(loopResp, err, http.StatusBadRequest, CodeRelayLoop)
	anonymous, _ := http.NewRequest(http.MethodGet, hub.server.URL+HubPathPrefix+"state", nil)
	anonResp, err := http.DefaultClient.Do(anonymous)
	expectCode(anonResp, err, http.StatusUnauthorized, CodeUnauthorized)
}

func TestStateETagAndCatalogRefresh(t *testing.T) {
	ctx := testContext(t)
	hub := newHub(t)
	a := joinMember(t, hub, "member-a", "Member A")
	eventually(t, "catalog applied", func() bool { return a.local.appliedCount() == 1 })
	// The state settles once the hub has the member's offers for that catalog.
	eventually(t, "hub has the member's offers", func() bool {
		node, _ := nodeView(t, hub.svc, "member-a")
		return len(node.Offers) == 1
	})

	getState := func(etag string) *http.Response {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, hub.server.URL+HubPathPrefix+"state", nil)
		req.Header.Set("Authorization", "Bearer "+a.settings.current().NodeToken)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	first := getState("")
	var state State
	if err := json.NewDecoder(first.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	first.Body.Close()
	etag := first.Header.Get("ETag")
	if first.StatusCode != http.StatusOK || state.Version == "" || etag != quoteETag(state.Version) || state.CatalogVersion == "" {
		t.Fatalf("state %d etag %q: %+v", first.StatusCode, etag, state)
	}
	second := getState(etag)
	second.Body.Close()
	if second.StatusCode != http.StatusNotModified {
		t.Fatalf("matching If-None-Match answered %d", second.StatusCode)
	}
	eventually(t, "member polls answered with 304", func() bool {
		return hub.requests.count(HubPathPrefix+"state 304") > 2
	})

	catalogGets := hub.requests.count(HubPathPrefix + "catalog")
	time.Sleep(300 * time.Millisecond)
	if got := hub.requests.count(HubPathPrefix + "catalog"); got != catalogGets || a.local.appliedCount() != 1 {
		t.Fatalf("catalog fetched %d->%d times, applied %d, without a version change", catalogGets, got, a.local.appliedCount())
	}

	hub.local.setModels(
		CatalogModel{ID: "gpt-test", DisplayName: "GPT Test", Enabled: true, Pricing: &config.PricingConfig{InputPerMillion: 1}},
		CatalogModel{ID: "new-model", DisplayName: "New", Enabled: true},
	)
	if err := hub.svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "changed catalog applied", func() bool { return a.local.appliedCount() == 2 })
	time.Sleep(200 * time.Millisecond)
	if got := hub.requests.count(HubPathPrefix + "catalog"); got != catalogGets+1 {
		t.Fatalf("catalog fetched %d times after one change, want %d", got, catalogGets+1)
	}
	status, err := a.svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Catalog == nil || len(status.Catalog.Models) != 2 || status.Catalog.Version == "" {
		t.Fatalf("member catalog = %+v", status.Catalog)
	}
}

func TestRevokedMemberStopsWithoutRedial(t *testing.T) {
	ctx := testContext(t)
	hub := newHub(t)
	// A long backoff makes the state poll, not a redial, find the revocation.
	a := joinMember(t, hub, "member-a", "Member A", func(s *Service) { s.minBackoff, s.maxBackoff = 10*time.Second, 10*time.Second })
	if err := hub.svc.RevokeNode(ctx, "member-a"); err != nil {
		t.Fatal(err)
	}
	if hub.svc.links.get("member-a") != nil {
		t.Fatal("revoked node still linked")
	}
	if node, _ := nodeView(t, hub.svc, "member-a"); node.Online || node.Status != NodeRevoked {
		t.Fatalf("hub view after revoke = %+v", node)
	}
	eventually(t, "member handles the revocation", func() bool {
		return a.settings.current().Revoked && a.local.clearedCount() == 1
	})
	if a.settings.current().NodeToken != "" {
		t.Fatal("a revoked membership keeps no node token")
	}
	eventually(t, "the hub answered a member poll with 401", func() bool {
		return hub.requests.count(HubPathPrefix+"state 401")+hub.requests.count(HubPathPrefix+"catalog 401") > 0
	})
	status, err := a.svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Role != RoleMember || status.Link == nil || status.Link.State != LinkRevoked {
		t.Fatalf("member status = %+v", status)
	}
	if _, _, ok := a.svc.ClusterRoute("gpt-test"); ok {
		t.Fatal("revoked member still routes to the cluster")
	}
	eventually(t, "revocation audited", func() bool {
		return slices.ContainsFunc(a.local.auditActions(), func(s string) bool { return strings.HasPrefix(s, "cluster.revoked") })
	})
	time.Sleep(100 * time.Millisecond) // let requests already in flight land
	links, polls := hub.requests.count(HubPathPrefix+"link"), hub.requests.count(HubPathPrefix+"state")
	time.Sleep(300 * time.Millisecond)
	if hub.requests.count(HubPathPrefix+"link") != links || hub.requests.count(HubPathPrefix+"state") != polls {
		t.Fatal("revoked member kept dialing or polling")
	}
	if err := a.svc.Leave(ctx); err != nil {
		t.Fatal(err)
	}
	if a.svc.Role() != RoleStandalone || a.settings.current().Role != "" {
		t.Fatal("leaving a revoked membership returns to standalone")
	}
}

func TestMemberReconnectsAfterHubDropsLink(t *testing.T) {
	hub := newHub(t)
	a := joinMember(t, hub, "member-a", "Member A")
	first := hub.svc.links.get("member-a")
	first.close()
	eventually(t, "member relinked", func() bool {
		current := hub.svc.links.get("member-a")
		return current != nil && current != first && nodeOnline(t, hub.svc, "member-a")
	})
	eventually(t, "member reports a connected link", func() bool {
		status, err := a.svc.Status(context.Background())
		return err == nil && status.Link != nil && status.Link.State == LinkConnected
	})
	a.local.setHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	resp, err := hub.svc.Do(testContext(t), "member-a", relayRequest(t, "/v1/models", ""))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status over the new link = %d", resp.StatusCode)
	}
}

func TestMemberMarksPeersOfflineWhenHubIsUnreachable(t *testing.T) {
	hub := newHub(t)
	a := joinMember(t, hub, "member-a", "Member A")
	eventually(t, "member routes to the hub", func() bool {
		_, _, ok := a.svc.ClusterRoute("gpt-test")
		return ok
	})
	hub.svc.Stop()
	hub.server.Close()
	eventually(t, "hub_unreachable warning", func() bool {
		status, err := a.svc.Status(context.Background())
		return err == nil && slices.Contains(status.Warnings, WarningHubUnreachable)
	})
	if _, _, ok := a.svc.ClusterRoute("gpt-test"); ok {
		t.Fatal("member still routes to an unreachable cluster")
	}
	peers := a.local.lastPeers()
	if len(peers) == 0 || slices.ContainsFunc(peers, func(n NodeState) bool { return n.Online }) {
		t.Fatalf("peers after hub loss = %+v", peers)
	}
	status, _ := a.svc.Status(context.Background())
	if node := status.Nodes[slices.IndexFunc(status.Nodes, func(n StatusNodeView) bool { return n.ID == "hub-node" })]; node.Online {
		t.Fatal("status still shows the hub online")
	}
}

func TestForeignForbiddenIsNotRevocation(t *testing.T) {
	hub := newHub(t)
	a := joinMember(t, hub, "member-a", "Member A")
	hub.requests.waf.Store(true)
	hub.svc.links.get("member-a").close()
	eventually(t, "hub_unreachable warning", func() bool {
		status, err := a.svc.Status(context.Background())
		return err == nil && slices.Contains(status.Warnings, WarningHubUnreachable)
	})
	if a.settings.current().Revoked || a.local.clearedCount() != 0 {
		t.Fatal("a 403 without the hub's error code revoked the member")
	}
	hub.requests.waf.Store(false)
	eventually(t, "member back online", func() bool {
		status, err := a.svc.Status(context.Background())
		return err == nil && !slices.Contains(status.Warnings, WarningHubUnreachable) && nodeOnline(t, hub.svc, "member-a")
	})
}

func TestLeaveAndDissolve(t *testing.T) {
	ctx := testContext(t)
	hub := newHub(t)
	a := joinMember(t, hub, "member-a", "Member A")
	b := joinMember(t, hub, "member-b", "Member B")

	if err := a.svc.Leave(ctx); err != nil {
		t.Fatal(err)
	}
	if a.svc.Role() != RoleStandalone || a.local.clearedCount() != 1 || a.settings.current().Role != "" {
		t.Fatal("leave must clear local cluster state")
	}
	if entry, _ := hub.hub.node("member-a"); entry.record.Status != NodeLeft {
		t.Fatalf("hub record after leave = %+v", entry.record)
	}
	eventually(t, "hub sees member A gone", func() bool { return !nodeOnline(t, hub.svc, "member-a") })
	if err := hub.svc.Leave(ctx); errorCode(err) != CodeInvalidRequest {
		t.Fatalf("hub leave: %v", err)
	}

	if err := hub.svc.Dissolve(ctx); err != nil {
		t.Fatal(err)
	}
	if hub.svc.Role() != RoleStandalone || hub.settings.current().Role != "" || hub.local.clearedCount() != 1 {
		t.Fatal("dissolve must return the hub to standalone")
	}
	if nodes, _ := hub.hub.Nodes(ctx); len(nodes) != 0 {
		t.Fatalf("hub store not reset: %+v", nodes)
	}
	eventually(t, "member B told it was revoked", func() bool {
		return b.settings.current().Revoked && b.local.clearedCount() == 1
	})
	resp, err := http.Get(hub.server.URL + HubPathPrefix + "state")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("dissolved hub API answered %d", resp.StatusCode)
	}
}

func TestLedgerNames(t *testing.T) {
	ctx := testContext(t)
	hub := newHub(t)
	day := time.Now().UTC().Format(dayLayout)
	hub.local.mu.Lock()
	hub.local.ledger = []LedgerRow{{Day: day, Borrower: "node:member-a", Requests: 3}}
	hub.local.mu.Unlock()
	a := joinMember(t, hub, "member-a", "Member A")
	a.local.mu.Lock()
	a.local.ledger = []LedgerRow{{Day: day, Lender: "ignored", Borrower: "key:k1", Requests: 2}}
	a.local.mu.Unlock()
	if err := hub.svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	for _, svc := range []*Service{hub.svc, a.svc} {
		rows, err := svc.Ledger(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 {
			t.Fatalf("ledger = %+v", rows)
		}
		for _, row := range rows {
			switch row.Lender {
			case "hub-node":
				if row.LenderName != "Hub" || row.BorrowerName != "Member A" {
					t.Fatalf("hub row = %+v", row)
				}
			case "member-a":
				if row.LenderName != "Member A" || row.Borrower != "key:k1" {
					t.Fatalf("member row = %+v", row)
				}
			default:
				t.Fatalf("unexpected lender in %+v", row)
			}
		}
	}
}

func TestStatusJSONShape(t *testing.T) {
	ctx := testContext(t)
	decode := func(svc *Service) map[string]any {
		t.Helper()
		view, err := svc.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(view)
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"nodes", "warnings", "offers", "models", "combos", "api_keys", "conflicts"} {
			if strings.Contains(string(data), `"`+field+`":null`) {
				t.Fatalf("%s is null in %s", field, data)
			}
		}
		var out map[string]any
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	lonely := newTestNode(t, "lonely", "Lonely")
	standalone := decode(lonely.svc)
	if standalone["role"] != "standalone" || standalone["cluster"] != nil || standalone["catalog"] != nil || standalone["link"] != nil {
		t.Fatalf("standalone status = %v", standalone)
	}
	if _, ok := standalone["shared"]; ok {
		t.Fatal("shared only appears on the hub")
	}
	if _, ok := standalone["suggested_hub_urls"]; ok {
		t.Fatal("suggested_hub_urls is added by the API layer")
	}
	if nodes, ok := standalone["nodes"].([]any); !ok || len(nodes) != 0 {
		t.Fatalf("nodes = %v", standalone["nodes"])
	}
	if lending := standalone["lending"].(map[string]any); lending["models"] == nil {
		t.Fatal("lending.models must be an array")
	}

	hub := newHub(t)
	joinMember(t, hub, "member-a", "Member A")
	hubView := decode(hub.svc)
	if hubView["link"] != nil || hubView["shared"] == nil || hubView["cluster"] == nil || hubView["catalog"] == nil {
		t.Fatalf("hub status = %v", hubView)
	}
	if warnings := hubView["warnings"].([]any); !slices.Contains(warnings, any(WarningInsecureHubURL)) {
		t.Fatalf("warnings = %v", warnings)
	}
	cluster := hubView["cluster"].(map[string]any)
	if cluster["joined_at"] == "" || cluster["hub_url"] != hub.server.URL || cluster["allow_insecure"] != true {
		t.Fatalf("cluster = %v", cluster)
	}
	catalog := hubView["catalog"].(map[string]any)
	if catalog["conflicts"] == nil || len(catalog["models"].([]any)) != 1 {
		t.Fatalf("catalog = %v", catalog)
	}
	nodes := hubView["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("hub nodes = %v", nodes)
	}
	self := nodes[0].(map[string]any)
	if self["id"] != "hub-node" || self["self"] != true || self["role"] != "hub" || self["offers"] == nil {
		t.Fatalf("hub self entry = %v", self)
	}
}
