package auth

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/security"
	"github.com/tproxy/tproxy/internal/store"
	_ "modernc.org/sqlite"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestBrowserPKCEStateSingleUseAndEncryptedToken(t *testing.T) {
	t.Setenv("TPROXY_TEST_OAUTH_CLIENT", "test-client")
	var tokenCalls atomic.Int32
	var receivedVerifier string
	var expectedChallenge string
	var providerServer *httptest.Server
	providerServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		tokenCalls.Add(1)
		_ = r.ParseForm()
		receivedVerifier = r.Form.Get("code_verifier")
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "accepted-code" {
			t.Errorf("unexpected token form: %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access-secret","refresh_token":"refresh-secret","token_type":"Bearer","expires_in":3600,"id_token":"identity-secret"}`))
	}))
	defer providerServer.Close()

	cfg := oauthConfig(providerServer.URL)
	dataStore, databasePath := newAuthStore(t, cfg)
	manager := NewManager(dataStore, providerServer.Client())
	defer manager.Close()

	started, err := manager.StartAuthorization(context.Background(), StartRequest{ProviderID: "oauth-provider", CredentialID: "oauth-account", Label: "Primary", Mode: "browser"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(started.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	state := parsed.Query().Get("state")
	expectedChallenge = parsed.Query().Get("code_challenge")
	if state == "" || expectedChallenge == "" || parsed.Query().Get("code_verifier") != "" {
		t.Fatalf("invalid authorization URL: %s", started.AuthorizationURL)
	}
	completed, err := manager.CompleteCallback(context.Background(), state, "accepted-code")
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "complete" || completed.CredentialID != "oauth-account" {
		t.Fatalf("callback status = %+v", completed)
	}
	if receivedVerifier == "" || pkceChallenge(receivedVerifier) != expectedChallenge {
		t.Fatalf("PKCE verifier did not match challenge")
	}
	if tokenCalls.Load() != 1 {
		t.Fatalf("token calls = %d", tokenCalls.Load())
	}
	if _, err = manager.CompleteCallback(context.Background(), state, "accepted-code"); Code(err) != "invalid_state" {
		t.Fatalf("reused callback error = %v code=%s", err, Code(err))
	}
	if tokenCalls.Load() != 1 {
		t.Fatalf("reused callback exchanged a token")
	}

	credentials, err := dataStore.Credentials(context.Background(), "oauth-provider")
	if err != nil || len(credentials) != 1 {
		t.Fatalf("credentials=%+v err=%v", credentials, err)
	}
	if credentials[0].Secret != "access-secret" || credentials[0].OAuthToken == nil || credentials[0].OAuthToken.RefreshToken != "refresh-secret" {
		t.Fatalf("stored token = %+v", credentials[0].OAuthToken)
	}
	snapshot, err := dataStore.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON, _ := json.Marshal(snapshot)
	if strings.Contains(string(snapshotJSON), "access-secret") || strings.Contains(string(snapshotJSON), "refresh-secret") || strings.Contains(string(snapshotJSON), "identity-secret") {
		t.Fatalf("snapshot leaked OAuth token: %s", snapshotJSON)
	}

	inspection, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Close()
	var ciphertext, metadata string
	if err = inspection.QueryRow(`SELECT secret_ciphertext,metadata_json FROM credentials WHERE id='oauth-account'`).Scan(&ciphertext, &metadata); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"access-secret", "refresh-secret", "identity-secret"} {
		if strings.Contains(ciphertext, secret) || strings.Contains(metadata, secret) {
			t.Fatalf("database leaked %q", secret)
		}
	}
}

func TestConcurrentRefreshIsDeduplicated(t *testing.T) {
	t.Setenv("TPROXY_TEST_OAUTH_CLIENT", "test-client")
	var refreshCalls atomic.Int32
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCalls.Add(1)
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "old-refresh" {
			t.Errorf("unexpected refresh form: %v", r.Form)
		}
		time.Sleep(40 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`))
	}))
	defer providerServer.Close()

	dataStore, _ := newAuthStore(t, oauthConfig(providerServer.URL))
	if err := dataStore.SaveOAuthCredential(context.Background(), "oauth-provider", "oauth-account", "", "", store.OAuthToken{AccessToken: "old-access", RefreshToken: "old-refresh", TokenType: "Bearer", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	provider, _ := dataStore.Provider(context.Background(), "oauth-provider")
	credentials, _ := dataStore.Credentials(context.Background(), "oauth-provider")
	manager := NewManager(dataStore, providerServer.Client())
	defer manager.Close()

	const concurrency = 16
	start := make(chan struct{})
	results := make(chan store.Credential, concurrency)
	errorsCh := make(chan error, concurrency)
	var wait sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			credential, err := manager.EnsureValid(context.Background(), *provider, credentials[0], false)
			results <- credential
			errorsCh <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	for credential := range results {
		if credential.Secret != "new-access" {
			t.Fatalf("refreshed access token = %q", credential.Secret)
		}
	}
	if refreshCalls.Load() != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls.Load())
	}
}

