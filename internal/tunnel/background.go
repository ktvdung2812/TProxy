package tunnel

import (
	"context"
	"log"
	"net"
	"regexp"
	"sort"
	"strings"
	"time"
)

var virtualIfacePattern = regexp.MustCompile(`(?i)^(utun|tun|tap|docker|veth|br-|vmnet|lo|wg)`)

type networkMonitor struct {
	lastFingerprint string
	lastWatchdogTick time.Time
	lastOnline       *bool
}

func networkFingerprint() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	active := make([]string, 0, len(ifaces))
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if virtualIfacePattern.MatchString(iface.Name) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP == nil || ipNet.IP.To4() == nil {
				continue
			}
			active = append(active, iface.Name+":"+ipNet.IP.String())
		}
	}
	sort.Strings(active)
	return strings.Join(active, "|")
}

func (s *Service) StartBackground(ctx context.Context) {
	s.mu.Lock()
	s.onUnexpectedExit = func() {
		go s.safeRestartTunnel(ctx, "unexpected-exit")
	}
	s.mu.Unlock()

	go func() {
		select {
		case <-time.After(3 * time.Second):
			s.runDeferredStartup(ctx)
		case <-ctx.Done():
			return
		case <-s.backgroundStop:
			return
		}
	}()
}

func (s *Service) StopBackground() {
	s.backgroundOnce.Do(func() {
		close(s.backgroundStop)
		close(s.watchdogStop)
		close(s.networkStop)
	})
}

func (s *Service) runDeferredStartup(ctx context.Context) {
	settings, err := s.settings.LoadSettings(ctx)
	if err != nil {
		log.Printf("[tunnel] startup settings load failed: %v", err)
		return
	}
	if settings.Enabled && !s.autoResumed {
		s.autoResumed = true
		log.Printf("[tunnel] auto-resuming cloudflare tunnel")
		go s.safeRestartTunnel(ctx, "startup")
	}
	if settings.TailscaleEnabled && !s.tailscaleAutoResumed {
		s.tailscaleAutoResumed = true
		log.Printf("[tunnel] auto-resuming tailscale funnel")
		go s.safeRestartTailscale(ctx, "startup")
	}
	if settings.Enabled || settings.TailscaleEnabled {
		s.configureMonitoring(true)
	}
}

func (s *Service) ConfigureMonitoringFromSettings(ctx context.Context) {
	settings, err := s.settings.LoadSettings(ctx)
	if err != nil {
		return
	}
	s.configureMonitoring(settings.Enabled || settings.TailscaleEnabled)
}

func (s *Service) configureMonitoring(enabled bool) {
	if !enabled {
		return
	}
	s.startWatchdog()
	s.startNetworkMonitor()
}

var forceRestartReasons = map[string]struct{}{
	"startup":         {},
	"netchange":       {},
	"sleep":           {},
	"sleep+netchange": {},
	"online":          {},
	"unexpected-exit": {},
}

