package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type EnableResult struct {
	Success        bool   `json:"success"`
	TunnelURL      string `json:"tunnelUrl,omitempty"`
	ShortID        string `json:"shortId,omitempty"`
	PublicURL      string `json:"publicUrl,omitempty"`
	AlreadyRunning bool   `json:"alreadyRunning,omitempty"`
	Error          string `json:"error,omitempty"`
}

type Status struct {
	Enabled         bool   `json:"enabled"`
	SettingsEnabled bool   `json:"settingsEnabled"`
	TunnelURL       string `json:"tunnelUrl,omitempty"`
	ShortID         string `json:"shortId,omitempty"`
	PublicURL       string `json:"publicUrl,omitempty"`
	Running         bool   `json:"running"`
}

type SettingsSnapshot struct {
	Enabled          bool
	TunnelURL        string
	TailscaleEnabled bool
	TailscaleURL     string
}

type SettingsStore interface {
	LoadSettings(ctx context.Context) (SettingsSnapshot, error)
	SaveCloudflare(ctx context.Context, enabled bool, tunnelURL string) error
	SaveTailscale(ctx context.Context, enabled bool, tunnelURL string) error
	OnPublicURL(ctx context.Context, publicURL string) error
}

type Service struct {
	layout      DataLayout
	localPort   int
	cloudflared *Cloudflared
	tailscale   *Tailscale
	settings    SettingsStore

	mu              sync.Mutex
	cancelled       bool
	spawnInProgress bool
	lastRestartAt   time.Time
	activeLocalPort int
	onUnexpectedExit func()

	tailscaleMu              sync.Mutex
	tailscaleCancelled       bool
	tailscaleSpawnInProgress bool
	tailscaleLastRestartAt   time.Time
	tailscaleActivePort      int

	autoResumed          bool
	tailscaleAutoResumed bool

	watchdogOnce    sync.Once
	networkOnce     sync.Once
	watchdogStop    chan struct{}
	networkStop     chan struct{}
	backgroundStop  chan struct{}
	backgroundOnce  sync.Once
}

func NewService(layout DataLayout, localPort int, settings SettingsStore) *Service {
	svc := &Service{
		layout:         layout,
		localPort:      localPort,
		cloudflared:    NewCloudflared(layout),
		tailscale:      NewTailscale(),
		settings:       settings,
		watchdogStop:   make(chan struct{}),
		networkStop:    make(chan struct{}),
		backgroundStop: make(chan struct{}),
	}
	svc.cloudflared.SetUnexpectedExitHandler(func() {
		svc.mu.Lock()
		handler := svc.onUnexpectedExit
		svc.mu.Unlock()
		if handler != nil {
			handler()
		}
	})
	return svc
}

func (s *Service) DownloadStatus() DownloadStatus {
	return s.cloudflared.DownloadStatus()
}

func (s *Service) IsManuallyDisabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelled
}

func (s *Service) IsReconnecting() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.spawnInProgress
}

func (s *Service) IsTailscaleReconnecting() bool {
	s.tailscaleMu.Lock()
	defer s.tailscaleMu.Unlock()
	return s.tailscaleSpawnInProgress
}

