package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/store"
)

func TestAccountAdmissionQueueCancellationAndRelease(t *testing.T) {
	r := &Router{}
	c := store.Credential{ID: "a", Metadata: map[string]any{"max_concurrent_requests": 1, "max_queue_size": 1, "queue_timeout_ms": 1000}}
	release, _, err := r.acquireAccount(context.Background(), c, true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, _, err := r.acquireAccount(ctx, c, true); done <- err }()
	deadline := time.After(time.Second)
	for r.AccountRuntime()[0].Waiting != 1 {
		select {
		case <-deadline:
			t.Fatal("request did not queue")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if _, _, err = r.acquireAccount(context.Background(), c, true); providers.Code(err) != "account_queue_full" {
		t.Fatalf("queue overflow: %v", err)
	}
	cancel()
	if err = <-done; err != context.Canceled {
		t.Fatal(err)
	}
	release()
	release()
	if got := r.AccountRuntime()[0]; got.InFlight != 0 || got.Waiting != 0 {
		t.Fatalf("leaked slot: %+v", got)
	}
	c.Metadata["queue_timeout_ms"] = 5
	release, _, _ = r.acquireAccount(context.Background(), c, true)
	if _, _, err = r.acquireAccount(context.Background(), c, true); providers.Code(err) != "account_queue_timeout" {
		t.Fatalf("timeout: %v", err)
	}
	release()
}

func TestAccountAdmissionConcurrentCeiling(t *testing.T) {
	r := &Router{}
	c := store.Credential{ID: "a", Metadata: map[string]any{"max_concurrent_requests": 2, "max_queue_size": 100, "queue_timeout_ms": 1000}}
	var wg sync.WaitGroup
	var active atomic.Int32
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, _, err := r.acquireAccount(context.Background(), c, true)
			if err != nil {
				t.Error(err)
				return
			}
			if active.Add(1) > 2 {
				t.Error("capacity exceeded")
			}
			time.Sleep(time.Millisecond)
			active.Add(-1)
			release()
		}()
	}
	wg.Wait()
	if got := r.AccountRuntime()[0]; got.InFlight != 0 || got.Waiting != 0 {
		t.Fatalf("leak: %+v", got)
	}
}

func TestMeasuredRoutingUsesObservations(t *testing.T) {
	r := &Router{}
	credentials := []store.Credential{{ID: "slow"}, {ID: "fast"}}
	r.recordAccount(Selection{Credential: credentials[0], StartedAt: time.Now().Add(-time.Second)}, 200, nil)
	r.recordAccount(Selection{Credential: credentials[1], StartedAt: time.Now().Add(-10 * time.Millisecond)}, 200, nil)
	if got := r.measuredOrder("latency-aware", credentials); got[0].ID != "fast" {
		t.Fatalf("order: %+v", got)
	}
	r.recordAccount(Selection{Credential: credentials[1], StartedAt: time.Now()}, 503, nil)
	if got := r.measuredOrder("health-first", credentials); got[0].ID != "slow" {
		t.Fatalf("health order: %+v", got)
	}
}

func TestBusyAccountFallsThroughWithoutSpendingRetryBudget(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ok","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()
	data := openTestStore(t, &config.Config{
		Providers: []config.ProviderConfig{{ID: "p", Type: "openai-compatible", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{
			{ID: "busy", AuthType: "none", Priority: 2, Metadata: map[string]any{"max_concurrent_requests": 1, "queue_timeout_ms": 10000}},
			{ID: "free", AuthType: "none", Priority: 1},
		}}},
		Models: []config.PublicModelConfig{{ID: "m", Enabled: true, Routes: []config.RouteTargetConfig{{ID: "r", Provider: "p", UpstreamModel: "m"}}}},
	})
	r := New(data, providers.NewRegistry())
	r.ConfigureRouting(config.RoutingConfig{Strategy: "fill-first", Retry: config.RetryConfig{MaxCredentials: 1}})
	busy, _ := data.CredentialByID(context.Background(), "busy")
	release, _, err := r.acquireAccount(context.Background(), busy, false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	model, err := r.Resolve(context.Background(), "m", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := r.Execute(ctx, *model, canonical.Request{RequestID: "busy-fallback", Messages: []canonical.Message{{Role: "user", Content: "hi"}}})
	if err != nil || result.Selection.Credential.ID != "free" {
		t.Fatalf("free account not used: %+v %v", result, err)
	}
	if result.Selection.Attempt != 2 {
		t.Fatal("busy account was not first candidate")
	}
}

func TestRoutingSessionIdentityIsBoundedAndScoped(t *testing.T) {
	request := canonical.Request{SessionID: strings.Repeat("session", 10000), Metadata: map[string]any{"client_api_key_id": "a", "team": "one"}}
	first := routingSessionID(request)
	if len(first) != 64 || first != routingSessionID(request) {
		t.Fatal("session key must be bounded and stable")
	}
	request.Metadata["client_api_key_id"] = "b"
	if first == routingSessionID(request) {
		t.Fatal("API keys share affinity")
	}
	request.Metadata["client_api_key_id"] = "a"
	request.Metadata["team"] = "two"
	if first == routingSessionID(request) {
		t.Fatal("teams share affinity")
	}
}