func TestPermanentRefreshRejectionRequiresAuthorization(t *testing.T) {
	t.Setenv("TPROXY_TEST_OAUTH_CLIENT", "test-client")
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"expired refresh token refresh-secret"}`))
	}))
	defer providerServer.Close()
	dataStore, _ := newAuthStore(t, oauthConfig(providerServer.URL))
	if err := dataStore.SaveOAuthCredential(context.Background(), "oauth-provider", "oauth-account", "", "", store.OAuthToken{AccessToken: "old-access", RefreshToken: "refresh-secret", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	provider, _ := dataStore.Provider(context.Background(), "oauth-provider")
	credentials, _ := dataStore.Credentials(context.Background(), "oauth-provider")
	manager := NewManager(dataStore, providerServer.Client())
	defer manager.Close()
	_, err := manager.EnsureValid(context.Background(), *provider, credentials[0], false)
	if err == nil || !IsPermanent(err) {
		t.Fatalf("refresh error = %v permanent=%v", err, IsPermanent(err))
	}
	credentials, _ = dataStore.Credentials(context.Background(), "oauth-provider")
	if credentials[0].Status != "auth_required" || strings.Contains(credentials[0].LastError, "refresh-secret") {
		t.Fatalf("credential state = %+v", credentials[0])
	}
	status, err := manager.CredentialStatus(context.Background(), "oauth-account")
	if err != nil || status.Status != "auth_required" {
		t.Fatalf("credential status = %+v err=%v", status, err)
	}
}

func TestRefreshUnauthorizedResponseRequiresAuthorization(t *testing.T) {
	t.Setenv("TPROXY_TEST_OAUTH_CLIENT", "test-client")
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`refresh-secret must not be returned`))
	}))
	defer providerServer.Close()
	dataStore, _ := newAuthStore(t, oauthConfig(providerServer.URL))
	if err := dataStore.SaveOAuthCredential(context.Background(), "oauth-provider", "oauth-account", "", "", store.OAuthToken{AccessToken: "old-access", RefreshToken: "refresh-secret", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	provider, _ := dataStore.Provider(context.Background(), "oauth-provider")
	credentials, _ := dataStore.Credentials(context.Background(), "oauth-provider")
	manager := NewManager(dataStore, providerServer.Client())
	defer manager.Close()
	_, err := manager.EnsureValid(context.Background(), *provider, credentials[0], false)
	if err == nil || !IsPermanent(err) || strings.Contains(err.Error(), "refresh-secret") {
		t.Fatalf("refresh error = %v permanent=%v", err, IsPermanent(err))
	}
	credentials, _ = dataStore.Credentials(context.Background(), "oauth-provider")
	if credentials[0].Status != "auth_required" {
		t.Fatalf("credential status = %q, want auth_required", credentials[0].Status)
	}
}

func TestOAuthProviderErrorCodeCannotExposeSecretText(t *testing.T) {
	err := oauthHTTPError([]byte(`{"error":"refresh-secret"}`), http.StatusBadRequest, true)
	if Code(err) != "oauth_provider_unavailable" || strings.Contains(Code(err), "refresh-secret") || strings.Contains(err.Error(), "refresh-secret") {
		t.Fatalf("provider error was not normalized: code=%q err=%v", Code(err), err)
	}
}

func TestOAuthExtraParamsCannotOverrideProtocolFields(t *testing.T) {
	authorization := authorizationURL(config.OAuthConfig{
		AuthorizationURL: "https://login.example/authorize",
		ExtraAuthParams: map[string]string{
			"client_id":             "attacker-client",
			"state":                 "attacker-state",
			"code_challenge":        "attacker-challenge",
			"code_challenge_method": "plain",
		},
	}, "expected-state", "expected-verifier", "https://gateway.example/callback", "expected-client")
	parsed, err := url.Parse(authorization)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("client_id") != "expected-client" || query.Get("state") != "expected-state" || query.Get("code_challenge") != pkceChallenge("expected-verifier") || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization protocol fields were overridden: %v", query)
	}

	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "expected-refresh" || r.Form.Get("client_id") != "expected-client" {
			t.Errorf("refresh protocol fields were overridden: %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"refreshed-access","expires_in":3600}`))
	}))
	defer providerServer.Close()
	manager := NewManager(nil, providerServer.Client())
	defer manager.Close()
	_, err = manager.exchangeRefresh(context.Background(), config.OAuthConfig{
		TokenURL: providerServer.URL,
		ClientID: "expected-client",
		ExtraTokenParams: map[string]string{
			"grant_type":    "client_credentials",
			"refresh_token": "attacker-refresh",
			"client_id":     "attacker-client",
		},
	}, "expected-refresh")
	if err != nil {
		t.Fatal(err)
	}
}

