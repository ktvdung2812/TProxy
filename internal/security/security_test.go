package security

import (
	"crypto/rand"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEncryptRoundTripAndAPIKeyHash(t *testing.T) {
	key, err := GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := encryptor.Encrypt("provider-secret")
	if err != nil {
		t.Fatal(err)
	}
	if ciphertext == "provider-secret" {
		t.Fatal("secret was not encrypted")
	}
	plaintext, err := encryptor.Decrypt(ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if plaintext != "provider-secret" {
		t.Fatalf("plaintext = %q", plaintext)
	}
	if HashAPIKey("one") == HashAPIKey("two") {
		t.Fatal("api key hashes collided")
	}
}

func TestRedactTextRemovesCredentialMaterial(t *testing.T) {
	input := `authorization: Bearer abc123 refresh_token="refresh-secret" api_key=key-secret token=generic-secret`
	output := RedactText(input)
	for _, secret := range []string{"abc123", "refresh-secret", "key-secret", "generic-secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("secret %q leaked in %q", secret, output)
		}
	}
}

func TestRedactHeaderHandlesAPIKeyVariants(t *testing.T) {
	for _, name := range []string{"X-API-Key", "X_API_KEY", "api_key", "Authorization", "Cookie", "X-Access-Token"} {
		if got := RedactHeader(name, "secret-value"); got != "[redacted]" {
			t.Fatalf("header %q was not redacted: %q", name, got)
		}
	}
}

func TestRedactTextRemovesProxyUserinfo(t *testing.T) {
	input := "proxy failed at http://proxy-user:proxy-password@proxy.example:8080"
	output := RedactText(input)
	if strings.Contains(output, "proxy-password") || strings.Contains(output, "proxy-user") {
		t.Fatalf("proxy credentials leaked: %q", output)
	}
}

func TestNewIDFailsClosedWhenEntropyUnavailable(t *testing.T) {
	original := rand.Reader
	rand.Reader = strings.NewReader("entropy-unavailable")
	defer func() { rand.Reader = original }()
	// strings.Reader supplies deterministic bytes, so force the read to fail by
	// replacing it with an empty reader after the first generated ID check.
	rand.Reader = strings.NewReader("")
	defer func() {
		if recover() == nil {
			t.Error("NewID returned a predictable ID instead of failing closed")
		}
	}()
	_ = NewID("state_")
}

func TestIsLoopbackRejectsForwardedRequests(t *testing.T) {
	request := httptest.NewRequest("GET", "http://127.0.0.1/", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	if !IsLoopback(request) {
		t.Fatal("direct loopback request was not recognized")
	}
	request.Header.Set("X-Forwarded-For", "203.0.113.4")
	if IsLoopback(request) {
		t.Fatal("forwarded request was treated as trusted loopback")
	}
}
