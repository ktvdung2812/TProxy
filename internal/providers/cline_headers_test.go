package providers

import "testing"

func TestClineAuthorizationValue(t *testing.T) {
	if got := clineAuthorizationValue("abc"); got != "Bearer workos:abc" {
		t.Fatalf("got %q", got)
	}
	if got := clineAuthorizationValue("workos:abc"); got != "Bearer workos:abc" {
		t.Fatalf("got %q", got)
	}
}