func TestBrowserOnlyDiscoveryDoesNotRequireDeviceEndpoint(t *testing.T) {
	t.Setenv("TPROXY_TEST_OAUTH_CLIENT", "test-client")
	var tokenCalls atomic.Int32
	var providerServer *httptest.Server
	providerServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/discovery":
			_, _ = w.Write([]byte(`{"authorization_endpoint":"` + providerServer.URL + `/authorize","token_endpoint":"` + providerServer.URL + `/token"}`))
		case "/token":
			tokenCalls.Add(1)
			_, _ = w.Write([]byte(`{"access_token":"discovery-access","refresh_token":"discovery-refresh","expires_in":3600}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer providerServer.Close()
	cfg := oauthConfig(providerServer.URL)
	cfg.Providers[0].OAuth.AuthorizationURL = ""
	cfg.Providers[0].OAuth.TokenURL = ""
	cfg.Providers[0].OAuth.DiscoveryURL = providerServer.URL + "/discovery"
	dataStore, _ := newAuthStore(t, cfg)
	manager := NewManager(dataStore, providerServer.Client())
	defer manager.Close()
	started, err := manager.StartAuthorization(context.Background(), StartRequest{ProviderID: "oauth-provider", CredentialID: "discovery-account", Mode: "browser"})
	if err != nil {
		t.Fatal(err)
	}
	authorization, _ := url.Parse(started.AuthorizationURL)
	if _, err = manager.CompleteCallback(context.Background(), authorization.Query().Get("state"), "discovery-code"); err != nil {
		t.Fatal(err)
	}
	if tokenCalls.Load() != 1 {
		t.Fatalf("token calls = %d", tokenCalls.Load())
	}
}

func TestDeviceFlowTreatsSlowDownAsPending(t *testing.T) {
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"slow_down"}`))
	}))
	defer providerServer.Close()
	manager := NewManager(nil, providerServer.Client())
	defer manager.Close()
	token, pending, err := manager.exchangeDeviceCode(context.Background(), config.OAuthConfig{ClientID: "device-client", TokenURL: providerServer.URL + "/token"}, "device-code")
	if !pending || Code(err) != "slow_down" || token.AccessToken != "" {
		t.Fatalf("slow_down result token=%+v pending=%v err=%v", token, pending, err)
	}
}

func TestCancelledBrowserFlowCannotPersistCredential(t *testing.T) {
	t.Setenv("TPROXY_TEST_OAUTH_CLIENT", "test-client")
	requestStarted := make(chan struct{})
	release := make(chan struct{})
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"cancelled-access","refresh_token":"cancelled-refresh","expires_in":3600}`))
	}))
	defer providerServer.Close()
	dataStore, _ := newAuthStore(t, oauthConfig(providerServer.URL))
	manager := NewManager(dataStore, providerServer.Client())
	defer manager.Close()
	started, err := manager.StartAuthorization(context.Background(), StartRequest{ProviderID: "oauth-provider", CredentialID: "cancelled-account", Mode: "browser"})
	if err != nil {
		t.Fatal(err)
	}
	authorization, _ := url.Parse(started.AuthorizationURL)
	result := make(chan error, 1)
	go func() {
		_, callbackErr := manager.CompleteCallback(context.Background(), authorization.Query().Get("state"), "cancelled-code")
		result <- callbackErr
	}()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("token exchange did not start")
	}
	if err = manager.CancelSession(started.SessionID); err != nil {
		t.Fatal(err)
	}
	close(release)
	if callbackErr := <-result; callbackErr == nil || Code(callbackErr) != "invalid_state" {
		t.Fatalf("callback error after cancellation = %v", callbackErr)
	}
	credentials, err := dataStore.Credentials(context.Background(), "oauth-provider")
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 0 {
		t.Fatalf("cancelled callback persisted credentials: %+v", credentials)
	}
}

func TestCopilotOAuthUsesGenericRefreshPathAndDoesNotPersistDerivedToken(t *testing.T) {
	dataStore, _ := newAuthStore(t, &config.Config{Providers: []config.ProviderConfig{{
		ID: "copilot", Type: "copilot", Enabled: true,
		OAuth: &config.OAuthConfig{ClientID: "copilot-client", TokenURL: "https://oauth.example/token", RefreshSafetyWindow: "1m"},
	}}})
	if err := dataStore.SaveOAuthCredential(context.Background(), "copilot", "copilot-account", "", "", store.OAuthToken{AccessToken: "github-access", RefreshToken: "github-refresh", ExpiresAt: time.Now().Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.UpdateCredentialMetadata(context.Background(), "copilot-account", map[string]any{"copilot_exchange_unsupported": true, "proxy_pool_ids": []string{"egress"}}); err != nil {
		t.Fatal(err)
	}
	provider, _ := dataStore.Provider(context.Background(), "copilot")
	credentials, _ := dataStore.Credentials(context.Background(), "copilot")
	manager := NewManager(dataStore, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected Copilot exchange request: %s", request.URL)
		return nil, nil
	})})
	defer manager.Close()
	updated, err := manager.EnsureValid(context.Background(), *provider, credentials[0], false)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Secret != "github-access" {
		t.Fatalf("derived token = %q", updated.Secret)
	}
	credentials, _ = dataStore.Credentials(context.Background(), "copilot")
	if got := stringValue(credentials[0].Metadata["copilot_api_token"]); got != "" {
		t.Fatalf("derived token persisted in metadata: %q", got)
	}
	if len(credentials[0].ProxyPoolIDs) != 1 || credentials[0].ProxyPoolIDs[0] != "egress" {
		t.Fatalf("proxy pool binding was dropped: %+v", credentials[0].ProxyPoolIDs)
	}
}

func TestCopilotExchangeErrorRedactsResponseBody(t *testing.T) {
	manager := NewManager(nil, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader(`upstream refresh-secret`)), Header: make(http.Header)}, nil
	})})
	defer manager.Close()
	_, err := manager.exchangeCopilotToken(context.Background(), "copilot-account", "github-secret")
	if err == nil || strings.Contains(err.Error(), "refresh-secret") {
		t.Fatalf("exchange error leaked response body: %v", err)
	}
}

func TestCopilotExchangeRejectsUnsafeEndpoint(t *testing.T) {
	manager := NewManager(nil, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"token":"copilot-api-token","expires_at":4102444800,"endpoints":{"api":"https://evil.example"}}`)),
			Header:     make(http.Header),
		}, nil
	})})
	defer manager.Close()
	_, err := manager.exchangeCopilotToken(context.Background(), "copilot-account", "github-secret")
	if err == nil || Code(err) != "oauth_configuration_invalid" || strings.Contains(err.Error(), "copilot-api-token") {
		t.Fatalf("unsafe endpoint result = %v code=%s", err, Code(err))
	}
}

