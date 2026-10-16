package router

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/store"
)

func TestQuotaCacheCoalescesAndRecoversOnlyAutomaticDisable(t *testing.T) {
	var calls, used atomic.Int32
	var fail atomic.Bool
	used.Store(100)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"usage":{"limit":100,"used":%d,"remaining":%d}}`, used.Load(), 100-used.Load())
	}))
	defer upstream.Close()
	data := openTestStore(t, &config.Config{Providers: []config.ProviderConfig{{ID: "kimi", Type: "kimi", BaseURL: upstream.URL, Enabled: true, Credentials: []config.CredentialConfig{{ID: "a", AuthType: "api_key", Secret: "test-token"}}}}})
	r := New(data, providers.NewRegistry())
	provider, _ := data.Provider(context.Background(), "kimi")
	credential, _ := data.CredentialByID(context.Background(), "a")
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.RefreshQuotaIfDue(context.Background(), *provider, credential)
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("coalescing/due guard made %d probes", calls.Load())
	}
	credential, _ = data.CredentialByID(context.Background(), "a")
	if credential.Enabled || !store.QuotaAutoDisabled(credential.Metadata) {
		t.Fatal("depleted account stayed available")
	}
	snapshot := r.CachedQuota("a", "kimi")
	if snapshot.State != "finite" || snapshot.Stale || snapshot.CheckedAt.IsZero() {
		t.Fatal(snapshot)
	}
	reset := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
	r.quotaMu.Lock()
	entry := r.quotaCache["a"].Quota.Quotas["weekly"]
	entry.ResetAt = reset
	r.quotaCache["a"].Quota.Quotas["weekly"] = entry
	r.quotaMu.Unlock()
	model := store.PublicModel{ID: "kimi:direct", Enabled: true, Capabilities: []string{"text"}}
	_, blocked := r.Execute(context.Background(), model, canonical.Request{})
	if providers.Status(blocked) != http.StatusTooManyRequests || providers.RetryAfter(blocked) == "" {
		t.Fatalf("depleted pool error=%v", blocked)
	}
	if calls.Load() != 1 {
		t.Fatal("dispatch probed quota")
	}
	snapshot.Quota.Quotas["weekly"] = providers.QuotaEntry{Total: 1}
	if r.CachedQuota("a", "kimi").Quota.Quotas["weekly"].Total != 100 {
		t.Fatal("cache was mutable by readers")
	}
	used.Store(0)
	fail.Store(true)
	_, _ = r.CredentialQuota(context.Background(), *provider, credential)
	credential, _ = data.CredentialByID(context.Background(), "a")
	snapshot = r.CachedQuota("a", "kimi")
	if credential.Enabled || snapshot.State != "unknown" || !snapshot.Stale || snapshot.Quota == nil {
		t.Fatal("failed refresh erased the known depleted state")
	}
	fail.Store(false)
	r.quotaMu.Lock()
	cacheEntry := r.quotaCache["a"]
	cacheEntry.AttemptedAt = time.Now().Add(-2 * time.Minute)
	r.quotaCache["a"] = cacheEntry
	r.quotaMu.Unlock()
	_, _ = r.RefreshQuotaIfDue(context.Background(), *provider, credential)
	credential, _ = data.CredentialByID(context.Background(), "a")
	if !credential.Enabled || store.QuotaAutoDisabled(credential.Metadata) {
		t.Fatal("quota recovery did not restore the account")
	}
	off := false
	if err := data.SaveCredential(context.Background(), "kimi", config.CredentialConfig{ID: "a", AuthType: "api_key", Secret: "test-token", Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	credential, _ = data.CredentialByID(context.Background(), "a")
	_, _ = r.CredentialQuota(context.Background(), *provider, credential)
	credential, _ = data.CredentialByID(context.Background(), "a")
	if credential.Enabled {
		t.Fatal("probe overrode manual disable")
	}
	if QuotaRefreshInterval("claude") != 3*time.Minute || QuotaRefreshInterval("kimi") != time.Minute {
		t.Fatal("provider cadence changed")
	}
}

func TestQuotaProbeConcurrencyAndCancellation(t *testing.T) {
	var active, maximum atomic.Int32
	started, release := make(chan struct{}, 8), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		for old := maximum.Load(); n > old; old = maximum.Load() {
			if maximum.CompareAndSwap(old, n) {
				break
			}
		}
		defer active.Add(-1)
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, `{"usage":{"limit":100,"used":0,"remaining":100}}`)
	}))
	defer upstream.Close()
	data := openTestStore(t, &config.Config{})
	r := New(data, providers.NewRegistry())
	p := store.Provider{ID: "kimi", Type: "kimi", BaseURL: upstream.URL}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.CredentialQuota(context.Background(), p, store.Credential{ID: fmt.Sprint(i), AuthType: "api_key", Secret: "test"})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	for range 4 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			close(release)
			t.Fatal("probe did not start")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.CredentialQuota(ctx, p, store.Credential{ID: "cancelled", Secret: "test"}); err != context.Canceled {
		t.Fatalf("cancelled probe: %v", err)
	}
	close(release)
	wg.Wait()
	if maximum.Load() != 4 {
		t.Fatalf("concurrent probes=%d", maximum.Load())
	}
}
