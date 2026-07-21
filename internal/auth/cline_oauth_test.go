package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/config"
)

func TestClineOAuthExchangeDecodedCode(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"accessToken":  "cline-access",
		"refreshToken": "cline-refresh",
		"email":        "user@example.com",
		"expiresAt":    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
	code := base64.StdEncoding.EncodeToString(payload)
	manager := NewManager(nil, http.DefaultClient)
	token, err := manager.exchangeClineCode(context.Background(), code, "http://127.0.0.1:28120/callback", "")
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "workos:cline-access" || token.RefreshToken != "cline-refresh" {
		t.Fatalf("token = %+v", token)
	}
}

func TestClineOAuthExchangeTokenEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["redirect_uri"] != clineAuthkitDeviceRedirect {
			http.Error(w, `{"error":"invalid redirect"}`, http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"accessToken":"api-access","refreshToken":"api-refresh","expiresAt":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"}}`))
	}))
	defer server.Close()

	manager := NewManager(nil, server.Client())
	token, err := manager.exchangeClineCode(context.Background(), "plain-auth-code", "http://127.0.0.1:28120/callback", server.URL+"/token")
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "workos:api-access" {
		t.Fatalf("token = %+v", token)
	}
}

func TestClineOAuthRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/refresh" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":{"accessToken":"refreshed-access","refreshToken":"refreshed-refresh","expiresAt":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"}}`))
	}))
	defer server.Close()

	manager := NewManager(nil, server.Client())
	token, err := manager.refreshClineToken(context.Background(), "old-refresh", server.URL+"/refresh")
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "workos:refreshed-access" || token.RefreshToken != "refreshed-refresh" {
		t.Fatalf("token = %+v", token)
	}
}

func TestClineBrowserOAuthStart(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "cline", Type: "cline", Enabled: true, OAuth: &config.OAuthConfig{}}}}
	dataStore, _ := newAuthStore(t, cfg)
	manager := NewManager(dataStore, http.DefaultClient)
	defer manager.Close()

	started, err := manager.StartAuthorization(context.Background(), StartRequest{
		ProviderID:   "cline",
		CredentialID: "cline-account",
		Mode:         "browser",
		RedirectURL:  "http://127.0.0.1:28120/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(started.AuthorizationURL, "api.cline.bot/api/v1/auth/authorize") {
		t.Fatalf("authorization url = %q", started.AuthorizationURL)
	}
	if !strings.Contains(started.AuthorizationURL, "client_type=extension") {
		t.Fatalf("authorization url = %q", started.AuthorizationURL)
	}
}

func TestClineOAuthCompleteCallbackSavesCredential(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"accessToken":  "saved-access",
		"refreshToken": "saved-refresh",
		"email":        "cline@example.com",
		"expiresAt":    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
	code := base64.StdEncoding.EncodeToString(payload)

	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "cline", Type: "cline", Enabled: true, OAuth: &config.OAuthConfig{}}}}
	dataStore, _ := newAuthStore(t, cfg)
	manager := NewManager(dataStore, http.DefaultClient)
	defer manager.Close()

	started, err := manager.StartAuthorization(context.Background(), StartRequest{
		ProviderID:   "cline",
		CredentialID: "cline-account",
		Mode:         "browser",
		RedirectURL:  "http://127.0.0.1:28120/callback",
	})
	if err != nil {
		t.Fatal(err)
	}

	status, err := manager.CompleteCallback(context.Background(), mustClineSessionState(t, manager, started.SessionID), code)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "complete" {
		t.Fatalf("status = %+v", status)
	}
	credentials, err := dataStore.Credentials(context.Background(), "cline")
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 1 || credentials[0].Secret != "workos:saved-access" {
		t.Fatalf("credentials = %+v", credentials)
	}
}

func TestClineOAuthStatelessCompleteCallback(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"accessToken":  "stateless-access",
		"refreshToken": "stateless-refresh",
		"email":        "cline@example.com",
		"expiresAt":    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
	code := base64.StdEncoding.EncodeToString(payload)

	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "cline", Type: "cline", Enabled: true, OAuth: &config.OAuthConfig{}}}}
	dataStore, _ := newAuthStore(t, cfg)
	manager := NewManager(dataStore, http.DefaultClient)
	defer manager.Close()

	_, err := manager.StartAuthorization(context.Background(), StartRequest{
		ProviderID:   "cline",
		CredentialID: "cline-account",
		Mode:         "browser",
		RedirectURL:  "http://127.0.0.1:28120/callback",
	})
	if err != nil {
		t.Fatal(err)
	}

	status, err := manager.CompleteCallback(context.Background(), "", code)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "complete" {
		t.Fatalf("status = %+v", status)
	}
}

func mustClineSessionState(t *testing.T, manager *Manager, sessionID string) string {
	t.Helper()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	item := manager.sessions[sessionID]
	if item == nil {
		t.Fatal("session missing")
	}
	item.mu.Lock()
	defer item.mu.Unlock()
	return item.state
}
