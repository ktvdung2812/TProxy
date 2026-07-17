package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"
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

func testPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return string(encoded)
}
