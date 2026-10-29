// Package antigravity holds the client identity tproxy presents to Google's
// Cloud Code (Antigravity) endpoints.
//
// Cloud Code sees this build number in the User-Agent, in X-Client-Version and
// in the onboarding metadata. Requests are not rejected on it — 2.1.1, 2.2.1,
// 2.8.1 and 2.25.0 were all accepted under both the "ide" and "hub" component
// names — but the model catalogue is shaped by it. Measured against live
// accounts on 2026-10-23:
//
//   - announcing 1.20.0 instead of 2.25.0 drops the Gemini 3.8 Flash
//     High/Medium/Low models from fetchAvailableModels;
//   - Gemini 4 Argon is listed only for the "hub" component, never for "ide",
//     whatever the account's plan.
//
// Stale or mismatched identity therefore hides new models rather than failing
// loudly, which is the argument for tracking the real build rather than pinning
// one and forgetting it — the version was previously hard-coded in five places
// across three packages, and had drifted six releases behind.
//
// The build is published by Antigravity's own auto-updater manifest, which
// reports the same number as the Antigravity App entries of the public
// changelog at antigravity.google/docs/changelog (verified: manifest 2.25.0 ==
// App v2.25.0). TPROXY_ANTIGRAVITY_IDE_VERSION pins a specific build and
// disables tracking.
package antigravity

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// FallbackVersion is used until the manifest has been read and whenever it
	// cannot be reached. Keep it at a build Cloud Code is known to accept.
	FallbackVersion = "2.25.0"
	idePlatform     = "darwin/arm64"

	// nodeAPIClient is appended for control-plane (onboardUser) calls only. The
	// client runs those through the Node library and Cloud Code expects the
	// longer agent string there.
	nodeAPIClient = "google-api-nodejs-client/10.3.0"

	// VersionEnv pins the announced build and suppresses manifest tracking.
	VersionEnv = "TPROXY_ANTIGRAVITY_IDE_VERSION"

	refreshInterval = 3 * time.Hour
	fetchTimeout    = 10 * time.Second
)

// manifestURL is a variable so tests can point the fetch at a local server.
var manifestURL = "https://antigravity-hub-auto-updater-974169037036.us-central1.run.app/manifest/latest-arm64-mac.yml"

var (
	mu         sync.RWMutex
	version    = FallbackVersion
	pinned     bool
	pinnedRead sync.Once

	startOnce sync.Once
)

// Version returns the Antigravity build to present upstream.
func Version() string {
	pinnedRead.Do(func() {
		if override := strings.TrimSpace(os.Getenv(VersionEnv)); override != "" && validVersion(override) {
			mu.Lock()
			version, pinned = override, true
			mu.Unlock()
		}
	})
	mu.RLock()
	defer mu.RUnlock()
	return version
}

// UserAgent is the agent string used for generate, stream, model list and quota
// requests. Since Antigravity 2.0 the agent runs in the App (the "hub"), which
// is the client those requests come from and the one whose build the manifest
// reports; the separate IDE ships on its own, older version line. Announcing
// "ide" with the App's build described a client that does not exist and hid
// models offered only to the App, such as Gemini 4 Argon.
func UserAgent() string {
	return fmt.Sprintf("antigravity/hub/%s %s", Version(), idePlatform)
}

// OnboardUserAgent is the longer control-plane agent string used by onboardUser.
func OnboardUserAgent() string {
	return UserAgent() + " " + nodeAPIClient
}

// StartVersionRefresh keeps the announced build current. It returns
// immediately; the first fetch happens on the returned goroutine so start-up is
// never blocked on a third-party endpoint. Repeat calls are a no-op, and an
// operator-pinned version disables it entirely.
func StartVersionRefresh(ctx context.Context) {
	if Version(); isPinned() {
		return
	}
	startOnce.Do(func() { go refreshLoop(ctx) })
}

func isPinned() bool {
	mu.RLock()
	defer mu.RUnlock()
	return pinned
}

func refreshLoop(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	refresh(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh(ctx)
		}
	}
}

func refresh(ctx context.Context) {
	fetched, err := fetchLatestVersion(ctx, &http.Client{Timeout: fetchTimeout})
	if err != nil {
		// A manifest outage must not change what tproxy sends; the cached or
		// fallback build stays in force.
		log.Printf("antigravity version refresh failed, keeping %s: %v", Version(), err)
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if pinned || version == fetched {
		return
	}
	log.Printf("antigravity client version updated to %s", fetched)
	version = fetched
}

func fetchLatestVersion(ctx context.Context, client *http.Client) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return "", err
	}
	// The manifest is served to electron-builder's updater; identify as such.
	request.Header.Set("User-Agent", "electron-builder")
	request.Header.Set("Cache-Control", "no-cache")

	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("antigravity manifest returned status %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<10))
	if err != nil {
		return "", err
	}
	var manifest struct {
		Version string `yaml:"version"`
	}
	if err = yaml.Unmarshal(raw, &manifest); err != nil {
		return "", err
	}
	parsed := strings.TrimSpace(manifest.Version)
	if parsed == "" {
		return "", errors.New("antigravity manifest returned an empty version")
	}
	if !validVersion(parsed) {
		return "", fmt.Errorf("antigravity manifest returned invalid version %q", parsed)
	}
	return parsed, nil
}

// validVersion accepts a plain dotted numeric version. Anything else would end
// up verbatim in a User-Agent, so it is rejected rather than forwarded.
func validVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) < 2 || len(parts) > 4 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, char := range part {
			if char < '0' || char > '9' {
				return false
			}
		}
	}
	return true
}
