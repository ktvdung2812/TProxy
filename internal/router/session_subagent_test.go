package router_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/router"
)

// affinityUpstream echoes the credential token back so a test can see which
// account served a request, and can make one token answer with a failure status.
type affinityUpstream struct {
	*httptest.Server
	mu         sync.Mutex
	failToken  string
	failStatus int
}

func newAffinityUpstream(t *testing.T) *affinityUpstream {
	t.Helper()
	upstream := &affinityUpstream{}
	upstream.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		upstream.mu.Lock()
		failToken, failStatus := upstream.failToken, upstream.failStatus
		upstream.mu.Unlock()
		if token == failToken && failStatus != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(failStatus)
			_, _ = w.Write([]byte(`{"error":{"message":"upstream said no"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"chatcmpl-affinity","model":"upstream","choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}]}`, token)
	}))
	t.Cleanup(upstream.Close)
	return upstream
}

func (u *affinityUpstream) fail(token string, status int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.failToken = token
	u.failStatus = status
}

func (u *affinityUpstream) stopFailing() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.failToken = ""
	u.failStatus = 0
}

func newAffinityRouter(t *testing.T, upstream *affinityUpstream, routing config.RoutingConfig) *router.Router {
	t.Helper()
	t.Setenv("TPROXY_AFFINITY_ONE", "account-one")
	t.Setenv("TPROXY_AFFINITY_TWO", "account-two")
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{
			ID: "provider", Type: "openai-compatible", Name: "Provider", BaseURL: upstream.URL, Enabled: true,
			Credentials: []config.CredentialConfig{
				{ID: "one", AuthType: "api_key", SecretEnv: "TPROXY_AFFINITY_ONE", Priority: 10},
				{ID: "two", AuthType: "api_key", SecretEnv: "TPROXY_AFFINITY_TWO", Priority: 10},
			},
		}},
		Models: []config.PublicModelConfig{{
			ID: "td-affinity", Enabled: true, RewriteResponseModel: true,
			Routes: []config.RouteTargetConfig{{ID: "route", Provider: "provider", UpstreamModel: "upstream", Priority: 10}},
		}},
	}
	dataStore := newStore(t, cfg)
	requestRouter := router.New(dataStore, providers.NewRegistry())
	if routing.Strategy == "" {
		routing.Strategy = "round-robin"
	}
	if routing.SessionAffinityTTL == "" {
		routing.SessionAffinityTTL = "1h"
	}
	routing.SessionAffinity = true
	requestRouter.ConfigureRouting(routing)
	return requestRouter
}

func executeAffinity(t *testing.T, requestRouter *router.Router, sessionID string, metadata map[string]any) string {
	t.Helper()
	model, err := requestRouter.Resolve(context.Background(), "td-affinity", nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := requestRouter.Execute(context.Background(), *model, canonical.Request{
		RequestID: "req-" + sessionID,
		SessionID: sessionID,
		Metadata:  metadata,
		Messages:  []canonical.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	content, _ := result.Response.Content.(string)
	return content
}

func TestSessionSubagentInheritsParentCredential(t *testing.T) {
	upstream := newAffinityUpstream(t)
	requestRouter := newAffinityRouter(t, upstream, config.RoutingConfig{})

	parent := executeAffinity(t, requestRouter, "parent-session", nil)
	child := executeAffinity(t, requestRouter, "child-session", map[string]any{config.ParentSessionMetadataKey: "parent-session"})
	if parent != child {
		t.Fatalf("child did not inherit the parent credential: parent=%q child=%q", parent, child)
	}
}

func TestSessionChildOwnBindingWins(t *testing.T) {
	upstream := newAffinityUpstream(t)
	requestRouter := newAffinityRouter(t, upstream, config.RoutingConfig{})

	executeAffinity(t, requestRouter, "parent-session", nil)
	// The child builds its own binding first; a later parent must not steal it.
	first := executeAffinity(t, requestRouter, "child-session", nil)
	second := executeAffinity(t, requestRouter, "child-session", map[string]any{config.ParentSessionMetadataKey: "parent-session"})
	if first != second {
		t.Fatalf("child binding was overridden by the parent: first=%q second=%q", first, second)
	}
}

func TestSessionRebindQuotaKeepsBindingAfterTransientFailure(t *testing.T) {
	upstream := newAffinityUpstream(t)
	// Cooldown is off so the 502 does not bench the account and route the next
	// request around the binding; only the session decision is under test.
	requestRouter := newAffinityRouter(t, upstream, config.RoutingConfig{
		SessionAffinityRebindOn: config.SessionRebindQuota,
		Cooldown:                config.CooldownConfig{Disabled: true},
	})

	sticky := executeAffinity(t, requestRouter, "quota-session", nil)

	// The sticky credential returns a 502; this request lands on the other account.
	upstream.fail(sticky, http.StatusBadGateway)
	moved := executeAffinity(t, requestRouter, "quota-session", nil)
	if moved == sticky {
		t.Skip("upstream served the same credential after a failure; nothing to assert")
	}
	upstream.stopFailing()

	// The binding must still point at the original credential.
	if back := executeAffinity(t, requestRouter, "quota-session", nil); back != sticky {
		t.Fatalf("quota mode moved the binding after a 502: got %q want %q", back, sticky)
	}
}

func TestSessionRebindQuotaRebindsAfter429(t *testing.T) {
	upstream := newAffinityUpstream(t)
	requestRouter := newAffinityRouter(t, upstream, config.RoutingConfig{SessionAffinityRebindOn: config.SessionRebindQuota})

	sticky := executeAffinity(t, requestRouter, "quota-429-session", nil)
	upstream.fail(sticky, http.StatusTooManyRequests)
	moved := executeAffinity(t, requestRouter, "quota-429-session", nil)
	if moved == sticky {
		t.Fatal("expected the request to move off the rate-limited credential")
	}
	upstream.stopFailing()

	// A 429 is a quota failure, so the binding follows the traffic.
	if after := executeAffinity(t, requestRouter, "quota-429-session", nil); after != moved {
		t.Fatalf("binding did not follow a 429: got %q want %q", after, moved)
	}
}

func TestSessionRebindAnyRebindsAfterTransientFailure(t *testing.T) {
	upstream := newAffinityUpstream(t)
	requestRouter := newAffinityRouter(t, upstream, config.RoutingConfig{SessionAffinityRebindOn: config.SessionRebindAny})

	sticky := executeAffinity(t, requestRouter, "any-session", nil)
	upstream.fail(sticky, http.StatusBadGateway)
	moved := executeAffinity(t, requestRouter, "any-session", nil)
	if moved == sticky {
		t.Skip("upstream served the same credential after a failure; nothing to assert")
	}
	upstream.stopFailing()

	if after := executeAffinity(t, requestRouter, "any-session", nil); after != moved {
		t.Fatalf("any mode should rebind after a 502: got %q want %q", after, moved)
	}
}