func (s *Service) safeRestartTunnel(ctx context.Context, reason string) {
	settings, err := s.settings.LoadSettings(ctx)
	if err != nil || !settings.Enabled {
		return
	}
	s.mu.Lock()
	if s.cancelled || s.spawnInProgress {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	if s.cloudflared.IsRunning() {
		return
	}

	_, force := forceRestartReasons[reason]
	s.mu.Lock()
	if !force && time.Since(s.lastRestartAt) < restartCooldownSec*time.Second {
		log.Printf("[tunnel] degraded but cooldown active, skip (%s)", reason)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	if !CheckInternet() {
		return
	}

	log.Printf("[tunnel] safeRestart (%s)", reason)
	if _, err := s.Enable(ctx, s.activeLocalPort); err != nil {
		if !stringsContainsAny(err.Error(), "cloudflared killed", "tunnel cancelled") {
			log.Printf("[tunnel] restart failed: %v", err)
		}
		return
	}
	s.mu.Lock()
	s.lastRestartAt = time.Now()
	s.mu.Unlock()
	log.Printf("[tunnel] restart success")
}

func (s *Service) safeRestartTailscale(ctx context.Context, reason string) {
	settings, err := s.settings.LoadSettings(ctx)
	if err != nil || !settings.TailscaleEnabled {
		return
	}
	s.tailscaleMu.Lock()
	if s.tailscaleCancelled || s.tailscaleSpawnInProgress {
		s.tailscaleMu.Unlock()
		return
	}
	port := s.tailscaleActivePort
	if port <= 0 {
		port = s.localPort
	}
	s.tailscaleMu.Unlock()

	probe := s.tailscale.Status()
	if reason != "startup" && probe.Running {
		return
	}
	if reason == "startup" && probe.LoggedIn && probe.Running {
		return
	}

	if probe.LoggedIn && port > 0 {
		s.tailscale.StopFunnel()
		if url, _, _, err := s.tailscale.StartFunnel(port); err == nil && url != "" {
			_ = s.settings.SaveTailscale(ctx, true, url)
			s.tailscaleMu.Lock()
			s.tailscaleLastRestartAt = time.Now()
			s.tailscaleMu.Unlock()
			log.Printf("[tailscale] funnel re-established")
		}
		return
	}

	_, force := forceRestartReasons[reason]
	s.tailscaleMu.Lock()
	if !force && time.Since(s.tailscaleLastRestartAt) < restartCooldownSec*time.Second {
		log.Printf("[tailscale] degraded but cooldown active, skip (%s)", reason)
		s.tailscaleMu.Unlock()
		return
	}
	s.tailscaleMu.Unlock()

	if !CheckInternet() {
		return
	}

	log.Printf("[tailscale] safeRestart (%s)", reason)
	if _, err := s.EnableTailscale(ctx, port); err != nil {
		log.Printf("[tailscale] restart failed: %v", err)
		return
	}
	s.tailscaleMu.Lock()
	s.tailscaleLastRestartAt = time.Now()
	s.tailscaleMu.Unlock()
}

func (s *Service) startWatchdog() {
	s.watchdogOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(watchdogIntervalSec * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					ctx := context.Background()
					s.safeRestartTunnel(ctx, "watchdog")
					s.safeRestartTailscale(ctx, "watchdog")
				case <-s.watchdogStop:
					return
				case <-s.backgroundStop:
					return
				}
			}
		}()
	})
}

func (s *Service) startNetworkMonitor() {
	s.networkOnce.Do(func() {
		go func() {
			monitor := networkMonitor{
				lastFingerprint:  networkFingerprint(),
				lastWatchdogTick: time.Now(),
			}
			ticker := time.NewTicker(networkCheckSec * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					now := time.Now()
					elapsed := now.Sub(monitor.lastWatchdogTick)
					monitor.lastWatchdogTick = now
					current := networkFingerprint()
					networkChanged := current != monitor.lastFingerprint
					wasSleep := elapsed > networkCheckSec*6*time.Second
					if networkChanged {
						monitor.lastFingerprint = current
					}
					online := CheckInternet()
					wasOffline := monitor.lastOnline != nil && !*monitor.lastOnline
					monitor.lastOnline = &online
					if !online {
						continue
					}
					onlineEdge := wasOffline
					if !networkChanged && !wasSleep && !onlineEdge {
						continue
					}
					time.Sleep(networkSettleSec * time.Second)
					reason := "netchange"
					switch {
					case onlineEdge:
						reason = "online"
					case wasSleep && networkChanged:
						reason = "sleep+netchange"
					case wasSleep:
						reason = "sleep"
					}
					ctx := context.Background()
					s.safeRestartTunnel(ctx, reason)
					s.safeRestartTailscale(ctx, reason)
				case <-s.networkStop:
					return
				case <-s.backgroundStop:
					return
				}
			}
		}()
	})
}

func stringsContainsAny(value string, parts ...string) bool {
	for _, part := range parts {
		if strings.Contains(value, part) {
			return true
		}
	}
	return false
}
