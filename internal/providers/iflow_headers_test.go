package providers

import "testing"

func TestIflowSignature(t *testing.T) {
	sig := iflowSignature("iFlow-Cli", "session-1", 1700000000000, "secret-key")
	if sig == "" {
		t.Fatal("expected non-empty signature")
	}
	if sig == iflowSignature("iFlow-Cli", "session-1", 1700000000001, "secret-key") {
		t.Fatal("signature should change with timestamp")
	}
}
