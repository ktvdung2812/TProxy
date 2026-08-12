package tunnel

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestCloudflareQuickTunnelURL(t *testing.T) {
	tests := map[string]string{
		"canonical quick tunnel": "https://example.trycloudflare.com",
		"normalizes casing":      "https://EXAMPLE.TRYCLOUDFLARE.COM/",
		"rejects relay host":     "https://rabc123.abc-tunnel.us",
		"rejects a path":         "https://example.trycloudflare.com/v1",
		"rejects HTTP":           "http://example.trycloudflare.com",
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			got := CloudflareQuickTunnelURL(raw)
			if name == "canonical quick tunnel" || name == "normalizes casing" {
				if got != "https://example.trycloudflare.com" {
					t.Fatalf("CloudflareQuickTunnelURL(%q) = %q", raw, got)
				}
				return
			}
			if got != "" {
				t.Fatalf("CloudflareQuickTunnelURL(%q) = %q, want empty", raw, got)
			}
		})
	}
}

func TestCloudflaredDownloadIsPinnedAndChecksummedByDefault(t *testing.T) {
	t.Setenv(cloudflaredVersionEnv, "")
	t.Setenv(cloudflaredURLEnv, "")
	t.Setenv(cloudflaredSHA256Env, "")
	url, _, err := cloudflaredDownloadURL()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(url, "/latest/") || !strings.Contains(url, "/"+cloudflaredPinnedVersion+"/") {
		t.Fatalf("cloudflared URL = %q, want pinned release", url)
	}
	if _, err := cloudflaredExpectedSHA256(); err != nil {
		t.Fatalf("default pinned checksum unavailable: %v", err)
	}
}

func TestCloudflaredCustomArtifactRequiresChecksum(t *testing.T) {
	t.Setenv(cloudflaredVersionEnv, "2026.7.2")
	t.Setenv(cloudflaredURLEnv, "")
	t.Setenv(cloudflaredSHA256Env, "")
	if _, err := cloudflaredExpectedSHA256(); err == nil {
		t.Fatal("custom cloudflared version must require an explicit checksum")
	}
	t.Setenv(cloudflaredVersionEnv, "latest")
	if _, _, err := cloudflaredDownloadURL(); err == nil {
		t.Fatal("mutable latest cloudflared release was accepted")
	}
}

func TestCloudflaredDownloadRequiresImmutablePinAndChecksum(t *testing.T) {
	t.Setenv(cloudflaredVersionEnv, "")
	t.Setenv(cloudflaredURLEnv, "")
	t.Setenv(cloudflaredSHA256Env, "")
	if url, _, err := cloudflaredDownloadURL(); err != nil || !strings.Contains(url, "/releases/download/"+cloudflaredPinnedVersion+"/") {
		t.Fatalf("default pinned download URL = %q, %v", url, err)
	}
	if _, err := cloudflaredExpectedSHA256(); err != nil {
		t.Fatalf("default pinned checksum rejected: %v", err)
	}

	t.Setenv(cloudflaredVersionEnv, "2026.1.2")
	url, _, err := cloudflaredDownloadURL()
	if err != nil || !strings.Contains(url, "/releases/download/2026.1.2/") {
		t.Fatalf("versioned download URL = %q, %v", url, err)
	}
	t.Setenv(cloudflaredSHA256Env, strings.Repeat("a", 64))
	if _, err := cloudflaredExpectedSHA256(); err != nil {
		t.Fatalf("valid checksum rejected: %v", err)
	}

	t.Setenv(cloudflaredURLEnv, "http://example.com/cloudflared")
	if _, _, err := cloudflaredDownloadURL(); err == nil {
		t.Fatal("non-HTTPS download URL was accepted")
	}
}

func TestQuickTunnelReadinessRequiresURLAndConnection(t *testing.T) {
	state := &quickTunnelReadiness{}
	if url, registered := state.snapshot(); url != "" || registered {
		t.Fatalf("initial readiness = (%q, %v)", url, registered)
	}
	state.markRegistered()
	if url, registered := state.snapshot(); url != "" || !registered {
		t.Fatalf("registered-only readiness = (%q, %v)", url, registered)
	}
	if !state.setURL("https://example.trycloudflare.com") {
		t.Fatal("first URL update was not accepted")
	}
	if url, registered := state.snapshot(); url != "https://example.trycloudflare.com" || !registered {
		t.Fatalf("ready state = (%q, %v)", url, registered)
	}
}

