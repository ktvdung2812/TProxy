package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type memoryTunnelSettings struct{ SettingsSnapshot }

func (m *memoryTunnelSettings) LoadSettings(context.Context) (SettingsSnapshot, error) {
	return m.SettingsSnapshot, nil
}
func (m *memoryTunnelSettings) SaveCloudflare(_ context.Context, enabled bool, token, hostname string) error {
	m.Enabled, m.TunnelToken, m.TunnelHostname = enabled, token, hostname
	m.TunnelURL = CloudflareTunnelURL(hostname)
	return nil
}
func (m *memoryTunnelSettings) SaveTailscale(context.Context, bool, string) error { return nil }
func (m *memoryTunnelSettings) OnPublicURL(context.Context, string) error         { return nil }

func TestCloudflareConfigurationAndDisable(t *testing.T) {
	settings := &memoryTunnelSettings{SettingsSnapshot: SettingsSnapshot{
		Enabled: true, TunnelToken: "saved-private-token", TunnelHostname: "api.example.com",
	}}
	svc := NewService(NewDataLayout(t.TempDir()), 0, settings)
	for _, input := range []struct{ token, hostname string }{
		{"token", "https://api.example.com/v1"},
		{"cloudflared tunnel run --token secret", "api.example.com"},
		{"token\nsecret", "api.example.com"},
	} {
		if _, err := svc.Enable(context.Background(), 0, input.token, input.hostname); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("expected invalid configuration, got %v", err)
		}
	}
	if !settings.Enabled || settings.TunnelToken != "saved-private-token" {
		t.Fatal("invalid input must not replace the previous configuration")
	}
	if err := svc.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if settings.Enabled || settings.TunnelToken != "saved-private-token" || settings.TunnelHostname != "api.example.com" {
		t.Fatal("disable must retain named tunnel configuration")
	}
	if _, err := svc.enable(context.Background(), 0, "", "", true); err == nil {
		t.Fatal("queued recovery must not undo a manual disable")
	}
	settings.TunnelToken = ""
	if _, err := svc.Enable(context.Background(), 0, "", "api.example.com"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("missing token must be rejected, got %v", err)
	}
}

func TestFailedNamedTunnelReplacementPreservesSettings(t *testing.T) {
	// Fail before downloading or starting a connector; the settings have already
	// been staged by this point, just as for an invalid or expired remote token.
	t.Setenv(cloudflaredVersionEnv, "latest")
	t.Setenv(cloudflaredURLEnv, "")
	for _, enabled := range []bool{false, true} {
		previous := SettingsSnapshot{
			Enabled: enabled, TunnelToken: "saved-private-token", TunnelHostname: "api.example.com",
			TunnelURL: "https://api.example.com", TailscaleEnabled: true, TailscaleURL: "https://device.ts.net",
		}
		settings := &memoryTunnelSettings{SettingsSnapshot: previous}
		svc := NewService(NewDataLayout(t.TempDir()), 0, settings)
		if _, err := svc.Enable(context.Background(), 0, "replacement-token", "new.example.com"); err == nil {
			t.Fatal("expected connector startup to fail")
		}
		if settings.SettingsSnapshot != previous {
			t.Fatal("failed replacement discarded the previous tunnel settings")
		}
	}
}

type tunnelTransport func(*http.Request) (*http.Response, error)

