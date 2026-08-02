package tunnel

import (
	"net/url"
	"os"
	"strings"
)

const (
	healthCheckInterval = 2
	healthCheckTimeout  = 60
	healthFetchTimeout  = 5
	watchdogIntervalSec = 60
	networkCheckSec     = 5
	restartCooldownSec  = 120
	networkSettleSec    = 3
)

// CloudflareQuickTunnelURL returns the canonical public origin emitted by
// cloudflared's quick-tunnel mode. Quick tunnels are intentionally ephemeral:
// a new URL is allocated whenever cloudflared reconnects.
func CloudflareQuickTunnelURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" || !strings.HasSuffix(host, ".trycloudflare.com") || strings.TrimSuffix(host, ".trycloudflare.com") == "" {
		return ""
	}
	if parsed.Port() != "" || (parsed.Path != "" && parsed.Path != "/") {
		return ""
	}
	return "https://" + host
}

func QuickTunnelProtocol() string {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("TUNNEL_TRANSPORT_PROTOCOL")))
	if value == "" {
		value = strings.ToLower(strings.TrimSpace(os.Getenv("CLOUDFLARED_PROTOCOL")))
	}
	switch value {
	case "quic", "auto", "http2":
		return value
	default:
		return "http2"
	}
}