func TestGenerateShortID(t *testing.T) {
	id := GenerateShortID()
	if len(id) != 6 {
		t.Fatalf("short id length = %d", len(id))
	}
}

func TestSaveAndLoadState(t *testing.T) {
	dir := t.TempDir()
	layout := NewDataLayout(dir)
	state := State{ShortID: "abc123", TunnelURL: "https://foo.trycloudflare.com"}
	if err := SaveState(layout.StateFile, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadState(layout.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ShortID != state.ShortID || loaded.TunnelURL != state.TunnelURL {
		t.Fatalf("loaded = %+v", loaded)
	}
}

func TestCloudflaredUnexpectedExitHandler(t *testing.T) {
	c := NewCloudflared(NewDataLayout(t.TempDir()))
	cmd := &exec.Cmd{}
	c.process = cmd
	fired := false
	c.onUnexpectedExit = func() { fired = true }

	handler := c.takeUnexpectedExitHandler(cmd)
	if handler == nil {
		t.Fatal("expected unexpected-exit handler")
	}
	handler()
	if !fired {
		t.Fatal("unexpected-exit handler did not run")
	}
	if c.process != nil {
		t.Fatal("exited process was not detached")
	}
}

func TestCloudflaredReplacedOrStoppedProcessDoesNotRestart(t *testing.T) {
	c := NewCloudflared(NewDataLayout(t.TempDir()))
	oldCmd := &exec.Cmd{}
	newCmd := &exec.Cmd{}
	c.process = newCmd
	c.onUnexpectedExit = func() { t.Fatal("replacement must not restart") }
	if handler := c.takeUnexpectedExitHandler(oldCmd); handler != nil {
		t.Fatal("replaced process returned a restart handler")
	}

	c.process = oldCmd
	c.intentionalKill = true
	if handler := c.takeUnexpectedExitHandler(oldCmd); handler != nil {
		t.Fatal("intentionally stopped process returned a restart handler")
	}
}

func TestCloudflaredConnectionStateTracksRegisteredConnections(t *testing.T) {
	c := NewCloudflared(NewDataLayout(t.TempDir()))
	c.process = &exec.Cmd{}
	c.resetConnectionsLocked()

	c.observeTunnelConnectionLog("INF Registered tunnel connection connIndex=0 protocol=http2")
	c.observeTunnelConnectionLog("INF Registered tunnel connection connIndex=1 protocol=http2")
	if !c.IsConnected() {
		t.Fatal("expected registered cloudflared connections to be active")
	}

	c.observeTunnelConnectionLog("INF Unregistered tunnel connection connIndex=0")
	if !c.IsConnected() {
		t.Fatal("one remaining HA connection should keep tunnel connected")
	}

	c.observeTunnelConnectionLog("INF Unregistered tunnel connection connIndex=1")
	if c.IsConnected() {
		t.Fatal("expected disconnected state after all HA connections unregister")
	}
}

// --- regression tests for tunnel resilience -------------------------------

// The Tailscale watchdog must be able to tell "tailscaled is logged in" from
// "the funnel is actually serving our port". Conflating them makes the watchdog
// skip every repair while the funnel is down.
func TestTailscaleStatusDistinguishesLoginFromFunnel(t *testing.T) {
	ts := &Tailscale{
		binary: "tailscale",
		run: func(_ string, args ...string) ([]byte, error) {
			switch {
			case len(args) >= 2 && args[0] == "status":
				return []byte(`{"BackendState":"Running","Self":{"DNSName":"box.tail1234.ts.net."}}`), nil
			case len(args) >= 1 && args[0] == "serve":
				// Logged in, but no funnel configured.
				return []byte(`{"AllowFunnel":{}}`), nil
			}
			return nil, nil
		},
	}
	probe := ts.Status()
	if !probe.LoggedIn {
		t.Fatal("expected LoggedIn=true")
	}
	if probe.Running {
		t.Error("Running must be false when no funnel is configured")
	}
}

func TestTailscaleStatusReportsRunningWhenFunnelActive(t *testing.T) {
	ts := &Tailscale{
		binary: "tailscale",
		run: func(_ string, args ...string) ([]byte, error) {
			switch {
			case len(args) >= 2 && args[0] == "status":
				return []byte(`{"BackendState":"Running","Self":{"DNSName":"box.tail1234.ts.net."}}`), nil
			case len(args) >= 1 && args[0] == "serve":
				return []byte(`{"AllowFunnel":{"box.tail1234.ts.net:443":true}}`), nil
			}
			return nil, nil
		},
	}
	probe := ts.Status()
	if !probe.Running {
		t.Error("Running must be true while a funnel is allowed")
	}
	if probe.URL != "https://box.tail1234.ts.net" {
		t.Errorf("URL = %q", probe.URL)
	}
}

// When `serve status` cannot be read we must not claim the funnel is down —
// that would make the watchdog thrash. Fall back to the login state.
func TestTailscaleStatusFallsBackWhenServeStatusUnavailable(t *testing.T) {
	ts := &Tailscale{
		binary: "tailscale",
		run: func(_ string, args ...string) ([]byte, error) {
			if len(args) >= 1 && args[0] == "serve" {
				return nil, errors.New("unknown command")
			}
			return []byte(`{"BackendState":"Running","Self":{"DNSName":"box.tail1234.ts.net."}}`), nil
		},
	}
	if !ts.Status().Running {
		t.Error("expected fallback to LoggedIn when serve status is unavailable")
	}
}

// Auto-recovery must not hinge on one hard-coded IP being reachable.
func TestCheckInternetUsesMultipleTargets(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	if reachableAny([]string{"192.0.2.1:443", listener.Addr().String()}, 2*time.Second) != true {
		t.Error("expected success when any target is reachable")
	}
	if reachableAny([]string{"192.0.2.1:443", "192.0.2.2:443"}, 300*time.Millisecond) != false {
		t.Error("expected failure when every target is unreachable")
	}
}

// Killing by PID file must verify the PID still belongs to cloudflared: PIDs are
// recycled, so a stale file can otherwise point at an unrelated user process.
func TestProcessIsCloudflaredRejectsForeignPID(t *testing.T) {
	if processIsCloudflared(os.Getpid()) {
		t.Error("the test process must not be mistaken for cloudflared")
	}
	if processIsCloudflared(0) || processIsCloudflared(-1) {
		t.Error("invalid pids must be rejected")
	}
}

// The port-based sweep must not match unrelated cloudflared instances.
func TestCloudflaredPortPatternIsAnchoredToLoopback(t *testing.T) {
	pattern := cloudflaredPortPattern(28120)
	if !strings.Contains(pattern, "--url") {
		t.Errorf("pattern %q should anchor on the --url flag we spawn with", pattern)
	}
	re := regexp.MustCompile(pattern)
	if re.MatchString("cloudflared tunnel --url http://127.0.0.1:9999") {
		t.Error("pattern must not match a different port")
	}
	if !re.MatchString("cloudflared tunnel --url http://127.0.0.1:28120 --config /tmp/x") {
		t.Error("pattern must match our own spawn command line")
	}
	if re.MatchString("cloudflared tunnel run --token abc mytunnel 28120") {
		t.Error("pattern must not match an unrelated named tunnel")
	}
}

// A connector inherited from a previous tproxy run must be adopted, not
// recreated: recreating hands out a new quick-tunnel URL and silently breaks
// every client already pointed at the old one.
func TestStoredTunnelReachableProbesRecordedURL(t *testing.T) {
	var served string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dir := t.TempDir()
	svc := &Service{layout: NewDataLayout(dir)}

	// A non-Cloudflare URL must never be treated as our quick tunnel.
	if svc.storedTunnelReachable(context.Background(), SettingsSnapshot{TunnelURL: server.URL}) {
		t.Error("only trycloudflare.com URLs may be adopted")
	}
	if served != "" {
		t.Errorf("unexpected probe of %q", served)
	}

	// An unreachable but well-formed quick tunnel must report false.
	if svc.storedTunnelReachable(context.Background(), SettingsSnapshot{TunnelURL: "https://gone.trycloudflare.com"}) {
		t.Error("an unreachable tunnel must not be adopted")
	}
}

func TestOwnsProcessIsFalseForInheritedConnector(t *testing.T) {
	c := NewCloudflared(NewDataLayout(t.TempDir()))
	if c.OwnsProcess() {
		t.Error("a connector we never spawned must not be reported as ours")
	}
}
