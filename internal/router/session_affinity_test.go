package router

import (
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/config"
)

func newSessionRouter(t *testing.T, routing config.RoutingConfig) *Router {
	t.Helper()
	r := New(nil, nil)
	if routing.SessionAffinityTTL == "" {
		routing.SessionAffinityTTL = "1h"
	}
	routing.SessionAffinity = true
	r.ConfigureRouting(routing)
	return r
}

func TestSessionStartCredentialPrefersOwnBinding(t *testing.T) {
	r := newSessionRouter(t, config.RoutingConfig{})
	r.sessions["td-model:child"] = sessionBinding{CredentialID: "own", ExpiresAt: time.Now().Add(time.Hour)}
	r.sessions["td-model:parent"] = sessionBinding{CredentialID: "inherited", ExpiresAt: time.Now().Add(time.Hour)}

	credential, reason := r.sessionStartCredential("td-model", "child", "parent")
	if credential != "own" || reason != "session-affinity" {
		t.Fatalf("credential=%q reason=%q, want the child's own binding", credential, reason)
	}
}

func TestSessionStartCredentialInheritsParent(t *testing.T) {
	r := newSessionRouter(t, config.RoutingConfig{})
	r.sessions["td-model:parent"] = sessionBinding{CredentialID: "inherited", ExpiresAt: time.Now().Add(time.Hour)}

	credential, reason := r.sessionStartCredential("td-model", "child", "parent")
	if credential != "inherited" || reason != "session-affinity-parent" {
		t.Fatalf("credential=%q reason=%q", credential, reason)
	}
}

func TestSessionStartCredentialDoesNotTouchParentTTL(t *testing.T) {
	r := newSessionRouter(t, config.RoutingConfig{SessionAffinityTTL: "1h"})
	expires := time.Now().Add(time.Hour).Truncate(time.Second)
	r.sessions["td-model:parent"] = sessionBinding{CredentialID: "inherited", ExpiresAt: expires}

	r.sessionStartCredential("td-model", "child", "parent")

	if got := r.sessions["td-model:parent"].ExpiresAt; !got.Equal(expires) {
		t.Fatalf("parent TTL changed: got %v want %v", got, expires)
	}
	if _, exists := r.sessions["td-model:child"]; exists {
		t.Fatal("looking up the parent must not create a child binding")
	}
}

func TestSessionStartCredentialIgnoresExpiredBindings(t *testing.T) {
	r := newSessionRouter(t, config.RoutingConfig{})
	r.sessions["td-model:parent"] = sessionBinding{CredentialID: "inherited", ExpiresAt: time.Now().Add(-time.Minute)}

	if credential, _ := r.sessionStartCredential("td-model", "child", "parent"); credential != "" {
		t.Fatalf("expired parent binding was used: %q", credential)
	}
	if _, exists := r.sessions["td-model:parent"]; exists {
		t.Fatal("expired binding should be evicted on lookup")
	}
}

func TestSessionStartCredentialRespectsSubagentSwitch(t *testing.T) {
	off := false
	r := newSessionRouter(t, config.RoutingConfig{SessionAffinitySubagents: &off})
	r.sessions["td-model:parent"] = sessionBinding{CredentialID: "inherited", ExpiresAt: time.Now().Add(time.Hour)}

	if credential, _ := r.sessionStartCredential("td-model", "child", "parent"); credential != "" {
		t.Fatalf("subagent inheritance should be off, got %q", credential)
	}
}

func TestSessionStartCredentialNeedsAffinity(t *testing.T) {
	r := newSessionRouter(t, config.RoutingConfig{})
	r.sessionAffinity = false
	r.sessions["td-model:parent"] = sessionBinding{CredentialID: "inherited", ExpiresAt: time.Now().Add(time.Hour)}

	if credential, _ := r.sessionStartCredential("td-model", "parent", ""); credential != "" {
		t.Fatalf("affinity off must not prefer anything, got %q", credential)
	}
}

func TestBindSessionRebindModes(t *testing.T) {
	cases := []struct {
		mode     string
		previous FailureReason
		want     string
	}{
		{config.SessionRebindAny, FailureTransient, "fresh"},
		{config.SessionRebindAny, FailureQuota, "fresh"},
		{config.SessionRebindQuota, FailureTransient, "sticky"},
		{config.SessionRebindQuota, FailureQuota, "fresh"},
		{config.SessionRebindQuota, FailureAuth, "fresh"},
		// No quota/auth failure means quota mode keeps the existing binding,
		// including when cooldown filtering removed it from this request's list.
		{config.SessionRebindQuota, FailureNone, "sticky"},
	}
	for _, tc := range cases {
		r := newSessionRouter(t, config.RoutingConfig{SessionAffinityRebindOn: tc.mode})
		r.bindSession("td-model", "session", "sticky", FailureNone)
		r.bindSession("td-model", "session", "fresh", tc.previous)
		if got := r.sessions["td-model:session"].CredentialID; got != tc.want {
			t.Fatalf("mode=%s previous=%v -> %q, want %q", tc.mode, tc.previous, got, tc.want)
		}
	}
}

func TestBindSessionFirstBindingAlwaysSticks(t *testing.T) {
	// With no existing binding there is no move to second-guess: quota mode must
	// still record the first successful credential.
	r := newSessionRouter(t, config.RoutingConfig{SessionAffinityRebindOn: config.SessionRebindQuota})
	r.bindSession("td-model", "session", "first", FailureTransient)
	if got := r.sessions["td-model:session"].CredentialID; got != "first" {
		t.Fatalf("first binding = %q", got)
	}
}

func TestFailureReasonClassification(t *testing.T) {
	if !failureReasonForStatus(429).IsQuota() || !failureReasonForStatus(401).IsQuota() || !failureReasonForStatus(403).IsQuota() {
		t.Fatal("429/401/403 must count as quota")
	}
	for _, status := range []int{408, 500, 502, 503, 504} {
		if failureReasonForStatus(status).IsQuota() {
			t.Fatalf("status %d must be transient", status)
		}
	}
	if failureReasonForStatus(0) != FailureNone {
		t.Fatal("status 0 is not a failure")
	}
}

func TestRoutingParentSessionIDScopesByClientKey(t *testing.T) {
	request := canonical.Request{
		SessionID: "child",
		Metadata:  map[string]any{config.ParentSessionMetadataKey: "parent", "client_api_key_id": "key-a"},
	}
	other := canonical.Request{
		SessionID: "child",
		Metadata:  map[string]any{config.ParentSessionMetadataKey: "parent", "client_api_key_id": "key-b"},
	}
	if routingParentSessionID(request) == routingParentSessionID(other) {
		t.Fatal("parent key must include the client key")
	}
	if routingParentSessionID(request) == "" {
		t.Fatal("expected a parent key")
	}
}

func TestRoutingParentSessionIDEmptyWithoutParent(t *testing.T) {
	if got := routingParentSessionID(canonical.Request{SessionID: "child"}); got != "" {
		t.Fatalf("parent key = %q, want empty", got)
	}
}