func TestCopilotAPIEndpointAllowlist(t *testing.T) {
	accepted := []string{
		"https://api.githubcopilot.com",
		"https://edge.githubcopilot.com/v1",
		"https://copilot-api.githubusercontent.com/",
	}
	for _, endpoint := range accepted {
		got, err := validateCopilotAPIEndpoint(endpoint)
		if err != nil || got == "" {
			t.Errorf("endpoint %q rejected: %v", endpoint, err)
		}
	}
	rejected := []string{
		"http://api.githubcopilot.com",
		"https://githubcopilot.com:8443",
		"https://evil.example",
		"https://api.githubcopilot.com@evil.example",
		"https://127.0.0.1",
	}
	for _, endpoint := range rejected {
		if _, err := validateCopilotAPIEndpoint(endpoint); err == nil {
			t.Errorf("endpoint %q unexpectedly accepted", endpoint)
		}
	}
}

func TestValidateRedirectURLRequiresSecureRemoteTransport(t *testing.T) {
	accepted := []string{
		"http://127.0.0.1:8317/oauth/callback",
		"http://localhost:1455/auth/callback",
		"https://gateway.example.com/api/admin/oauth/callback",
	}
	for _, redirectURL := range accepted {
		if err := validateRedirectURL(redirectURL); err != nil {
			t.Errorf("redirect %q rejected: %v", redirectURL, err)
		}
	}
	rejected := []string{
		"http://gateway.example.com/oauth/callback",
		"https://user:password@gateway.example.com/oauth/callback",
		"https://gateway.example.com/oauth/callback#fragment",
		"javascript:alert(1)",
	}
	for _, redirectURL := range rejected {
		if err := validateRedirectURL(redirectURL); err == nil {
			t.Errorf("redirect %q unexpectedly accepted", redirectURL)
		}
	}
}

func TestTerminalSessionClearsAuthorizationMaterial(t *testing.T) {
	manager := NewManager(nil, nil)
	defer manager.Close()
	item := &session{
		state:          "oauth-state-secret",
		verifier:       "pkce-verifier-secret",
		deviceCode:     "device-code-secret",
		deviceUserCode: "device-user-code",
		status:         "pending",
		cancel:         make(chan struct{}),
	}
	manager.completeSession(item)
	item.mu.Lock()
	defer item.mu.Unlock()
	if item.state != "" || item.verifier != "" || item.deviceCode != "" || item.deviceUserCode != "" {
		t.Fatalf("terminal session retained authorization material: %+v", item)
	}
}

