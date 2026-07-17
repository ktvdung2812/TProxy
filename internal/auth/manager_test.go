package auth

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
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
