package cluster

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestJoinRejectsExistingIdentityAndAllowsRevoked(t *testing.T) {
	ctx := testContext(t)
	hub := newHub(t)
	token, _, err := hub.svc.CreateJoinToken(ctx, "", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	a := newTestNode(t, "same-id", "A")
	b := newTestNode(t, "same-id", "B")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, n := range []*testNode{a, b} {
		wg.Add(1)
		go func(n *testNode) {
			defer wg.Done()
			errs <- n.svc.Join(ctx, JoinInput{Token: token, AllowInsecure: true})
		}(n)
	}
	wg.Wait()
	close(errs)
	successes, exists := 0, 0
	for err := range errs {
		if err == nil {
			successes++
		} else if errorCode(err) == CodeNodeExists {
			exists++
		} else {
			t.Fatalf("join: %v", err)
		}
	}
	if successes != 1 || exists != 1 {
		t.Fatalf("successes %d, conflicts %d", successes, exists)
	}
	if err := hub.svc.RevokeNode(ctx, "same-id"); err != nil {
		t.Fatal(err)
	}
	if a.svc.Role() == RoleStandalone {
		if err := a.svc.Join(ctx, JoinInput{Token: token, AllowInsecure: true}); err != nil {
			t.Fatal(err)
		}
	} else if err := b.svc.Join(ctx, JoinInput{Token: token, AllowInsecure: true}); err != nil {
		t.Fatal(err)
	}
}

func TestLateLinkRejectedAfterRevokeAndRejoin(t *testing.T) {
	ctx := testContext(t)
	hub := newHub(t)
	member := joinMember(t, hub, "member-a", "A")
	token := member.settings.current().NodeToken
	if err := hub.svc.RevokeNode(ctx, "member-a"); err != nil {
		t.Fatal(err)
	}
	if hub.svc.addLink(&hubLink{nodeID: "member-a"}, HashSecret(token)) {
		t.Fatal("registered revoked link")
	}
	resp, err := hub.svc.Do(ctx, "member-a", relayRequest(t, "/v1/chat", ""))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("X-TProxy-Error-Code") != CodeNodeOffline {
		t.Fatalf("got %s", resp.Status)
	}
	newToken, _, err := hub.svc.CreateJoinToken(ctx, "", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	other := newTestNode(t, "member-a", "replacement")
	if err := other.svc.Join(ctx, JoinInput{Token: newToken, AllowInsecure: true}); err != nil {
		t.Fatal(err)
	}
	if hub.svc.addLink(&hubLink{nodeID: "member-a"}, HashSecret(token)) {
		t.Fatal("old token registered after rejoin")
	}
}

func TestLeaveCleanupRetriesAndRetainsMembership(t *testing.T) {
	ctx := testContext(t)
	hub := newHub(t)
	member := joinMember(t, hub, "member-a", "A")
	member.local.mu.Lock()
	member.local.clearFailures = 100
	member.local.mu.Unlock()
	if err := member.svc.Leave(ctx); err == nil {
		t.Fatal("cleanup succeeded unexpectedly")
	}
	if m := member.settings.current(); m.Role != RoleMember || !m.Revoked {
		t.Fatalf("membership = %+v", m)
	}
	if len(member.svc.CatalogIDs()) != 0 || member.svc.ActiveMember() {
		t.Fatal("revoked member exposed catalog")
	}
	member.local.mu.Lock()
	member.local.clearFailures = 0
	member.local.mu.Unlock()
	eventually(t, "cleanup retry", func() bool {
		member.local.mu.Lock()
		defer member.local.mu.Unlock()
		return member.local.clearSuccesses > 0
	})
	if err := member.svc.Leave(ctx); err != nil {
		t.Fatal(err)
	}
	if member.settings.current().Role != "" {
		t.Fatal("membership retained after successful leave")
	}
}

func TestStartupRetriesRevokedCleanup(t *testing.T) {
	settings := &memSettings{identity: NodeIdentity{ID: "member-a"}, membership: Membership{Role: RoleMember, Revoked: true}}
	local := newFakeLocal()
	local.clearFailures = 2
	svc := New(Options{Settings: settings, Hub: newMemHub(), Local: local, Logf: testLogf(t)})
	svc.minBackoff = time.Millisecond
	svc.maxBackoff = 5 * time.Millisecond
	svc.Start(context.Background())
	defer svc.Stop()
	eventually(t, "startup cleanup", func() bool { return local.clearedCount() >= 3 })
}

func TestDissolveRetriesAfterMembershipFailure(t *testing.T) {
	ctx := testContext(t)
	hub := newHub(t)
	hub.settings.mu.Lock()
	hub.settings.clearMembershipFails = true
	hub.settings.mu.Unlock()
	if err := hub.svc.Dissolve(ctx); err == nil {
		t.Fatal("dissolve succeeded unexpectedly")
	}
	if hub.svc.Role() != RoleHub || hub.settings.current().Role != RoleHub {
		t.Fatal("hub membership lost after failure")
	}
	hub.settings.mu.Lock()
	hub.settings.clearMembershipFails = false
	hub.settings.mu.Unlock()
	if err := hub.svc.Dissolve(ctx); err != nil {
		t.Fatal(err)
	}
	if hub.svc.Role() != RoleStandalone || hub.settings.current().Role != "" {
		t.Fatal("retry did not dissolve")
	}
}

type checkingSettings struct{ *memSettings }

func (s checkingSettings) EncryptionAvailable() bool { return false }
func TestJoinChecksEncryptionBeforeHubRequest(t *testing.T) {
	ctx := testContext(t)
	hub := newHub(t)
	token, _, err := hub.svc.CreateJoinToken(ctx, "", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	settings := checkingSettings{&memSettings{identity: NodeIdentity{ID: "member-a"}}}
	svc := New(Options{Settings: settings, Hub: newMemHub(), Local: newFakeLocal(), Logf: testLogf(t)})
	defer svc.Stop()
	if err := svc.Join(ctx, JoinInput{Token: token, AllowInsecure: true}); errorCode(err) != CodeMasterKeyRequired {
		t.Fatalf("got %v", err)
	}
	if hub.requests.count(HubPathPrefix+"join") != 0 {
		t.Fatal("join request sent")
	}
	recs, err := hub.hub.JoinTokens(ctx)
	if err != nil || recs[0].Uses != 0 {
		t.Fatalf("token consumed: %+v, %v", recs, err)
	}
}

func TestAdmissionHonorsCancellation(t *testing.T) {
	svc := New(Options{})
	svc.opMu.Lock()
	defer svc.opMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := svc.Sync(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("sync: %v", err)
	}
	if err := svc.Leave(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("leave: %v", err)
	}
	svc.pollMu.Lock()
	defer svc.pollMu.Unlock()
	if err := svc.pollHub(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("poll hub: %v", err)
	}
	if err := svc.pollMember(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("poll member: %v", err)
	}
}

func TestSaturatedLinkReturnsBusy(t *testing.T) {
	l := &hubLink{streams: make(chan struct{}, maxLinkStreams), waitTimeout: 10 * time.Millisecond}
	for range maxLinkStreams {
		l.streams <- struct{}{}
	}
	req := httptest.NewRequest(http.MethodGet, "http://member/test", nil)
	resp, err := l.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 503 || resp.Header.Get("X-TProxy-Error-Code") != CodeNodeBusy || !strings.Contains(string(body), CodeNodeBusy) {
		t.Fatalf("got %d %s", resp.StatusCode, body)
	}
}

func TestWSConnDeadlineInterruptsBlockedWrite(t *testing.T) {
	for name, setDeadline := range map[string]func(*wsConn, time.Time) error{
		"write": (*wsConn).SetWriteDeadline,
		"both":  (*wsConn).SetDeadline,
	} {
		t.Run(name, func(t *testing.T) {
			accepted := make(chan *wsConn, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ws, err := linkUpgrader.Upgrade(w, r, nil)
				if err == nil {
					accepted <- newWSConn(ws)
				}
			}))
			defer srv.Close()
			ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			conn := newWSConn(ws)
			defer conn.Close()
			peer := <-accepted
			defer peer.Close()
			done := make(chan error, 1)
			go func() { _, err := conn.Write(make([]byte, 32<<20)); done <- err }()
			select {
			case err := <-done:
				t.Fatalf("write did not block: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			if err := setDeadline(conn, time.Now().Add(20*time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("write succeeded")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("blocked write ignored deadline")
			}
		})
	}
}

func TestRevocationCleanupRetries(t *testing.T) {
	ctx := testContext(t)
	hub := newHub(t)
	member := joinMember(t, hub, "member-a", "A")
	member.local.mu.Lock()
	member.local.clearFailures = 2
	member.local.mu.Unlock()
	if err := hub.svc.RevokeNode(ctx, "member-a"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "revoked cleanup retry", func() bool {
		member.local.mu.Lock()
		defer member.local.mu.Unlock()
		return member.local.cleared >= 3 && member.local.clearSuccesses > 0
	})
	if member.svc.ActiveMember() || len(member.svc.CatalogIDs()) != 0 {
		t.Fatal("revoked member exposed catalog")
	}
}

func TestStandaloneCatalogIDsAreEmpty(t *testing.T) {
	svc := New(Options{})
	svc.catalog.Models = []CatalogModel{{ID: "stale", Enabled: true}}
	if ids := svc.CatalogIDs(); len(ids) != 0 {
		t.Fatalf("standalone catalog: %v", ids)
	}
}

func TestSaturatedLinkDoAndRelayReturnBusy(t *testing.T) {
	ctx := testContext(t)
	hub := newHub(t)
	_ = joinMember(t, hub, "member-a", "A")
	origin := joinMember(t, hub, "member-b", "B")
	link := &hubLink{nodeID: "member-a", streams: make(chan struct{}, maxLinkStreams), waitTimeout: 20 * time.Millisecond}
	for range maxLinkStreams {
		link.streams <- struct{}{}
	}
	hub.svc.links.mu.Lock()
	old := hub.svc.links.byNode["member-a"]
	hub.svc.links.byNode["member-a"] = link
	hub.svc.links.mu.Unlock()
	defer func() {
		hub.svc.links.mu.Lock()
		hub.svc.links.byNode["member-a"] = old
		hub.svc.links.mu.Unlock()
	}()
	for name, svc := range map[string]*Service{"direct": hub.svc, "relay": origin.svc} {
		resp, err := svc.Do(ctx, "member-a", relayRequest(t, "/v1/chat", ""))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("X-TProxy-Error-Code") != CodeNodeBusy || !strings.Contains(string(body), CodeNodeBusy) {
			t.Fatalf("%s: status %d body %s", name, resp.StatusCode, body)
		}
	}
}

func TestDissolveRetriesAfterCleanupFailure(t *testing.T) {
	ctx := testContext(t)
	hub := newHub(t)
	hub.local.mu.Lock()
	hub.local.clearFailures = 1
	hub.local.mu.Unlock()
	if err := hub.svc.Dissolve(ctx); err == nil {
		t.Fatal("dissolve succeeded despite cleanup failure")
	}
	if hub.svc.Role() != RoleHub || hub.settings.current().Role != RoleHub {
		t.Fatal("hub membership lost after cleanup failure")
	}
	if err := hub.svc.Dissolve(ctx); err != nil {
		t.Fatal(err)
	}
	if hub.svc.Role() != RoleStandalone {
		t.Fatal("retry did not dissolve")
	}
}
