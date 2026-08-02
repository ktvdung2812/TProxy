package tunnel

import (
	"context"
	"fmt"
	"log"
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
	Connected       bool   `json:"connected"`
	Reachable       bool   `json:"reachable"`
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

	mu               sync.Mutex
	cancelled        bool
	spawnInProgress  bool
	lastRestartAt    time.Time
	activeLocalPort  int
	onUnexpectedExit func()

	tailscaleMu              sync.Mutex
	tailscaleCancelled       bool
	tailscaleSpawnInProgress bool
	tailscaleLastRestartAt   time.Time
	tailscaleActivePort      int

	autoResumed          bool
	tailscaleAutoResumed bool

	watchdogOnce   sync.Once
	networkOnce    sync.Once
	watchdogStop   chan struct{}
	networkStop    chan struct{}
	backgroundStop chan struct{}
	backgroundOnce sync.Once
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
	svc.installUnexpectedExitHandler()
	return svc
}

func (s *Service) installUnexpectedExitHandler() {
	s.cloudflared.SetUnexpectedExitHandler(func() {
		s.mu.Lock()
		handler := s.onUnexpectedExit
		s.mu.Unlock()
		if handler != nil {
			handler()
		}
	})
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

func (s *Service) persistCloudflareTunnel(ctx context.Context, state State) {
	if err := SaveState(s.layout.StateFile, state); err != nil {
		log.Printf("[tunnel] save state warning: %v", err)
	}
	if err := s.settings.SaveCloudflare(ctx, true, state.TunnelURL); err != nil {
		log.Printf("[tunnel] save settings warning: %v", err)
	}
	if publicURL := CloudflareQuickTunnelURL(state.TunnelURL); publicURL != "" {
		if err := s.settings.OnPublicURL(ctx, publicURL); err != nil {
			log.Printf("[tunnel] save public URL warning: %v", err)
		}
	}
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
	s.installUnexpectedExitHandler()

	cancelled := func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.cancelled
	}

	if s.cloudflared.IsRunning() && s.cloudflared.IsConnected() {
		state, _ := LoadState(s.layout.StateFile)
		if state != nil && state.TunnelURL != "" {
			publicURL := CloudflareQuickTunnelURL(state.TunnelURL)
			log.Printf("[tunnel] already running, reuse: %s", state.TunnelURL)
			return EnableResult{
				Success:        true,
				TunnelURL:      state.TunnelURL,
				ShortID:        state.ShortID,
				PublicURL:      publicURL,
				AlreadyRunning: true,
			}, nil
		}
		log.Printf("[tunnel] cloudflared is connected but tunnel state is missing; recreating")
	} else if s.cloudflared.IsRunning() {
		log.Printf("[tunnel] cloudflared is running without an active Cloudflare connection; recreating")
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

	onURLUpdate := func(url string) {
		if cancelled() {
			return
		}
		publicURL := CloudflareQuickTunnelURL(url)
		if publicURL == "" {
			log.Printf("[tunnel] ignored invalid quick-tunnel URL update: %q", url)
			return
		}
		log.Printf("[tunnel] url updated: %s", publicURL)
		s.persistCloudflareTunnel(context.Background(), State{ShortID: shortID, TunnelURL: publicURL})
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

	publicURL := CloudflareQuickTunnelURL(tunnelURL)
	if publicURL == "" {
		s.cloudflared.Kill(localPort)
		return EnableResult{}, fmt.Errorf("cloudflared returned an invalid Cloudflare quick-tunnel URL")
	}
	s.persistCloudflareTunnel(context.Background(), State{ShortID: shortID, TunnelURL: publicURL})

	log.Printf("[tunnel] connector registered publicUrl=%s (public DNS may take a few moments)", publicURL)
	return EnableResult{Success: true, TunnelURL: publicURL, ShortID: shortID, PublicURL: publicURL}, nil
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
	publicURL := CloudflareQuickTunnelURL(tunnelURL)
	running := false
	connected := false
	reachable := false
	if settings.Enabled {
		running = s.cloudflared.IsRunning()
		connected = s.cloudflared.IsConnected()
		if publicURL != "" && ProbeURLAlive(ctx, publicURL) {
			reachable = true
		} else if tunnelURL != "" && ProbeURLAlive(ctx, tunnelURL) {
			reachable = true
		}
	}
	return Status{
		Enabled:         settings.Enabled && running && connected,
		SettingsEnabled: settings.Enabled,
		TunnelURL:       tunnelURL,
		ShortID:         shortID,
		PublicURL:       publicURL,
		Running:         running,
		Connected:       connected,
		Reachable:       reachable,
	}, nil
}

type TailscaleStatus struct {
	Enabled         bool   `json:"enabled"`
	SettingsEnabled bool   `json:"settingsEnabled"`
	TunnelURL       string `json:"tunnelUrl,omitempty"`
	Running         bool   `json:"running"`
	LoggedIn        bool   `json:"loggedIn"`
	Reachable       bool   `json:"reachable"`
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
	if settings.TailscaleEnabled && status.TunnelURL != "" {
		status.Reachable = ProbeURLAlive(ctx, status.TunnelURL)
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