func (f tunnelTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNamedTunnelStatusAndRecoveryUseConfiguredHostname(t *testing.T) {
	transport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = transport })
	var probed string
	http.DefaultTransport = tunnelTransport(func(r *http.Request) (*http.Response, error) {
		probed = r.URL.String()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"ok","service":"tproxy"}`)), Header: make(http.Header)}, nil
	})
	settings := &memoryTunnelSettings{SettingsSnapshot: SettingsSnapshot{
		Enabled: true, TunnelToken: "private-token", TunnelHostname: "api.example.com",
		TunnelURL: "https://obsolete.trycloudflare.com",
	}}
	svc := NewService(NewDataLayout(t.TempDir()), 28120, settings)
	status, err := svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PublicURL != "https://api.example.com" || status.ServiceURL != "http://127.0.0.1:28120" || !status.TokenConfigured || !status.Reachable {
		t.Fatalf("unexpected status: %+v", status)
	}
	encoded, _ := json.Marshal(status)
	if strings.Contains(string(encoded), settings.TunnelToken) {
		t.Fatal("status leaked the tunnel token")
	}
	if !svc.storedTunnelReachable(context.Background(), settings.SettingsSnapshot) || probed != "https://api.example.com/healthz" {
		t.Fatalf("recovery probed %q, want the configured hostname", probed)
	}
}

func TestProbeURLAliveRequiresTProxyHealth(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"tproxy", http.StatusOK, `{"status":"ok","service":"tproxy"}`, true},
		{"wrong route", http.StatusOK, `<html>Another website</html>`, false},
		{"another service", http.StatusOK, `{"status":"ok","service":"other"}`, false},
		{"unhealthy", http.StatusOK, `{"status":"unavailable","service":"tproxy"}`, false},
		{"cloudflare error", http.StatusBadGateway, `<html>Bad gateway</html>`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/healthz" {
					t.Errorf("unexpected health path %q", r.URL.Path)
				}
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			if got := ProbeURLAlive(context.Background(), server.URL); got != test.want {
				t.Fatalf("health = %v, want %v", got, test.want)
			}
		})
	}
}

func TestNamedTunnelCommandAndSecretHandling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture requires Unix")
	}
	t.Setenv("TUNNEL_TOKEN", "unrelated-inherited-token")
	t.Setenv("TUNNEL_TOKEN_FILE", "/unrelated/token")
	dir := t.TempDir()
	binary := filepath.Join(dir, "cloudflared")
	// The fixture checks the command contract, then logs the token deliberately
	// to verify that even child-process failures cannot disclose it to the API.
	script := `#!/bin/sh
test -z "$TUNNEL_TOKEN" && test -z "$TUNNEL_TOKEN_FILE" || exit 2
case "$*" in *--url*|*private-test-token*) exit 3;; esac
token_file=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--token-file" ]; then shift; token_file="$1"; fi
  shift
done
test -n "$token_file" || exit 4
test "$(cat "$token_file")" = "private-test-token" || exit 5
printf '%s\n' "$token_file" > "$0.token-path"
echo "invalid token private-test-token" >&2
exit 6
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	c := NewCloudflared(NewDataLayout(dir))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := c.spawnTunnel(ctx, binary, 0, "private-test-token")
	if err == nil || strings.Contains(err.Error(), "private-test-token") || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("expected redacted child failure, got %v", err)
	}
	path, err := os.ReadFile(binary + ".token-path")
	if err != nil {
		t.Fatalf("named tunnel command contract failed: %v", err)
	}
	if _, err := os.Stat(strings.TrimSpace(string(path))); !os.IsNotExist(err) {
		t.Fatalf("private token file was not removed: %v", err)
	}
}

func TestNamedTunnelRemovesTokenAfterConnecting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture requires Unix")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "cloudflared")
	script := `#!/bin/sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--token-file" ]; then shift; token_file="$1"; fi
  shift
done
test "$(cat "$token_file")" = "private-test-token" || exit 2
printf '%s\n' "$token_file" > "$0.token-path"
echo "INF Registered tunnel connection connIndex=0 protocol=http2" >&2
exec sleep 30
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	c := NewCloudflared(NewDataLayout(dir))
	t.Cleanup(func() { c.Kill(0) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.spawnTunnel(ctx, binary, 0, "private-test-token"); err != nil {
		t.Fatal(err)
	}
	if !c.IsConnected() {
		t.Fatal("registered connector must report connected")
	}
	path, err := os.ReadFile(binary + ".token-path")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(strings.TrimSpace(string(path))); !os.IsNotExist(err) {
		t.Fatalf("connected tunnel retained its plaintext token file: %v", err)
	}
}
