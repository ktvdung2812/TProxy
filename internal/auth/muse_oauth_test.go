package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/store"
)

func museTestServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *Manager) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/muse-code/key" {
			t.Errorf("unexpected mint path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return server, NewManager(nil, server.Client())
}

func museTestProvider(server *httptest.Server) store.Provider {
	return store.Provider{ID: "p1", Type: "muse", BaseURL: server.URL + "/v1"}
}

func museMintBody(t *testing.T, w http.ResponseWriter, r *http.Request) map[string]string {
	t.Helper()
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Errorf("mint request not JSON: %v", err)
	}
	return body
}

func TestMuseEnrichTokenMintsSubscriptionKey(t *testing.T) {
	server, manager := museTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer dca:device-token" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("User-Agent") != museCodeUserAgent {
			t.Errorf("user-agent = %q", r.Header.Get("User-Agent"))
		}
		if museMintBody(t, w, r)["dca_token"] != "dca:device-token" {
			t.Error("dca_token missing from mint body")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"api_key":         "LLM|minted-inference-key",
			"user_email":      "user@example.com",
			"subs_tier_name":  "Muse Pro",
			"is_subs_active":  true,
			"require_payment": false,
		})
	})
	expires := time.Now().Add(48 * time.Hour)
	token, email, err := manager.enrichMuseToken(context.Background(), museTestProvider(server), store.OAuthToken{
		AccessToken: "dca:device-token",
		ExpiresAt:   expires,
		Extra:       map[string]any{},
	}, "")
	if err != nil {
		t.Fatalf("enrichMuseToken: %v", err)
	}
	if token.AccessToken != "LLM|minted-inference-key" {
		t.Fatalf("access token = %q", token.AccessToken)
	}
	if email != "user@example.com" {
		t.Fatalf("email = %q", email)
	}
	if token.Extra[museExtraDCAToken] != "dca:device-token" {
		t.Fatalf("dca token not stored: %v", token.Extra)
	}
	if token.Extra[museExtraDCAExpires] == "" {
		t.Fatal("dca expiry not stored")
	}
	if token.Extra["subs_tier_name"] != "Muse Pro" || token.Extra["is_subs_active"] != true {
		t.Fatalf("subscription metadata not stored: %v", token.Extra)
	}
}

func TestMuseEnrichTokenRejectsInactiveSubscription(t *testing.T) {
	server, manager := museTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"api_key":        "LLM|payg-key",
			"is_subs_active": false,
		})
	})
	token, _, err := manager.enrichMuseToken(context.Background(), museTestProvider(server), store.OAuthToken{
		AccessToken: "dca:device-token",
		Extra:       map[string]any{},
	}, "")
	if err == nil {
		t.Fatal("inactive subscription minted a pay-as-you-go key without error")
	}
	if !IsPermanent(err) {
		t.Fatalf("inactive subscription error should be permanent, got %v", err)
	}
	// The dca: token stays stored so a later re-mint can succeed once the
	// subscription is active; it must not become the inference secret.
	if token.AccessToken != "dca:device-token" {
		t.Fatalf("access token = %q", token.AccessToken)
	}
}

func TestMuseEnrichTokenRejectsPaymentRequired(t *testing.T) {
	server, manager := museTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"api_key":         "LLM|payg-key",
			"is_subs_active":  true,
			"require_payment": true,
		})
	})
	if _, _, err := manager.enrichMuseToken(context.Background(), museTestProvider(server), store.OAuthToken{
		AccessToken: "dca:device-token",
		Extra:       map[string]any{},
	}, ""); err == nil || !IsPermanent(err) {
		t.Fatalf("require_payment should fail permanently, got %v", err)
	}
}

func TestMuseRemintKeyUsesStoredDCA(t *testing.T) {
	server, manager := museTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if museMintBody(t, w, r)["dca_token"] != "dca:stored" {
			t.Error("remint did not use stored dca token")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"api_key":        "LLM|fresh-key",
			"subs_tier_name": "Muse Pro",
			"is_subs_active": true,
		})
	})
	old := store.OAuthToken{
		AccessToken: "LLM|old-key",
		Extra: map[string]any{
			museExtraDCAToken:   "dca:stored",
			museExtraDCAExpires: "9999999999",
			"email":             "user@example.com",
		},
	}
	token, err := manager.remintMuseKey(context.Background(), museTestProvider(server), old)
	if err != nil {
		t.Fatalf("remintMuseKey: %v", err)
	}
	if token.AccessToken != "LLM|fresh-key" {
		t.Fatalf("access token = %q", token.AccessToken)
	}
	if token.Extra[museExtraDCAToken] != "dca:stored" || token.Extra["email"] != "user@example.com" {
		t.Fatalf("extra not preserved: %v", token.Extra)
	}
}

func TestMuseRemintKeyExpiredDCA(t *testing.T) {
	server, manager := museTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("expired dca still hit the mint endpoint")
	})
	old := store.OAuthToken{
		AccessToken: "LLM|old-key",
		Extra: map[string]any{
			museExtraDCAToken:   "dca:stored",
			museExtraDCAExpires: "1",
		},
	}
	_, err := manager.remintMuseKey(context.Background(), museTestProvider(server), old)
	if err == nil || !IsPermanent(err) {
		t.Fatalf("expired dca should fail permanently, got %v", err)
	}
}

func TestMuseMintURL(t *testing.T) {
	if got := museMintURL(store.Provider{BaseURL: "https://api.meta.ai/v1"}); got != "https://api.meta.ai/muse-code/key" {
		t.Fatalf("mint url = %q", got)
	}
	if got := museMintURL(store.Provider{BaseURL: "https://api.meta.ai/v1/"}); got != "https://api.meta.ai/muse-code/key" {
		t.Fatalf("trailing slash mint url = %q", got)
	}
	if got := museMintURL(store.Provider{BaseURL: ""}); !strings.HasSuffix(got, "/muse-code/key") {
		t.Fatalf("empty base mint url = %q", got)
	}
}