func TestPurgeExpiredSessionsExpiresAbandonedSessionBeforeRemoval(t *testing.T) {
	manager := NewManager(nil, nil)
	defer manager.Close()

	now := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	item := &session{
		id:             "abandoned-browser-session",
		state:          "oauth-state-secret",
		verifier:       "pkce-verifier-secret",
		deviceCode:     "device-code-secret",
		deviceUserCode: "device-user-code",
		status:         "pending",
		expiresAt:      now.Add(time.Minute),
		cancel:         make(chan struct{}),
	}
	manager.sessions[item.id] = item

	now = item.expiresAt.Add(time.Nanosecond)
	manager.PurgeExpiredSessions(time.Hour)

	status := manager.statusFor(item)
	if status.Status != "expired" || status.ErrorCode != "invalid_state" {
		t.Fatalf("purged session status = %+v", status)
	}
	item.mu.Lock()
	if item.state != "" || item.verifier != "" || item.deviceCode != "" || item.deviceUserCode != "" {
		item.mu.Unlock()
		t.Fatalf("expired session retained authorization material: %+v", item)
	}
	if !item.cancelClosed {
		item.mu.Unlock()
		t.Fatal("expired session was not stopped")
	}
	item.mu.Unlock()
	if _, exists := manager.sessions[item.id]; !exists {
		t.Fatal("expired session was removed before retention elapsed")
	}

	now = item.expiresAt.Add(time.Hour + time.Nanosecond)
	manager.PurgeExpiredSessions(time.Hour)
	if _, exists := manager.sessions[item.id]; exists {
		t.Fatal("expired session was retained after retention elapsed")
	}
}

func TestFailSessionPreservesCancellation(t *testing.T) {
	manager := NewManager(nil, nil)
	defer manager.Close()
	item := &session{status: "cancelled", cancel: make(chan struct{})}
	manager.failSession(item, "oauth_provider_unavailable")
	item.mu.Lock()
	defer item.mu.Unlock()
	if item.status != "cancelled" {
		t.Fatalf("cancelled session changed to %q", item.status)
	}
}

