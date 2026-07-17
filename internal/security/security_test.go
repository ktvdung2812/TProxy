package security

import (
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
	input := `authorization: Bearer abc123 refresh_token="refresh-secret" api_key=key-secret`
	output := RedactText(input)
	for _, secret := range []string{"abc123", "refresh-secret", "key-secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("secret %q leaked in %q", secret, output)
		}
	}
}
