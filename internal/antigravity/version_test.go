package antigravity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Cloud Code is only ever called by the IDE, so the agent string has to name
// the IDE surface. Announcing the Hub build described a client that does not
// make these calls at all.
func TestUserAgentAnnouncesTheIDESurface(t *testing.T) {
	agent := UserAgent()
	if !strings.HasPrefix(agent, "antigravity/ide/") {
		t.Fatalf("UserAgent() = %q, want the antigravity/ide/ prefix", agent)
	}
	if strings.Contains(agent, "/hub/") {
		t.Fatalf("UserAgent() = %q, still announces the Hub surface", agent)
	}
	if !strings.HasSuffix(agent, " darwin/arm64") {
		t.Fatalf("UserAgent() = %q, want the platform suffix", agent)
	}
	if !strings.Contains(agent, Version()) {
		t.Fatalf("UserAgent() = %q, does not carry version %q", agent, Version())
	}
}

func TestOnboardUserAgentAddsTheNodeClientSuffix(t *testing.T) {
	agent := UserAgent()
	onboard := OnboardUserAgent()
	if !strings.HasPrefix(onboard, agent+" ") {
		t.Fatalf("OnboardUserAgent() = %q, want it to extend %q", onboard, agent)
	}
	if !strings.Contains(onboard, "google-api-nodejs-client/") {
		t.Fatalf("OnboardUserAgent() = %q, want the Node client suffix", onboard)
	}
}

func TestVersionDefaultsToAKnownGoodBuild(t *testing.T) {
	resetVersionState(t)
	if got := Version(); got != FallbackVersion {
		t.Fatalf("Version() = %q, want the fallback %q before any refresh", got, FallbackVersion)
	}
}

// A malformed override would be forwarded verbatim inside a User-Agent.
func TestValidVersion(t *testing.T) {
	for _, good := range []string{"2.1.1", "2.1", "10.0.0", "1.23.2.4"} {
		if !validVersion(good) {
			t.Errorf("validVersion(%q) = false, want true", good)
		}
	}
	for _, bad := range []string{"", "2", "2.1.1-beta", "v2.1.1", "2..1", "2.1.x", "2.1.1.2.3"} {
		if validVersion(bad) {
			t.Errorf("validVersion(%q) = true, want false", bad)
		}
	}
}

func resetVersionState(t *testing.T) {
	t.Helper()
	mu.Lock()
	version, pinned = FallbackVersion, false
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		version, pinned = FallbackVersion, false
		mu.Unlock()
	})
}

// The fallback must be a build Cloud Code accepts, and must not drift releases
// behind the shipping one the way the previous hard-coded constant did.
func TestFallbackVersionIsCurrent(t *testing.T) {
	if !validVersion(FallbackVersion) {
		t.Fatalf("FallbackVersion %q is not a usable version string", FallbackVersion)
	}
}

func TestRefreshAdoptsTheManifestBuild(t *testing.T) {
	resetVersionState(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "electron-builder" {
			t.Errorf("manifest User-Agent = %q, want electron-builder", got)
		}
		_, _ = w.Write([]byte("version: 2.9.4\nfiles:\n  - url: hub.zip\n"))
	}))
	defer server.Close()
	original := manifestURL
	manifestURL = server.URL
	defer func() { manifestURL = original }()

	refresh(context.Background())
	if got := Version(); got != "2.9.4" {
		t.Fatalf("Version() = %q, want the manifest build", got)
	}
	if !strings.Contains(UserAgent(), "2.9.4") {
		t.Fatalf("UserAgent() = %q, does not carry the refreshed build", UserAgent())
	}
}

// A malformed build would be forwarded verbatim inside a User-Agent.
func TestRefreshRejectsAMalformedManifestBuild(t *testing.T) {
	resetVersionState(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("version: 2.8.1-beta+exp\n"))
	}))
	defer server.Close()
	original := manifestURL
	manifestURL = server.URL
	defer func() { manifestURL = original }()

	refresh(context.Background())
	if got := Version(); got != FallbackVersion {
		t.Fatalf("Version() = %q, want the fallback after a malformed manifest", got)
	}
}

// A manifest outage must not change what tproxy sends.
func TestRefreshKeepsCurrentBuildWhenManifestFails(t *testing.T) {
	resetVersionState(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	original := manifestURL
	manifestURL = server.URL
	defer func() { manifestURL = original }()

	refresh(context.Background())
	if got := Version(); got != FallbackVersion {
		t.Fatalf("Version() = %q, want the fallback retained", got)
	}
}

// An operator who pinned a build meant it; tracking must not overwrite it.
func TestPinnedBuildIsNotOverwrittenByTheManifest(t *testing.T) {
	resetVersionState(t)
	mu.Lock()
	version, pinned = "2.3.0", true
	mu.Unlock()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("version: 2.9.4\n"))
	}))
	defer server.Close()
	original := manifestURL
	manifestURL = server.URL
	defer func() { manifestURL = original }()

	refresh(context.Background())
	if got := Version(); got != "2.3.0" {
		t.Fatalf("Version() = %q, want the pinned build preserved", got)
	}
}
