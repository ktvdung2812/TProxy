package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/store"
)

func refreshServer(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		// A long-lived token: expiry alone would never trigger another refresh.
		_, _ = w.Write([]byte(`{"access_token":"rotated-access","refresh_token":"rotated-refresh","expires_in":2592000}`))
	}))
}

// A token that has not expired must still be rotated once it crosses the hard
// ceiling, otherwise a credential can keep the same access token indefinitely.
func TestTokenPastHardAgeIsRefreshedOnUse(t *testing.T) {
	t.Setenv("TPROXY_TEST_OAUTH_CLIENT", "test-client")
	var calls atomic.Int32
	server := refreshServer(t, &calls)
	defer server.Close()

	dataStore, _ := newAuthStore(t, oauthConfig(server.URL))
	ctx := context.Background()
	token := store.OAuthToken{
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(30 * 24 * time.Hour),
	}
	if err := dataStore.SaveOAuthCredential(ctx, "oauth-provider", "oauth-account", "", "", token); err != nil {
		t.Fatal(err)
	}
	provider, _ := dataStore.Provider(ctx, "oauth-provider")
	credentials, _ := dataStore.Credentials(ctx, "oauth-provider")

	manager := NewManager(dataStore, server.Client())
	defer manager.Close()
	// Jump past the 72h ceiling; the credential was stamped "now" on save.
	manager.now = func() time.Time { return time.Now().Add(defaultHardMaxTokenAge + time.Hour) }

	updated, err := manager.EnsureValid(ctx, *provider, credentials[0], false)
	if err != nil {
		t.Fatalf("EnsureValid: %v", err)
	}
	if updated.Secret != "rotated-access" {
		t.Errorf("access token = %q, want the rotated one", updated.Secret)
	}
	if calls.Load() != 1 {
		t.Errorf("refresh calls = %d, want 1", calls.Load())
	}
}

// Below the ceiling nothing should change: the request path must not refresh on
// every call just because a background rotation is due soon.
func TestTokenBelowHardAgeIsNotRefreshedOnUse(t *testing.T) {
	t.Setenv("TPROXY_TEST_OAUTH_CLIENT", "test-client")
	var calls atomic.Int32
	server := refreshServer(t, &calls)
	defer server.Close()

	dataStore, _ := newAuthStore(t, oauthConfig(server.URL))
	ctx := context.Background()
	token := store.OAuthToken{
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(30 * 24 * time.Hour),
	}
	if err := dataStore.SaveOAuthCredential(ctx, "oauth-provider", "oauth-account", "", "", token); err != nil {
		t.Fatal(err)
	}
	provider, _ := dataStore.Provider(ctx, "oauth-provider")
	credentials, _ := dataStore.Credentials(ctx, "oauth-provider")

	manager := NewManager(dataStore, server.Client())
	defer manager.Close()
	manager.now = func() time.Time { return time.Now().Add(defaultMaxTokenAge + time.Hour) }

	updated, err := manager.EnsureValid(ctx, *provider, credentials[0], false)
	if err != nil {
		t.Fatalf("EnsureValid: %v", err)
	}
	if updated.Secret != "old-access" {
		t.Errorf("access token = %q, want the original", updated.Secret)
	}
	if calls.Load() != 0 {
		t.Errorf("refresh calls = %d, want 0", calls.Load())
	}
}

// The background loop owns the 48h rotation, including tokens the provider gave
// no expiry for — those were previously skipped outright and never refreshed.
func TestBackgroundLoopRotatesTokenWithoutExpiry(t *testing.T) {
	t.Setenv("TPROXY_TEST_OAUTH_CLIENT", "test-client")
	var calls atomic.Int32
	server := refreshServer(t, &calls)
	defer server.Close()

	dataStore, _ := newAuthStore(t, oauthConfig(server.URL))
	ctx := context.Background()
	token := store.OAuthToken{AccessToken: "old-access", RefreshToken: "old-refresh", TokenType: "Bearer"}
	if err := dataStore.SaveOAuthCredential(ctx, "oauth-provider", "oauth-account", "", "", token); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(dataStore, server.Client())
	defer manager.Close()

	manager.now = func() time.Time { return time.Now().Add(defaultMaxTokenAge - time.Hour) }
	manager.refreshExpiring(ctx)
	if calls.Load() != 0 {
		t.Fatalf("refresh calls before the age threshold = %d, want 0", calls.Load())
	}

	manager.now = func() time.Time { return time.Now().Add(defaultMaxTokenAge + time.Minute) }
	manager.refreshExpiring(ctx)
	if calls.Load() != 1 {
		t.Fatalf("refresh calls after the age threshold = %d, want 1", calls.Load())
	}

	credentials, _ := dataStore.Credentials(ctx, "oauth-provider")
	if credentials[0].OAuthToken == nil || credentials[0].OAuthToken.AccessToken != "rotated-access" {
		t.Errorf("stored token was not rotated: %+v", credentials[0].OAuthToken)
	}
}

