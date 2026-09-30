package tunnel

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
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
	// A tunnel left enabled must survive a slow database at boot, so the startup
	// settings load is retried instead of silently disabling auto-resume.
	startupSettingsAttempts = 5
	startupRetryDelaySec    = 3
)

var ErrInvalidConfig = errors.New("invalid Cloudflare Tunnel configuration")
var hostnameLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// CloudflareTunnelURL accepts a user's published hostname or HTTPS origin.
// Ingress and DNS routing are configured in the user's Cloudflare account.
func CloudflareTunnelURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(raw, "#") {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if len(host) > 253 || !strings.Contains(host, ".") || net.ParseIP(host) != nil || host == "trycloudflare.com" || strings.HasSuffix(host, ".trycloudflare.com") {
		return ""
	}
	if strings.Contains(parsed.Host, ":") || (parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" {
		return ""
	}
	for _, label := range strings.Split(host, ".") {
		if !hostnameLabel.MatchString(label) {
			return ""
		}
	}
	return "https://" + host
}

func validateCloudflareConfig(token, hostname string) error {
	if token == "" || len(token) > 16384 || strings.ContainsAny(token, " \t\r\n\x00") {
		return fmt.Errorf("%w: enter the tunnel token from Cloudflare (only the token, not the install command)", ErrInvalidConfig)
	}
	if CloudflareTunnelURL(hostname) == "" {
		return fmt.Errorf("%w: enter your published hostname, for example api.example.com, without a port or path", ErrInvalidConfig)
	}
	return nil
}

func TunnelProtocol() string {
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