func TestDeviceFlowPollsUntilAuthorized(t *testing.T) {
	t.Setenv("TPROXY_TEST_OAUTH_CLIENT", "test-client")
	var polls atomic.Int32
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/device":
			_, _ = w.Write([]byte(`{"device_code":"device-secret","user_code":"ABCD-EFGH","verification_uri":"https://login.example/device","expires_in":60,"interval":1}`))
		case "/token":
			if polls.Add(1) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"device-access","refresh_token":"device-refresh","expires_in":3600}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer providerServer.Close()
	cfg := oauthConfig(providerServer.URL)
	cfg.Providers[0].OAuth.DeviceCodeURL = providerServer.URL + "/device"
	dataStore, _ := newAuthStore(t, cfg)
	manager := NewManager(dataStore, providerServer.Client())
	defer manager.Close()
	started, err := manager.StartAuthorization(context.Background(), StartRequest{ProviderID: "oauth-provider", CredentialID: "device-account", Mode: "device"})
	if err != nil {
		t.Fatal(err)
	}
	if started.UserCode != "ABCD-EFGH" || strings.Contains(mustJSON(started), "device-secret") {
		t.Fatalf("device response = %+v", started)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, statusErr := manager.SessionStatus(started.SessionID)
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		if status.Status == "complete" {
			credentials, _ := dataStore.Credentials(context.Background(), "oauth-provider")
			if len(credentials) != 1 || credentials[0].Secret != "device-access" {
				t.Fatalf("device credential = %+v", credentials)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("device session did not complete; polls=%d", polls.Load())
}

func TestLocalCallbackListenerCompletesBrowserFlow(t *testing.T) {
	t.Setenv("TPROXY_TEST_OAUTH_CLIENT", "test-client")
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"local-access","refresh_token":"local-refresh","expires_in":3600}`))
	}))
	defer providerServer.Close()
	callbackProbe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	callbackAddress := callbackProbe.Addr().String()
	_ = callbackProbe.Close()
	cfg := oauthConfig(providerServer.URL)
	cfg.Providers[0].OAuth.RedirectURL = "http://" + callbackAddress + "/oauth/callback"
	cfg.Providers[0].OAuth.ListenForCallback = true
	dataStore, _ := newAuthStore(t, cfg)
	manager := NewManager(dataStore, providerServer.Client())
	defer manager.Close()
	started, err := manager.StartAuthorization(context.Background(), StartRequest{ProviderID: "oauth-provider", CredentialID: "local-account", Mode: "browser"})
	if err != nil {
		t.Fatal(err)
	}
	authorization, _ := url.Parse(started.AuthorizationURL)
	callbackURL := "http://" + callbackAddress + "/oauth/callback?state=" + url.QueryEscape(authorization.Query().Get("state")) + "&code=local-code"
	response, err := http.Get(callbackURL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("callback status=%d", response.StatusCode)
	}
	status, err := manager.SessionStatus(started.SessionID)
	if err != nil || status.Status != "complete" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestLocalCallbackListenerAcceptsFormPost(t *testing.T) {
	t.Setenv("TPROXY_TEST_OAUTH_CLIENT", "test-client")
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"post-access","refresh_token":"post-refresh","expires_in":3600}`))
	}))
	defer providerServer.Close()
	callbackProbe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	callbackAddress := callbackProbe.Addr().String()
	_ = callbackProbe.Close()
	cfg := oauthConfig(providerServer.URL)
	cfg.Providers[0].OAuth.RedirectURL = "http://" + callbackAddress + "/oauth/callback"
	cfg.Providers[0].OAuth.ListenForCallback = true
	dataStore, _ := newAuthStore(t, cfg)
	manager := NewManager(dataStore, providerServer.Client())
	defer manager.Close()
	started, err := manager.StartAuthorization(context.Background(), StartRequest{ProviderID: "oauth-provider", CredentialID: "post-account", Mode: "browser"})
	if err != nil {
		t.Fatal(err)
	}
	authorization, _ := url.Parse(started.AuthorizationURL)
	callbackURL := "http://" + callbackAddress + "/oauth/callback"
	response, err := http.PostForm(callbackURL, url.Values{"state": {authorization.Query().Get("state")}, "code": {"post-code"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("callback status=%d", response.StatusCode)
	}
	status, err := manager.SessionStatus(started.SessionID)
	if err != nil || status.Status != "complete" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestJSONTokenExchangeIncludesState(t *testing.T) {
	t.Setenv("TPROXY_TEST_OAUTH_CLIENT", "test-client")
	var received map[string]any
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("content type = %q", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"json-access","refresh_token":"json-refresh","expires_in":3600}`))
	}))
	defer providerServer.Close()
	cfg := oauthConfig(providerServer.URL)
	cfg.Providers[0].OAuth.TokenRequestFormat = "json"
	cfg.Providers[0].OAuth.IncludeStateInToken = true
	dataStore, _ := newAuthStore(t, cfg)
	manager := NewManager(dataStore, providerServer.Client())
	defer manager.Close()
	started, err := manager.StartAuthorization(context.Background(), StartRequest{ProviderID: "oauth-provider", CredentialID: "json-account", Mode: "browser"})
	if err != nil {
		t.Fatal(err)
	}
	authorization, _ := url.Parse(started.AuthorizationURL)
	state := authorization.Query().Get("state")
	if _, err = manager.CompleteCallback(context.Background(), state, "json-code#"+state); err != nil {
		t.Fatal(err)
	}
	if received["grant_type"] != "authorization_code" || received["state"] != state || received["code"] != "json-code" {
		t.Fatalf("token JSON = %+v", received)
	}
}

func TestCodexDeviceBootstrapExchangesAuthorizationCode(t *testing.T) {
	var devicePolls atomic.Int32
	idTokenPayload, _ := json.Marshal(map[string]any{"email": "codex@example.com", "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct-device"}})
	idToken := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + base64.RawURLEncoding.EncodeToString(idTokenPayload) + ".signature"
	var providerServer *httptest.Server
	providerServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/device/usercode":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["client_id"] != "codex-client" {
				t.Errorf("device client id = %v", body["client_id"])
			}
			_, _ = w.Write([]byte(`{"device_auth_id":"device-auth-secret","user_code":"CODE-X","interval":1}`))
		case "/device/token":
			if devicePolls.Add(1) == 1 {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"authorization_code":"codex-auth-code","code_verifier":"codex-verifier","code_challenge":"challenge"}`))
		case "/oauth/token":
			_ = r.ParseForm()
			if r.Form.Get("code") != "codex-auth-code" || r.Form.Get("code_verifier") != "codex-verifier" || r.Form.Get("redirect_uri") != providerServer.URL+"/device/callback" {
				t.Errorf("token form = %v", r.Form)
			}
			_, _ = w.Write([]byte(`{"access_token":"codex-device-access","refresh_token":"codex-device-refresh","expires_in":3600,"id_token":"` + idToken + `"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer providerServer.Close()
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "codex", Type: "codex", Enabled: true, OAuth: &config.OAuthConfig{ClientID: "codex-client", TokenURL: providerServer.URL + "/oauth/token", DeviceCodeURL: providerServer.URL + "/device/usercode", DeviceTokenURL: providerServer.URL + "/device/token", DeviceVerificationURL: providerServer.URL + "/verify", DeviceExchangeRedirectURL: providerServer.URL + "/device/callback"}}}}
	dataStore, _ := newAuthStore(t, cfg)
	manager := NewManager(dataStore, providerServer.Client())
	defer manager.Close()
	started, err := manager.StartAuthorization(context.Background(), StartRequest{ProviderID: "codex", CredentialID: "codex-device", Mode: "device"})
	if err != nil {
		t.Fatal(err)
	}
	if started.UserCode != "CODE-X" || started.VerificationURI != providerServer.URL+"/verify" || strings.Contains(mustJSON(started), "device-auth-secret") {
		t.Fatalf("start response = %+v", started)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, statusErr := manager.SessionStatus(started.SessionID)
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		if status.Status == "complete" {
			credentials, _ := dataStore.Credentials(context.Background(), "codex")
			if len(credentials) != 1 || credentials[0].Secret != "codex-device-access" || credentials[0].Email != "codex@example.com" || credentials[0].OAuthToken.Extra["account_id"] != "acct-device" {
				t.Fatalf("credential = %+v", credentials)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("Codex device flow did not complete; polls=%d", devicePolls.Load())
}

func TestRFCDeviceFlowAcceptsPendingSuccessResponse(t *testing.T) {
	var polls atomic.Int32
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/device":
			_, _ = w.Write([]byte(`{"device_code":"rfc-device","user_code":"RFC-CODE","verification_uri":"https://login.example/device","expires_in":60,"interval":1}`))
		case "/token":
			if polls.Add(1) == 1 {
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"rfc-access","refresh_token":"rfc-refresh","expires_in":3600}`))
		}
	}))
	defer providerServer.Close()
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "kimi", Type: "kimi", Enabled: true, OAuth: &config.OAuthConfig{ClientID: "kimi-client", DeviceCodeURL: providerServer.URL + "/device", TokenURL: providerServer.URL + "/token", DeviceFlow: "rfc8628", DeviceRequestFormat: "form"}}}}
	dataStore, _ := newAuthStore(t, cfg)
	manager := NewManager(dataStore, providerServer.Client())
	defer manager.Close()
	started, err := manager.StartAuthorization(context.Background(), StartRequest{ProviderID: "kimi", CredentialID: "kimi-account", Mode: "device"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, statusErr := manager.SessionStatus(started.SessionID)
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		if status.Status == "complete" {
			credentials, _ := dataStore.Credentials(context.Background(), "kimi")
			if len(credentials) != 1 || credentials[0].Secret != "rfc-access" {
				t.Fatalf("credential = %+v", credentials)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("RFC device flow did not complete; polls=%d", polls.Load())
}

func TestXAIDiscoveryDeviceFlowAndRefresh(t *testing.T) {
	var discoveryCalls atomic.Int32
	var tokenCalls atomic.Int32
	payload, _ := json.Marshal(map[string]any{"email": "grok@example.com", "sub": "xai-user"})
	idToken := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
	var providerServer *httptest.Server
	providerServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/discovery":
			discoveryCalls.Add(1)
			_, _ = w.Write([]byte(`{"device_authorization_endpoint":"` + providerServer.URL + `/device","token_endpoint":"` + providerServer.URL + `/token"}`))
		case "/device":
			_ = r.ParseForm()
			if r.Form.Get("client_id") != "xai-client" || !strings.Contains(r.Form.Get("scope"), "grok-cli:access") {
				t.Errorf("device form = %v", r.Form)
			}
			_, _ = w.Write([]byte(`{"device_code":"xai-device","user_code":"XAI-CODE","verification_uri":"https://accounts.x.ai/oauth2/device","verification_uri_complete":"https://accounts.x.ai/oauth2/device?user_code=XAI-CODE","expires_in":60,"interval":1}`))
		case "/token":
			call := tokenCalls.Add(1)
			_ = r.ParseForm()
			if r.Form.Get("grant_type") == "urn:ietf:params:oauth:grant-type:device_code" && call == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
			if r.Form.Get("grant_type") == "refresh_token" {
				_, _ = w.Write([]byte(`{"access_token":"xai-refreshed","refresh_token":"xai-refresh-2","expires_in":3600,"id_token":"` + idToken + `"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"xai-access","refresh_token":"xai-refresh","expires_in":3600,"id_token":"` + idToken + `"}`))
		}
	}))
	defer providerServer.Close()
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "xai", Type: "xai", Enabled: true, OAuth: &config.OAuthConfig{DiscoveryURL: providerServer.URL + "/discovery", ClientID: "xai-client", Scopes: []string{"openid", "grok-cli:access"}}}}}
	dataStore, _ := newAuthStore(t, cfg)
	manager := NewManager(dataStore, providerServer.Client())
	defer manager.Close()
	started, err := manager.StartAuthorization(context.Background(), StartRequest{ProviderID: "xai", CredentialID: "xai-account"})
	if err != nil {
		t.Fatal(err)
	}
	if started.Mode != "device" || started.VerificationURI != "https://accounts.x.ai/oauth2/device?user_code=XAI-CODE" {
		t.Fatalf("start response = %+v", started)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, statusErr := manager.SessionStatus(started.SessionID)
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		if status.Status == "complete" {
			provider, _ := dataStore.Provider(context.Background(), "xai")
			credentials, _ := dataStore.Credentials(context.Background(), "xai")
			if len(credentials) != 1 || credentials[0].Email != "grok@example.com" || credentials[0].OAuthToken.Extra["subject"] != "xai-user" {
				t.Fatalf("credential = %+v", credentials)
			}
			refreshed, refreshErr := manager.EnsureValid(context.Background(), *provider, credentials[0], true)
			if refreshErr != nil || refreshed.Secret != "xai-refreshed" {
				t.Fatalf("refresh credential=%+v err=%v", refreshed, refreshErr)
			}
			if discoveryCalls.Load() != 1 {
				t.Fatalf("discovery calls = %d", discoveryCalls.Load())
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("xAI device flow did not complete")
}

func TestAntigravityBrowserOAuthEnrichesCredentialAndPreservesProjectOnRefresh(t *testing.T) {
	t.Setenv("TPROXY_TEST_ANTIGRAVITY_SECRET", "google-client-secret")
	var tokenCalls atomic.Int32
	var projectCalls atomic.Int32
	var providerServer *httptest.Server
	providerServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			tokenCalls.Add(1)
			_ = r.ParseForm()
			if r.Form.Get("client_id") != "antigravity-client" || r.Form.Get("client_secret") != "google-client-secret" {
				t.Errorf("token form = %v", r.Form)
			}
			if r.Form.Get("grant_type") == "refresh_token" {
				_, _ = w.Write([]byte(`{"access_token":"antigravity-refreshed","expires_in":3600}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"antigravity-access","refresh_token":"antigravity-refresh","expires_in":3600}`))
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer antigravity-access" {
				t.Errorf("userinfo authorization = %q", r.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(`{"email":"antigravity@example.com"}`))
		case "/v1internal:loadCodeAssist":
			projectCalls.Add(1)
			if r.Header.Get("Authorization") != "Bearer antigravity-access" {
				t.Errorf("loadCodeAssist authorization = %q", r.Header.Get("Authorization"))
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			metadata, _ := body["metadata"].(map[string]any)
			if metadata["ideType"] != "ANTIGRAVITY" {
				t.Errorf("loadCodeAssist body = %+v", body)
			}
			_, _ = w.Write([]byte(`{"cloudaicompanionProject":"cloud-project-123"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer providerServer.Close()
	callbackProbe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	callbackAddress := callbackProbe.Addr().String()
	_ = callbackProbe.Close()
	cfg := &config.Config{Providers: []config.ProviderConfig{{
		ID: "antigravity", Type: "antigravity", BaseURL: providerServer.URL, Enabled: true,
		OAuth: &config.OAuthConfig{
			AuthorizationURL:    providerServer.URL + "/authorize",
			TokenURL:            providerServer.URL + "/token",
			UserInfoURL:         providerServer.URL + "/userinfo",
			ClientID:            "antigravity-client",
			ClientSecretEnv:     "TPROXY_TEST_ANTIGRAVITY_SECRET",
			RequireClientSecret: true,
			RedirectURL:         "http://" + callbackAddress + "/oauth-callback",
		},
	}}}
	dataStore, _ := newAuthStore(t, cfg)
	manager := NewManager(dataStore, providerServer.Client())
	defer manager.Close()
	started, err := manager.StartAuthorization(context.Background(), StartRequest{ProviderID: "antigravity", CredentialID: "antigravity-account", Mode: "browser"})
	if err != nil {
		t.Fatal(err)
	}
	authorization, _ := url.Parse(started.AuthorizationURL)
	if authorization.Query().Get("access_type") != "offline" || authorization.Query().Get("prompt") != "consent" || authorization.Query().Get("code_challenge") == "" {
		t.Fatalf("authorization URL = %s", started.AuthorizationURL)
	}
	if _, err = manager.CompleteCallback(context.Background(), authorization.Query().Get("state"), "google-code"); err != nil {
		t.Fatal(err)
	}
	provider, _ := dataStore.Provider(context.Background(), "antigravity")
	credentials, _ := dataStore.Credentials(context.Background(), "antigravity")
	if len(credentials) != 1 || credentials[0].Email != "antigravity@example.com" || credentials[0].OAuthToken.Extra["project_id"] != "cloud-project-123" {
		t.Fatalf("credential = %+v", credentials)
	}
	refreshed, err := manager.EnsureValid(context.Background(), *provider, credentials[0], true)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Secret != "antigravity-refreshed" || refreshed.OAuthToken.Extra["project_id"] != "cloud-project-123" || projectCalls.Load() != 1 || tokenCalls.Load() != 2 {
		t.Fatalf("refreshed=%+v projectCalls=%d tokenCalls=%d", refreshed, projectCalls.Load(), tokenCalls.Load())
	}
}

func oauthConfig(baseURL string) *config.Config {
	return &config.Config{Providers: []config.ProviderConfig{{
		ID: "oauth-provider", Type: "openai-compatible", Name: "OAuth Provider", BaseURL: baseURL, Enabled: true,
		OAuth: &config.OAuthConfig{AuthorizationURL: baseURL + "/authorize", TokenURL: baseURL + "/token", ClientIDEnv: "TPROXY_TEST_OAUTH_CLIENT", Scopes: []string{"profile", "inference"}, RedirectURL: "http://127.0.0.1:8317/api/admin/oauth/callback"},
	}}}
}

func newAuthStore(t *testing.T, cfg *config.Config) (*store.Store, string) {
	t.Helper()
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "auth.db")
	dataStore, err := store.OpenSQLite(path, encryptor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	if err = dataStore.Seed(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	return dataStore, path
}

func mustJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}
