package providers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/store"
)

func TestClineAuthorizationValueOAuthPrefixesWorkOS(t *testing.T) {
	if got := clineAuthorizationValue("abc", "oauth"); got != "Bearer workos:abc" {
		t.Fatalf("oauth bare = %q", got)
	}
	if got := clineAuthorizationValue("workos:abc", "oauth"); got != "Bearer workos:abc" {
		t.Fatalf("oauth prefixed = %q", got)
	}
	if got := clineAuthorizationValue("abc", ""); got != "Bearer workos:abc" {
		t.Fatalf("empty auth type defaults to oauth prefix = %q", got)
	}
}

func TestClineAuthorizationValueAPIKeyNoWorkOSPrefix(t *testing.T) {
	if got := clineAuthorizationValue("sk-cline-key", "api_key"); got != "Bearer sk-cline-key" {
		t.Fatalf("api_key = %q", got)
	}
	if got := clineAuthorizationValue("sk-cline-key", "apikey"); got != "Bearer sk-cline-key" {
		t.Fatalf("apikey = %q", got)
	}
}

func TestApplyClineHeadersSetsPlatformMetadata(t *testing.T) {
	headers := make(http.Header)
	applyClineHeaders(headers)
	for _, key := range []string{"HTTP-Referer", "X-Title", "User-Agent", "X-PLATFORM", "X-CLIENT-TYPE", "X-CLIENT-VERSION"} {
		if headers.Get(key) == "" {
			t.Fatalf("missing header %s", key)
		}
	}
	if headers.Get("X-CLIENT-TYPE") != "tproxy" {
		t.Fatalf("client type = %q", headers.Get("X-CLIENT-TYPE"))
	}
	if !strings.Contains(headers.Get("User-Agent"), "tproxy/") {
		t.Fatalf("user-agent = %q", headers.Get("User-Agent"))
	}
}

func TestApplyClineAuthHeadersAPIKey(t *testing.T) {
	headers := make(http.Header)
	applyClineAuthHeaders(headers, store.Credential{Secret: "sk-key", AuthType: "api_key"})
	if headers.Get("Authorization") != "Bearer sk-key" {
		t.Fatalf("auth = %q", headers.Get("Authorization"))
	}
}

func TestClinepassStaticModels(t *testing.T) {
	items := clinepassStaticModelEntries(store.Provider{Type: "clinepass"})
	if len(items) < 5 {
		t.Fatalf("expected static clinepass models, got %d", len(items))
	}
	for _, item := range items {
		if !strings.HasPrefix(item.ID, "cline-pass/") {
			t.Fatalf("unexpected id %q", item.ID)
		}
	}
}

func TestClineStaticModels(t *testing.T) {
	items := clineStaticModelEntries(store.Provider{Type: "cline"})
	if len(items) < 5 {
		t.Fatalf("expected static cline models, got %d", len(items))
	}
}
