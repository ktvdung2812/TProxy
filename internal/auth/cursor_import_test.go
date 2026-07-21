package auth

import (
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
