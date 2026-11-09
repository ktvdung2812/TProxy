package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/config"
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

func museMintBody(t *testing.T, w http.ResponseWriter, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
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
		if r.Header.Get("x-api-version") != "1.0.0" {
			t.Errorf("x-api-version = %q", r.Header.Get("x-api-version"))
		}
		body := museMintBody(t, w, r)
		if body["dca_token"] != "dca:device-token" {
			t.Error("dca_token missing from mint body")
		}
		if body["onboard"] != true {
			t.Errorf("login mint should onboard the account, body = %v", body)
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
		body := museMintBody(t, w, r)
		if body["dca_token"] != "dca:stored" {
			t.Error("remint did not use stored dca token")
		}
		if _, onboard := body["onboard"]; onboard {
			t.Errorf("remint should not re-onboard, body = %v", body)
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

func museNoRetryDelay(t *testing.T) {
	t.Helper()
	previous := museMintRetryDelay
	museMintRetryDelay = func(int) time.Duration { return 0 }
	t.Cleanup(func() { museMintRetryDelay = previous })
}

func TestMuseLoginMintRetriesThrottledMint(t *testing.T) {
	museNoRetryDelay(t)
	calls := 0
	server, manager := museTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"api_key": "LLM|after-retry", "is_subs_active": true})
	})
	token, _, err := manager.enrichMuseToken(context.Background(), museTestProvider(server), store.OAuthToken{
		AccessToken: "dca:device-token",
		Extra:       map[string]any{},
	}, "")
	if err != nil {
		t.Fatalf("enrichMuseToken: %v", err)
	}
	if calls != 3 || token.AccessToken != "LLM|after-retry" {
		t.Fatalf("calls = %d, access token = %q", calls, token.AccessToken)
	}
}

func TestMuseRemintDoesNotRetryThrottledMint(t *testing.T) {
	museNoRetryDelay(t)
	calls := 0
	server, manager := museTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := manager.remintMuseKey(context.Background(), museTestProvider(server), store.OAuthToken{
		Extra: map[string]any{museExtraDCAToken: "dca:stored", museExtraDCAExpires: "9999999999"},
	})
	if err == nil || IsPermanent(err) {
		t.Fatalf("throttled remint should fail transiently, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("remint retried in-line: %d calls", calls)
	}
}

func TestMuseMintErrorEnvelopeBecomesDetail(t *testing.T) {
	server, manager := museTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]any{
			"code":       4705002,
			"title":      "Subscription required",
			"detail":     "Subscribe to Muse Code to continue",
			"action_url": "https://muse.ai/subscribe",
		})
	})
	_, _, err := manager.enrichMuseToken(context.Background(), museTestProvider(server), store.OAuthToken{
		AccessToken: "dca:device-token",
		Extra:       map[string]any{},
	}, "")
	if err == nil || !IsPermanent(err) || Code(err) != "authorization_required" {
		t.Fatalf("403 mint should fail permanently, got %v", err)
	}
	want := "Subscription required: Subscribe to Muse Code to continue — https://muse.ai/subscribe"
	if got := Detail(err); got != want {
		t.Fatalf("detail = %q, want %q", got, want)
	}
}

func TestMuseMintModelAPIErrorBecomesDetail(t *testing.T) {
	if got := museErrorDetail([]byte(`{"error":{"message":"Too many requests","type":"rate_limit_error"}}`)); got != "Too many requests" {
		t.Fatalf("detail = %q", got)
	}
	if got := museErrorDetail([]byte(`not json`)); got != "" {
		t.Fatalf("detail = %q", got)
	}
}

func TestMuseSubscriptionRequiredCarriesActionURL(t *testing.T) {
	server, manager := museTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"require_payment":            true,
			"require_payment_action_url": "https://muse.ai/billing",
		})
	})
	_, _, err := manager.enrichMuseToken(context.Background(), museTestProvider(server), store.OAuthToken{
		AccessToken: "dca:device-token",
		Extra:       map[string]any{},
	}, "")
	if err == nil || !IsPermanent(err) {
		t.Fatalf("require_payment should fail permanently, got %v", err)
	}
	if detail := Detail(err); !strings.Contains(detail, "https://muse.ai/billing") {
		t.Fatalf("detail = %q, want the billing link", detail)
	}
}

