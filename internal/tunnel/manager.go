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
	Hostname        string `json:"hostname,omitempty"`
	TokenConfigured bool   `json:"tokenConfigured"`
	ServiceURL      string `json:"serviceUrl"`
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
	TunnelToken      string
	TunnelHostname   string
	TailscaleEnabled bool
	TailscaleURL     string
}

type SettingsStore interface {
	LoadSettings(ctx context.Context) (SettingsSnapshot, error)
	SaveCloudflare(ctx context.Context, enabled bool, token, hostname string) error
	SaveTailscale(ctx context.Context, enabled bool, tunnelURL string) error
	OnPublicURL(ctx context.Context, publicURL string) error
}

type Service struct {
	layout      DataLayout
	localPort   int
	cloudflared *Cloudflared
	tailscale   *Tailscale
	settings    SettingsStore

	cloudflareOpMu   sync.Mutex
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

// Enable uses saved credentials when token is empty, including during recovery.
// The hostname route must already exist in the user's Cloudflare account.
func (s *Service) Enable(ctx context.Context, localPort int, token, hostname string) (EnableResult, error) {
	return s.enable(ctx, localPort, token, hostname, false)
}

func (s *Service) enable(ctx context.Context, localPort int, token, hostname string, resume bool) (EnableResult, error) {
	s.cloudflareOpMu.Lock()
	defer s.cloudflareOpMu.Unlock()

	settings, err := s.settings.LoadSettings(ctx)
	if err != nil {
		return EnableResult{}, err
	}
	// A queued recovery must not undo a manual disable while waiting for the lock.
	if resume && !settings.Enabled {
		return EnableResult{}, fmt.Errorf("tunnel cancelled")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		token = settings.TunnelToken
	}
	if hostname == "" {
		hostname = settings.TunnelHostname
	}
	if err := validateCloudflareConfig(token, hostname); err != nil {
		return EnableResult{}, err
	}
	publicURL := CloudflareTunnelURL(hostname)
	hostname = strings.TrimPrefix(publicURL, "https://")
	if localPort <= 0 {
		localPort = s.localPort
	}

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

	if settings.Enabled && settings.TunnelToken == token && settings.TunnelHostname == hostname && s.cloudflared.IsRunning() && s.cloudflared.IsConnected() {
		return EnableResult{Success: true, TunnelURL: publicURL, PublicURL: publicURL, AlreadyRunning: true}, nil
	}

	s.cloudflared.Kill(localPort)
	// Persist the hostname BEFORE starting: dashboard access must be protected
	// as soon as Cloudflare begins forwarding requests to this instance.
	if err := s.settings.SaveCloudflare(ctx, settings.Enabled, token, hostname); err != nil {
		return EnableResult{}, err
	}
	restoreSettings := func(startErr error) error {
		// A failed replacement must not discard the previous credentials. The
		// request may have been cancelled, but recovery still needs its settings.
		restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := s.settings.SaveCloudflare(restoreCtx, settings.Enabled, settings.TunnelToken, settings.TunnelHostname); err != nil {
			return fmt.Errorf("%w; restore previous Cloudflare Tunnel settings: %v", startErr, err)
		}
		return startErr
	}
	if err := s.cloudflared.SpawnTunnel(ctx, localPort, token); err != nil {
		return EnableResult{}, restoreSettings(err)
	}
	if err := s.settings.SaveCloudflare(ctx, true, token, hostname); err != nil {
		s.cloudflared.Kill(localPort)
		return EnableResult{}, restoreSettings(err)
	}
	state, _ := LoadState(s.layout.StateFile)
	shortID := ""
	if state != nil {
		shortID = state.ShortID
	}
	if err := SaveState(s.layout.StateFile, State{ShortID: shortID, TunnelURL: publicURL}); err != nil {
		log.Printf("[tunnel] save state warning: %v", err)
	}
	if err := s.settings.OnPublicURL(ctx, publicURL); err != nil {
		log.Printf("[tunnel] save public URL warning: %v", err)
	}
	log.Printf("[tunnel] connector registered publicUrl=%s", publicURL)
	return EnableResult{Success: true, TunnelURL: publicURL, PublicURL: publicURL}, nil
}

func (s *Service) Disable(ctx context.Context) error {
	s.cloudflareOpMu.Lock()
	defer s.cloudflareOpMu.Unlock()
	log.Printf("[tunnel] disable")
	s.mu.Lock()
	s.cancelled = true
	port := s.activeLocalPort
	if port <= 0 {
		port = s.localPort
	}
	s.activeLocalPort = 0
	s.mu.Unlock()

	s.cloudflared.SetUnexpectedExitHandler(nil)
	s.cloudflared.Kill(port)

	settings, err := s.settings.LoadSettings(ctx)
	if err != nil {
		return err
	}
	state, _ := LoadState(s.layout.StateFile)
	if state != nil {
		_ = SaveState(s.layout.StateFile, State{ShortID: state.ShortID})
	}
	// Retain the credentials and hostname so the user can re-enable the same
	// tunnel, and requests from other connectors still obey dashboard policy.
	return s.settings.SaveCloudflare(ctx, false, settings.TunnelToken, settings.TunnelHostname)
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	settings, err := s.settings.LoadSettings(ctx)
	if err != nil {
		return Status{}, err
	}
	publicURL := CloudflareTunnelURL(settings.TunnelHostname)
	status := Status{
		Hostname:        settings.TunnelHostname,
		TokenConfigured: settings.TunnelToken != "",
		ServiceURL:      fmt.Sprintf("http://127.0.0.1:%d", s.localPort),
		SettingsEnabled: settings.Enabled && settings.TunnelToken != "" && publicURL != "",
	}
	if status.SettingsEnabled {
		status.TunnelURL = publicURL
		status.PublicURL = publicURL
		status.Running = s.cloudflared.IsRunning()
		status.Connected = s.cloudflared.IsConnected()
		status.Reachable = ProbeURLAlive(ctx, publicURL)
		status.Enabled = status.Running && (status.Connected || status.Reachable)
	}
	return status, nil
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
