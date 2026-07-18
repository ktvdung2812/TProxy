package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/store"
)

func TestParseVertexServiceAccount(t *testing.T) {
	pemKey := testPrivateKeyPEM(t)
	raw, err := json.Marshal(vertexServiceAccount{
		Type:        "service_account",
		ClientEmail: "vertex@test.iam.gserviceaccount.com",
		PrivateKey:  pemKey,
		ProjectID:   "demo-project",
	})
	if err != nil {
		t.Fatal(err)
	}
	sa, err := parseVertexServiceAccount(string(raw))
	if err != nil {
		t.Fatalf("parse service account: %v", err)
	}
	if sa.ClientEmail != "vertex@test.iam.gserviceaccount.com" {
		t.Fatalf("unexpected client email %q", sa.ClientEmail)
	}
}

func TestSignVertexJWT(t *testing.T) {
	sa := vertexServiceAccount{
		Type:        "service_account",
		ClientEmail: "vertex@test.iam.gserviceaccount.com",
		PrivateKey:  testPrivateKeyPEM(t),
		ProjectID:   "demo-project",
	}
	assertion, err := signVertexJWT(sa, time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	if len(strings.Split(assertion, ".")) != 3 {
		t.Fatalf("expected jwt with 3 parts, got %q", assertion)
	}
}

func TestVertexAccessTokenCacheUsesFiveMinuteSafetyWindow(t *testing.T) {
	const email = "cache-vertex@test.iam.gserviceaccount.com"
	vertexTokenCache.Delete(email)
	t.Cleanup(func() { vertexTokenCache.Delete(email) })
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"vertex-access","expires_in":3600}`)),
			Header:     make(http.Header),
		}, nil
	})}
	manager := NewManager(nil, client)
	manager.now = func() time.Time { return time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC) }
	credential := store.Credential{ID: "vertex-cache", AuthType: "api_key", Secret: mustVertexServiceAccount(t, email)}
	first, err := manager.ensureVertexServiceAccount(context.Background(), credential, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.ensureVertexServiceAccount(context.Background(), credential, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Secret != "vertex-access" || second.Secret != "vertex-access" || calls.Load() != 1 {
		t.Fatalf("cache results first=%q second=%q calls=%d", first.Secret, second.Secret, calls.Load())
	}
}

func TestVertexMetadataPersistenceErrorsAreReturned(t *testing.T) {
	const email = "metadata-vertex@test.iam.gserviceaccount.com"
	vertexTokenCache.Delete(email)
	t.Cleanup(func() { vertexTokenCache.Delete(email) })
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"vertex-access","expires_in":3600}`)),
			Header:     make(http.Header),
		}, nil
	})}
	dataStore, _ := newAuthStore(t, &config.Config{Providers: []config.ProviderConfig{{ID: "vertex", Type: "vertex", Enabled: true}}})
	_ = dataStore.Close()
	manager := NewManager(dataStore, client)
	credential := store.Credential{ID: "vertex-metadata", AuthType: "service_account", Secret: mustVertexServiceAccount(t, email)}
	if _, err := manager.ensureVertexServiceAccount(context.Background(), credential, false); err == nil {
		t.Fatal("expected metadata persistence error")
	}
}

func mustVertexServiceAccount(t *testing.T, email string) string {
	t.Helper()
	raw, err := json.Marshal(vertexServiceAccount{Type: "service_account", ClientEmail: email, PrivateKey: testPrivateKeyPEM(t), ProjectID: "demo-project"})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func testPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return string(encoded)
}
