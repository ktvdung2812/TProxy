package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

// The upstream text reads like a gateway error, so the surfaced message has to
// say where it came from and what clears it.
func TestAntigravityAccountVerificationErrorExplainsItself(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"Verify your account to continue."}}`))
	}))
	defer upstream.Close()

	adapter := &antigravityAdapter{client: upstream.Client()}
	credential := store.Credential{AuthType: "oauth", OAuthToken: &store.OAuthToken{Extra: map[string]any{"project_id": "p"}}}
	_, err := adapter.Execute(context.Background(), store.Provider{Type: "antigravity", BaseURL: upstream.URL}, credential,
		canonical.Request{RequestID: "r", UpstreamModel: "gemini-3-pro"})
	if err == nil {
		t.Fatal("expected an error")
	}
	message := err.Error()
	if !strings.Contains(message, "Verify your account to continue.") {
		t.Errorf("upstream text was lost: %q", message)
	}
	if !strings.Contains(message, "comes from Google, not tproxy") {
		t.Errorf("message does not attribute the rejection: %q", message)
	}
	if Status(err) != http.StatusForbidden {
		t.Errorf("status = %d, want 403", Status(err))
	}
}

func TestAntigravityStreamErrorCarriesTheSameHint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"Verify your account to continue."}}`))
	}))
	defer upstream.Close()

	adapter := &antigravityAdapter{client: upstream.Client()}
	credential := store.Credential{AuthType: "oauth", OAuthToken: &store.OAuthToken{Extra: map[string]any{"project_id": "p"}}}
	_, err := adapter.ExecuteStream(context.Background(), store.Provider{Type: "antigravity", BaseURL: upstream.URL}, credential,
		canonical.Request{RequestID: "r", UpstreamModel: "gemini-3-pro", Stream: true})
	if err == nil || !strings.Contains(err.Error(), "comes from Google, not tproxy") {
		t.Fatalf("streaming error missing the hint: %v", err)
	}
}

// Unrelated failures must not be relabelled.
func TestAntigravityUnrelatedErrorsAreLeftAlone(t *testing.T) {
	if hint := antigravityAccountHint(http.StatusForbidden, "Permission denied on resource project foo"); hint != "" {
		t.Errorf("unrelated 403 was given the verification hint: %q", hint)
	}
	if hint := antigravityAccountHint(http.StatusTooManyRequests, "Verify your account to continue."); hint != "" {
		t.Errorf("non-403 status was given the hint: %q", hint)
	}
}

// The quota panel shows per-model allocation, so an operator seeing every model
// at 100% needs to be told about the case the panel cannot show.
func TestAntigravityResourceExhaustedExplainsTheThrottle(t *testing.T) {
	hint := antigravityAccountHint(http.StatusTooManyRequests, "Resource has been exhausted (e.g. check quota).")
	if !strings.Contains(hint, "upstream throttle") {
		t.Fatalf("hint does not attribute the throttle upstream: %q", hint)
	}
	// The quota panel reading full while requests fail is the specific thing
	// that makes this look like a gateway fault.
	if !strings.Contains(hint, "different budget") {
		t.Fatalf("hint does not explain the misleading quota reading: %q", hint)
	}
}

// A response that already names the cause needs no extra text.
func TestAntigravityExplicitQuotaExhaustionIsNotAnnotated(t *testing.T) {
	if hint := antigravityAccountHint(http.StatusTooManyRequests, "Quota exhausted for this model"); hint != "" {
		t.Fatalf("explicit quota exhaustion was annotated: %q", hint)
	}
}

func TestAntigravityResourceExhaustedHintIsScopedTo429(t *testing.T) {
	if hint := antigravityAccountHint(http.StatusInternalServerError, "Resource has been exhausted (e.g. check quota)."); hint != "" {
		t.Fatalf("non-429 status was annotated: %q", hint)
	}
}