// A failing provider must not turn the 30s background tick into a hot loop.
func TestAgeBasedRefreshFailureIsThrottled(t *testing.T) {
	t.Setenv("TPROXY_TEST_OAUTH_CLIENT", "test-client")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"server_error"}`))
	}))
	defer server.Close()

	dataStore, _ := newAuthStore(t, oauthConfig(server.URL))
	ctx := context.Background()
	token := store.OAuthToken{AccessToken: "old-access", RefreshToken: "old-refresh", TokenType: "Bearer"}
	if err := dataStore.SaveOAuthCredential(ctx, "oauth-provider", "oauth-account", "", "", token); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(dataStore, server.Client())
	defer manager.Close()
	base := time.Now().Add(defaultMaxTokenAge + time.Minute)
	manager.now = func() time.Time { return base }

	manager.refreshExpiring(ctx)
	manager.refreshExpiring(ctx)
	manager.refreshExpiring(ctx)
	if got := calls.Load(); got != 1 {
		t.Fatalf("refresh calls within the retry interval = %d, want 1", got)
	}

	base = base.Add(ageRefreshRetryInterval + time.Minute)
	manager.refreshExpiring(ctx)
	if got := calls.Load(); got != 2 {
		t.Fatalf("refresh calls after the retry interval = %d, want 2", got)
	}
}

// Rotation is impossible without a refresh token; such credentials must be left
// alone rather than being marked as needing re-authorization.
func TestTokenWithoutRefreshTokenIsNeverAgedOut(t *testing.T) {
	credential := store.Credential{
		OAuthToken:    &store.OAuthToken{AccessToken: "only-access"},
		LastValidated: time.Now().Add(-30 * 24 * time.Hour),
	}
	if tokenOlderThan(credential, defaultMaxTokenAge, time.Now()) {
		t.Error("a credential with no refresh token must never be reported as stale")
	}
}

func TestTokenAgePolicyDefaultsAndOverrides(t *testing.T) {
	if maxTokenAge(nil) != 48*time.Hour {
		t.Errorf("default soft age = %v, want 48h", maxTokenAge(nil))
	}
	if hardMaxTokenAge(nil) != 72*time.Hour {
		t.Errorf("default hard age = %v, want 72h", hardMaxTokenAge(nil))
	}
	cfg := &config.OAuthConfig{MaxTokenAge: "6h", HardMaxTokenAge: "12h"}
	if maxTokenAge(cfg) != 6*time.Hour || hardMaxTokenAge(cfg) != 12*time.Hour {
		t.Errorf("overrides not applied: %v / %v", maxTokenAge(cfg), hardMaxTokenAge(cfg))
	}
	// A ceiling below the soft threshold would refresh on every request.
	clamped := &config.OAuthConfig{MaxTokenAge: "10h", HardMaxTokenAge: "1h"}
	if hardMaxTokenAge(clamped) != 10*time.Hour {
		t.Errorf("hard age = %v, want it clamped up to the soft age", hardMaxTokenAge(clamped))
	}
	invalid := &config.OAuthConfig{MaxTokenAge: "nonsense", HardMaxTokenAge: "-5h"}
	if maxTokenAge(invalid) != 48*time.Hour || hardMaxTokenAge(invalid) != 72*time.Hour {
		t.Error("invalid durations must fall back to the defaults")
	}
}

// The dashboard must be able to show when a token rotates next, so operators can
// verify the 48h/72h policy without reading logs.
func TestCredentialStatusReportsRefreshSchedule(t *testing.T) {
	t.Setenv("TPROXY_TEST_OAUTH_CLIENT", "test-client")
	var calls atomic.Int32
	server := refreshServer(t, &calls)
	defer server.Close()

	dataStore, _ := newAuthStore(t, oauthConfig(server.URL))
	ctx := context.Background()
	token := store.OAuthToken{AccessToken: "a", RefreshToken: "r", TokenType: "Bearer"}
	if err := dataStore.SaveOAuthCredential(ctx, "oauth-provider", "oauth-account", "", "", token); err != nil {
		t.Fatal(err)
	}
	credentials, _ := dataStore.Credentials(ctx, "oauth-provider")

	manager := NewManager(dataStore, server.Client())
	defer manager.Close()

	status, err := manager.CredentialStatus(ctx, credentials[0].ID)
	if err != nil {
		t.Fatalf("CredentialStatus: %v", err)
	}
	if status.RefreshedAt == nil || status.NextRefreshAt == nil || status.MaxRefreshAt == nil {
		t.Fatalf("schedule missing: %+v", status)
	}
	if got := status.NextRefreshAt.Sub(*status.RefreshedAt); got != defaultMaxTokenAge {
		t.Errorf("next refresh offset = %v, want %v", got, defaultMaxTokenAge)
	}
	if got := status.MaxRefreshAt.Sub(*status.RefreshedAt); got != defaultHardMaxTokenAge {
		t.Errorf("max refresh offset = %v, want %v", got, defaultHardMaxTokenAge)
	}
}
