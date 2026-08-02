package tunnel

import (
	"os/exec"
	"testing"
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
