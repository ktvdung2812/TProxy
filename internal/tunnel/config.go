package tunnel

import (
	"fmt"
	"os"
	"strings"
)

const (
	defaultWorkerURL    = "https://abc-tunnel.us"
	defaultPublicHost   = "abc-tunnel.us"
	healthCheckInterval = 2
	healthCheckTimeout  = 60
	healthFetchTimeout  = 5
	watchdogIntervalSec = 60
	networkCheckSec     = 5
	restartCooldownSec  = 120
	networkSettleSec    = 3
)

func WorkerURL() string {
	if value := strings.TrimSpace(os.Getenv("TPROXY_TUNNEL_WORKER_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}
	if value := strings.TrimSpace(os.Getenv("TUNNEL_WORKER_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}
	return defaultWorkerURL
}

func PublicHost() string {
	if value := strings.TrimSpace(os.Getenv("TPROXY_TUNNEL_PUBLIC_HOST")); value != "" {
		return strings.Trim(strings.TrimSpace(value), ".")
	}
	return defaultPublicHost
}

func PublicURL(shortID string) string {
	shortID = strings.TrimSpace(shortID)
	if shortID == "" {
		return ""
	}
	return fmt.Sprintf("https://r%s.%s", shortID, PublicHost())
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