func TestMuseMintAcceptsOmittedSubscriptionFlag(t *testing.T) {
	server, manager := museTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"api_key": "LLM|key"})
	})
	token, _, err := manager.enrichMuseToken(context.Background(), museTestProvider(server), store.OAuthToken{
		AccessToken: "dca:device-token",
		Extra:       map[string]any{},
	}, "")
	if err != nil {
		t.Fatalf("omitted is_subs_active should not read as inactive: %v", err)
	}
	if token.Extra["is_subs_active"] != true {
		t.Fatalf("is_subs_active = %v", token.Extra["is_subs_active"])
	}
}

// The refresh path runs prepareProviderToken on the reminted token. It must
// not mint a second time with the inference key as the account token.
func TestMusePrepareAfterRemintKeepsMintedKey(t *testing.T) {
	calls := 0
	server, manager := museTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer dca:stored" {
			t.Errorf("mint authorization = %q", r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(map[string]any{"api_key": "LLM|fresh-key", "is_subs_active": true})
	})
	provider := museTestProvider(server)
	reminted, err := manager.remintMuseKey(context.Background(), provider, store.OAuthToken{
		AccessToken: "LLM|old-key",
		Extra:       map[string]any{museExtraDCAToken: "dca:stored", museExtraDCAExpires: "9999999999"},
	})
	if err != nil {
		t.Fatalf("remintMuseKey: %v", err)
	}
	prepared, _, err := manager.prepareProviderToken(context.Background(), provider, reminted, "user@example.com")
	if err != nil {
		t.Fatalf("prepareProviderToken: %v", err)
	}
	if calls != 1 {
		t.Fatalf("mint calls = %d, want 1", calls)
	}
	if prepared.AccessToken != "LLM|fresh-key" || prepared.Extra[museExtraDCAToken] != "dca:stored" {
		t.Fatalf("prepared token = %q, dca = %v", prepared.AccessToken, prepared.Extra[museExtraDCAToken])
	}
}

func TestMuseOAuthRequestsCarryAPIVersion(t *testing.T) {
	var gotVersion string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotVersion = r.Header.Get("x-api-version")
		json.NewEncoder(w).Encode(map[string]any{"device_code": "dc", "user_code": "UC", "verification_uri": "https://auth.meta.com/device"})
	}))
	defer server.Close()
	manager := NewManager(nil, server.Client())
	// A provider stored before the header existed still gets it.
	provider := store.Provider{ID: "meta", Type: "muse", OAuth: &config.OAuthConfig{DeviceCodeURL: server.URL, ClientID: "client"}}
	resolved, err := manager.resolveOAuthConfig(context.Background(), provider)
	if err != nil {
		t.Fatalf("resolveOAuthConfig: %v", err)
	}
	if provider.OAuth.Headers != nil {
		t.Fatalf("stored provider config mutated: %v", provider.OAuth.Headers)
	}
	if _, err := manager.requestDeviceCode(context.Background(), *resolved, ""); err != nil {
		t.Fatalf("requestDeviceCode: %v", err)
	}
	if gotVersion != "1.0.0" {
		t.Fatalf("device code x-api-version = %q", gotVersion)
	}
}

func TestFailSessionErrExposesDetail(t *testing.T) {
	manager := NewManager(nil, http.DefaultClient)
	item := &session{id: "s1", status: "polling", cancel: make(chan struct{})}
	manager.failSessionErr(item, museSubscriptionError("Muse Code subscription required", "https://muse.ai"))
	status := manager.statusFor(item)
	if status.Status != "failed" || status.ErrorCode != "authorization_required" {
		t.Fatalf("status = %+v", status)
	}
	if status.ErrorMessage != "Muse Code subscription required — https://muse.ai" {
		t.Fatalf("error message = %q", status.ErrorMessage)
	}
}