func (s *Service) registerTunnelURL(ctx context.Context, shortID, tunnelURL string) error {
	body, err := json.Marshal(map[string]string{
		"shortId":   shortID,
		"tunnelUrl": tunnelURL,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, WorkerURL()+"/api/tunnel/register", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("worker register: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (s *Service) Enable(ctx context.Context, localPort int) (EnableResult, error) {
	if localPort <= 0 {
		localPort = s.localPort
	}
	log.Printf("[tunnel] enable start (port=%d)", localPort)

	s.mu.Lock()
	s.cancelled = false
	s.activeLocalPort = localPort
	s.spawnInProgress = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.spawnInProgress = false
		s.mu.Unlock()
	}()

	cancelled := func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.cancelled
	}

	if s.cloudflared.IsRunning() {
		state, _ := LoadState(s.layout.StateFile)
		if state != nil && state.TunnelURL != "" && state.ShortID != "" {
			publicURL := PublicURL(state.ShortID)
			directOK := ProbeURLAlive(ctx, state.TunnelURL)
			publicOK := ProbeURLAlive(ctx, publicURL)
			if directOK && publicOK {
				log.Printf("[tunnel] already running, reuse: %s", state.TunnelURL)
				return EnableResult{
					Success:        true,
					TunnelURL:      state.TunnelURL,
					ShortID:        state.ShortID,
					PublicURL:      publicURL,
					AlreadyRunning: true,
				}, nil
			}
			log.Printf("[tunnel] stale (direct=%v public=%v), respawn", directOK, publicOK)
		}
	}

	s.cloudflared.Kill(localPort)
	if cancelled() {
		return EnableResult{}, fmt.Errorf("tunnel cancelled")
	}

	state, _ := LoadState(s.layout.StateFile)
	shortID := ""
	if state != nil {
		shortID = state.ShortID
	}
	if shortID == "" {
		shortID = GenerateShortID()
	}

	onURLUpdate := func(url string) {
		if cancelled() {
			return
		}
		log.Printf("[tunnel] url updated: %s", url)
		_ = s.registerTunnelURL(ctx, shortID, url)
		_ = SaveState(s.layout.StateFile, State{ShortID: shortID, TunnelURL: url})
		_ = s.settings.SaveCloudflare(ctx, true, url)
	}

	tunnelURL, err := s.cloudflared.SpawnQuickTunnel(ctx, localPort, onURLUpdate)
	if err != nil {
		if !strings.Contains(err.Error(), "cloudflared killed") && !strings.Contains(err.Error(), "tunnel cancelled") {
			log.Printf("[tunnel] enable error: %v", err)
		}
		return EnableResult{}, err
	}
	if cancelled() {
		return EnableResult{}, fmt.Errorf("tunnel cancelled")
	}

	publicURL := PublicURL(shortID)
	if err := s.registerTunnelURL(ctx, shortID, tunnelURL); err != nil {
		log.Printf("[tunnel] worker register warning: %v", err)
	}
	_ = SaveState(s.layout.StateFile, State{ShortID: shortID, TunnelURL: tunnelURL})
	_ = s.settings.SaveCloudflare(ctx, true, tunnelURL)
	if publicURL != "" {
		_ = s.settings.OnPublicURL(ctx, publicURL)
	}

	if err := WaitForHealth(ctx, publicURL, cancelled); err != nil {
		return EnableResult{}, err
	}
	if !ProbeURLAlive(ctx, tunnelURL) {
		log.Printf("[tunnel] direct URL not reachable yet, continuing via public URL")
	}
	log.Printf("[tunnel] enable success shortId=%s publicUrl=%s", shortID, publicURL)
	return EnableResult{Success: true, TunnelURL: tunnelURL, ShortID: shortID, PublicURL: publicURL}, nil
}

func (s *Service) Disable(ctx context.Context) error {
	log.Printf("[tunnel] disable")
	s.mu.Lock()
	s.cancelled = true
	port := s.activeLocalPort
	s.activeLocalPort = 0
	s.spawnInProgress = false
	s.mu.Unlock()

	s.cloudflared.SetUnexpectedExitHandler(nil)
	s.cloudflared.Kill(port)

	state, _ := LoadState(s.layout.StateFile)
	if state != nil {
		_ = SaveState(s.layout.StateFile, State{ShortID: state.ShortID, TunnelURL: ""})
	}
	return s.settings.SaveCloudflare(ctx, false, "")
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	settings, err := s.settings.LoadSettings(ctx)
	if err != nil {
		return Status{}, err
	}
	state, _ := LoadState(s.layout.StateFile)
	shortID := ""
	tunnelURL := ""
	if state != nil {
		shortID = state.ShortID
		tunnelURL = state.TunnelURL
	}
	publicURL := PublicURL(shortID)
	running := false
	if settings.Enabled {
		running = s.cloudflared.IsRunning()
	}
	return Status{
		Enabled:         settings.Enabled && running,
		SettingsEnabled: settings.Enabled,
		TunnelURL:       tunnelURL,
		ShortID:         shortID,
		PublicURL:       publicURL,
		Running:         running,
	}, nil
}

type TailscaleStatus struct {
	Enabled         bool   `json:"enabled"`
	SettingsEnabled bool   `json:"settingsEnabled"`
	TunnelURL       string `json:"tunnelUrl,omitempty"`
	Running         bool   `json:"running"`
	LoggedIn        bool   `json:"loggedIn"`
}

func (s *Service) TailscaleStatus(ctx context.Context) (TailscaleStatus, error) {
	settings, err := s.settings.LoadSettings(ctx)
	if err != nil {
		return TailscaleStatus{}, err
	}
	status := TailscaleStatus{
		SettingsEnabled: settings.TailscaleEnabled,
		TunnelURL:       settings.TailscaleURL,
	}
	if !settings.TailscaleEnabled {
		return status, nil
	}
	probe := s.tailscale.Status()
	status.LoggedIn = probe.LoggedIn
	status.Running = probe.Running
	status.Enabled = settings.TailscaleEnabled && probe.Running
	if status.TunnelURL == "" && probe.URL != "" {
		status.TunnelURL = probe.URL
	}
	return status, nil
}

func (s *Service) EnableTailscale(ctx context.Context, localPort int) (TailscaleEnableResult, error) {
	if localPort <= 0 {
		localPort = s.localPort
	}
	log.Printf("[tailscale] enable start (port=%d)", localPort)

	s.tailscaleMu.Lock()
	s.tailscaleCancelled = false
	s.tailscaleActivePort = localPort
	s.tailscaleSpawnInProgress = true
	s.tailscaleMu.Unlock()
	defer func() {
		s.tailscaleMu.Lock()
		s.tailscaleSpawnInProgress = false
		s.tailscaleMu.Unlock()
	}()

	cancelled := func() bool {
		s.tailscaleMu.Lock()
		defer s.tailscaleMu.Unlock()
		return s.tailscaleCancelled
	}

	state, _ := LoadState(s.layout.StateFile)
	shortID := ""
	if state != nil && state.ShortID != "" {
		shortID = state.ShortID
	} else {
		shortID = GenerateShortID()
		_ = SaveState(s.layout.StateFile, State{ShortID: shortID, TunnelURL: stateTunnelURL(state)})
	}

	result := s.tailscale.Enable(ctx, localPort, shortID)
	if cancelled() {
		return TailscaleEnableResult{Success: false, Error: "tailscale cancelled"}, nil
	}
	if result.NeedsLogin || result.FunnelNotEnabled || !result.Success {
		return result, nil
	}
	_ = s.settings.SaveTailscale(ctx, true, result.TunnelURL)
	_ = s.settings.OnPublicURL(ctx, strings.TrimRight(result.TunnelURL, "/"))
	if err := WaitForHealth(ctx, result.TunnelURL, cancelled); err != nil {
		if !strings.Contains(err.Error(), "health check timeout") {
			return result, err
		}
		log.Printf("[tailscale] health check timed out, watchdog will retry")
	}
	return result, nil
}

func stateTunnelURL(state *State) string {
	if state == nil {
		return ""
	}
	return state.TunnelURL
}

func (s *Service) DisableTailscale(ctx context.Context) error {
	log.Printf("[tailscale] disable")
	s.tailscaleMu.Lock()
	s.tailscaleCancelled = true
	s.tailscaleActivePort = 0
	s.tailscaleSpawnInProgress = false
	s.tailscaleMu.Unlock()
	s.tailscale.StopFunnel()
	return s.settings.SaveTailscale(ctx, false, "")
}

// TailscaleCheckResponse is returned by the tailscale-check API.
type TailscaleCheckResponse struct {
	Installed bool `json:"installed"`
	LoggedIn  bool `json:"logged_in"`
	Running   bool `json:"running"`
}

func (s *Service) TailscaleCheckResponse() TailscaleCheckResponse {
	probe := s.tailscale.Status()
	return TailscaleCheckResponse{
		Installed: probe.Installed,
		LoggedIn:  probe.LoggedIn,
		Running:   probe.Running,
	}
}
