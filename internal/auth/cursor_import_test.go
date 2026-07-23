package auth

import (
	"strings"
	"testing"
)

func TestBuildCursorOAuthTokenStoresMachineID(t *testing.T) {
	token := BuildCursorOAuthToken("access-token", "machine-123")
	if token.AccessToken != "access-token" {
		t.Fatalf("access token = %q", token.AccessToken)
	}
	if token.Extra["machine_id"] != "machine-123" {
		t.Fatalf("machine_id = %#v", token.Extra["machine_id"])
	}
}

func TestNormalizeCursorDBValue(t *testing.T) {
	if got := normalizeCursorDBValue(`"quoted-token"`); got != "quoted-token" {
		t.Fatalf("quoted = %q", got)
	}
	if got := normalizeCursorDBValue("plain-token"); got != "plain-token" {
		t.Fatalf("plain = %q", got)
	}
}

func TestValidateCursorImportToken(t *testing.T) {
	validToken := strings.Repeat("a", 50)
	validMachine := "caea497f-0dfd-44f8-9afb-fb5497f746af"
	if err := ValidateCursorImportToken(validToken, validMachine); err != nil {
		t.Fatalf("expected valid token, got %v", err)
	}
	if err := ValidateCursorImportToken("short", validMachine); err == nil {
		t.Fatal("expected short token error")
	}
	if err := ValidateCursorImportToken(validToken, "bad"); err == nil {
		t.Fatal("expected invalid machine id error")
	}
}
