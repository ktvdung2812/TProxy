package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/router"
)

func TestDashboardPasswordCacheAvoidsRepeatedDerivation(t *testing.T) {
	cfg := &config.Config{}
	dataStore := apiTestStore(t, cfg)
	server := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry()))
	defer server.Close()

	ctx := context.Background()
	if !server.verifyDashboardPassword(ctx, testDashboardPassword) {
		t.Fatal("correct password rejected")
	}

	// Point the store at a database that is closed, so any further derivation
	// would fail. A second success can then only come from the cache.
	if err := dataStore.Close(); err != nil {
		t.Fatal(err)
	}
	if !server.verifyDashboardPassword(ctx, testDashboardPassword) {
		t.Fatal("cached decision was not reused")
	}
}

func TestDashboardPasswordCacheRemembersRejection(t *testing.T) {
	cfg := &config.Config{}
	dataStore := apiTestStore(t, cfg)
	server := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry()))
	defer server.Close()

	ctx := context.Background()
	// A wrong password must be cached too, otherwise every retry still pays the
	// full 600k-iteration derivation and the CPU-exhaustion lever survives.
	if server.verifyDashboardPassword(ctx, "wrong-password") {
		t.Fatal("wrong password accepted")
	}
	matched, ok := server.dashboardPasswordCache.lookup("wrong-password")
	if !ok {
		t.Fatal("rejection was not cached")
	}
	if matched {
		t.Fatal("rejection cached as a match")
	}
}

func TestDashboardPasswordCacheExpires(t *testing.T) {
	cache := newDashboardPasswordCache()
	now := time.Now()
	cache.now = func() time.Time { return now }
	cache.store("token", true)

	if _, ok := cache.lookup("token"); !ok {
		t.Fatal("entry missing before expiry")
	}
	now = now.Add(dashboardPasswordCacheTTL + time.Second)
	if _, ok := cache.lookup("token"); ok {
		t.Fatal("entry survived its TTL")
	}
}

func TestDashboardPasswordCacheIsBounded(t *testing.T) {
	cache := newDashboardPasswordCache()
	for i := 0; i < dashboardPasswordCacheMaxSize*3; i++ {
		cache.store(strings.Repeat("t", i+1), false)
	}
	cache.mu.Lock()
	size := len(cache.entries)
	cache.mu.Unlock()
	if size > dashboardPasswordCacheMaxSize {
		t.Fatalf("cache grew to %d entries, ceiling is %d", size, dashboardPasswordCacheMaxSize)
	}
}

func TestChangingDashboardPasswordInvalidatesCache(t *testing.T) {
	cfg := &config.Config{}
	dataStore := apiTestStore(t, cfg)
	server := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry()))
	defer server.Close()
	handler := server.Handler()

	// Prime the cache with the bootstrap password.
	primed := httptest.NewRequest(http.MethodGet, "/api/admin/snapshot", nil)
	primed.RemoteAddr = "127.0.0.1:1234"
	withDefaultManagementAuth(primed)
	primedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(primedRecorder, primed)
	if primedRecorder.Code != http.StatusOK {
		t.Fatalf("bootstrap password status=%d body=%s", primedRecorder.Code, primedRecorder.Body.String())
	}

	change := httptest.NewRequest(http.MethodPut, "/api/admin/settings/dashboard-password",
		strings.NewReader(`{"current_password":"`+testDashboardPassword+`","new_password":"a-new-password"}`))
	change.RemoteAddr = "127.0.0.1:1234"
	withDefaultManagementAuth(change)
	changeRecorder := httptest.NewRecorder()
	handler.ServeHTTP(changeRecorder, change)
	if changeRecorder.Code != http.StatusOK {
		t.Fatalf("password change status=%d body=%s", changeRecorder.Code, changeRecorder.Body.String())
	}

	// The old password must stop working immediately rather than lingering for
	// the cache TTL.
	stale := httptest.NewRequest(http.MethodGet, "/api/admin/snapshot", nil)
	stale.RemoteAddr = "127.0.0.1:1234"
	withDefaultManagementAuth(stale)
	staleRecorder := httptest.NewRecorder()
	handler.ServeHTTP(staleRecorder, stale)
	if staleRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("retired password still accepted: status=%d body=%s", staleRecorder.Code, staleRecorder.Body.String())
	}

	fresh := httptest.NewRequest(http.MethodGet, "/api/admin/snapshot", nil)
	fresh.RemoteAddr = "127.0.0.1:1234"
	fresh.Header.Set("Authorization", "Bearer a-new-password")
	freshRecorder := httptest.NewRecorder()
	handler.ServeHTTP(freshRecorder, fresh)
	if freshRecorder.Code != http.StatusOK {
		t.Fatalf("new password rejected: status=%d body=%s", freshRecorder.Code, freshRecorder.Body.String())
	}
}

// BenchmarkManagementAuthLoopback guards the reason the cache exists: without
// it every management request re-derives PBKDF2-SHA256 at 600k iterations,
// which measured 67.5ms per request on an M1 Pro.
func BenchmarkManagementAuthLoopback(b *testing.B) {
	cfg := &config.Config{}
	dataStore := apiTestStore(b, cfg)
	server := NewServer(cfg, dataStore, router.New(dataStore, providers.NewRegistry()))
	defer server.Close()
	handler := server.Handler()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		request := httptest.NewRequest(http.MethodGet, "/api/admin/snapshot", nil)
		request.RemoteAddr = "127.0.0.1:1234"
		withDefaultManagementAuth(request)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			b.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
}
