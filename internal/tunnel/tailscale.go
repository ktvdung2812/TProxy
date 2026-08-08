package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

type TailscaleProbe struct {
	Installed bool   `json:"installed"`
	LoggedIn  bool   `json:"logged_in"`
	Running   bool   `json:"running"`
	URL       string `json:"url,omitempty"`
}

type TailscaleEnableResult struct {
	Success          bool   `json:"success"`
	TunnelURL        string `json:"tunnelUrl,omitempty"`
	NeedsLogin       bool   `json:"needsLogin,omitempty"`
	AuthURL          string `json:"authUrl,omitempty"`
	FunnelNotEnabled bool   `json:"funnelNotEnabled,omitempty"`
	EnableURL        string `json:"enableUrl,omitempty"`
	Error            string `json:"error,omitempty"`
}

type Tailscale struct {
	// binary/run are injectable so the status logic can be tested without a
	// tailscale install.
	binary string
	run    func(name string, args ...string) ([]byte, error)
}

func NewTailscale() *Tailscale { return &Tailscale{} }

func (t *Tailscale) Binary() string {
	if t.binary != "" {
		return t.binary
	}
	path, err := exec.LookPath("tailscale")
	if err != nil {
		return ""
	}
	return path
}

func (t *Tailscale) exec(name string, args ...string) ([]byte, error) {
	if t.run != nil {
		return t.run(name, args...)
	}
	return exec.Command(name, args...).Output()
}

func (t *Tailscale) Installed() bool {
	return t.Binary() != ""
}

// Status reports login state and, separately, whether a Funnel is actually
// serving. The two are not the same: `tailscale funnel reset`, a tailnet policy
// change or a machine wake can drop the Funnel while tailscaled stays logged in.
// Treating "logged in" as "running" made the watchdog skip every repair.
func (t *Tailscale) Status() TailscaleProbe {
	status := TailscaleProbe{Installed: t.Installed()}
	if !status.Installed {
		return status
	}
	status.LoggedIn = t.loggedIn()
	if url := t.funnelURL(); url != "" {
		status.URL = url
	}
	if !status.LoggedIn {
		return status
	}
	active, known := t.funnelActive()
	if !known {
		// Older tailscale builds have no machine-readable serve status. Fall back
		// to the previous behaviour rather than declaring the funnel dead and
		// restarting it on every watchdog tick.
		status.Running = status.LoggedIn
		return status
	}
	status.Running = active
	return status
}

func (t *Tailscale) statusJSON() ([]byte, error) {
	bin := t.Binary()
	if bin == "" {
		return nil, fmt.Errorf("tailscale not installed")
	}
	return t.exec(bin, "status", "--json")
}

func (t *Tailscale) loggedIn() bool {
	out, err := t.statusJSON()
	if err != nil {
		return false
	}
	var payload struct {
		BackendState string `json:"BackendState"`
	}
	if json.Unmarshal(out, &payload) != nil {
		return false
	}
	return strings.EqualFold(payload.BackendState, "Running")
}

// funnelActive reports whether any Funnel target is currently allowed. The
// second return value is false when serve status could not be read at all, so
// callers can distinguish "no funnel" from "cannot tell".
func (t *Tailscale) funnelActive() (active bool, known bool) {
	bin := t.Binary()
	if bin == "" {
		return false, false
	}
	out, err := t.exec(bin, "serve", "status", "--json")
	if err != nil {
		return false, false
	}
	var payload struct {
		AllowFunnel map[string]bool `json:"AllowFunnel"`
	}
	if json.Unmarshal(out, &payload) != nil {
		return false, false
	}
	for _, allowed := range payload.AllowFunnel {
		if allowed {
			return true, true
		}
	}
	return false, true
}

func (t *Tailscale) funnelURL() string {
	out, err := t.statusJSON()
	if err != nil {
		return ""
	}
	var payload struct {
		Self struct {
			DNSName string `json:"DNSName"`
		} `json:"Self"`
	}
	if json.Unmarshal(out, &payload) != nil {
		return ""
	}
	host := strings.TrimSuffix(strings.TrimSpace(payload.Self.DNSName), ".")
	if host == "" {
		return ""
	}
	return "https://" + host
}

func (t *Tailscale) StartLogin(hostname string) (authURL string, alreadyLoggedIn bool, err error) {
	if !t.Installed() {
		return "", false, fmt.Errorf("tailscale not installed")
	}
	if t.loggedIn() {
		return "", true, nil
	}
	bin := t.Binary()
	args := []string{"up", "--accept-routes"}
	if strings.TrimSpace(hostname) != "" {
		args = append(args, "--hostname="+strings.TrimSpace(hostname))
	}
	cmd := exec.Command(bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Start()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if url := parseTailscaleAuthURL(stdout.String() + stderr.String()); url != "" {
			return url, false, nil
		}
		if url := t.authURLFromStatus(); url != "" {
			return url, false, nil
		}
		if t.loggedIn() {
			return "", true, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	if url := parseTailscaleAuthURL(stdout.String() + stderr.String()); url != "" {
		return url, false, nil
	}
	if url := t.authURLFromStatus(); url != "" {
		return url, false, nil
	}
	return "", false, fmt.Errorf("tailscale login timed out without auth URL")
}

func (t *Tailscale) authURLFromStatus() string {
	out, err := t.statusJSON()
	if err != nil {
		return ""
	}
	var payload struct {
		AuthURL string `json:"AuthURL"`
	}
	if json.Unmarshal(out, &payload) != nil {
		return ""
	}
	return strings.TrimSpace(payload.AuthURL)
}

var tailscaleAuthPattern = regexp.MustCompile(`https://login\.tailscale\.com/a/[a-zA-Z0-9]+`)

func parseTailscaleAuthURL(text string) string {
	match := tailscaleAuthPattern.FindString(text)
	return match
}

func (t *Tailscale) StartFunnel(port int) (tunnelURL string, funnelNotEnabled bool, enableURL string, err error) {
	bin := t.Binary()
	if bin == "" {
		return "", false, "", fmt.Errorf("tailscale not installed")
	}
	_, _ = t.exec(bin, "funnel", "--bg", "reset")
	cmd := exec.Command(bin, "funnel", "--bg", fmt.Sprintf("%d", port))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		combined := stdout.String() + stderr.String()
		if strings.Contains(combined, "Funnel is not enabled") {
			if match := regexp.MustCompile(`https://login\.tailscale\.com/[^\s]+`).FindString(combined); match != "" {
				return "", true, match, nil
			}
			return "", true, "", fmt.Errorf("tailscale funnel is not enabled on this tailnet")
		}
		return "", false, "", fmt.Errorf("tailscale funnel failed: %w (%s)", err, strings.TrimSpace(combined))
	}
	if url := t.funnelURL(); url != "" {
		return url, false, "", nil
	}
	return "", false, "", fmt.Errorf("tailscale funnel started but public URL is unavailable")
}

func (t *Tailscale) StopFunnel() {
	bin := t.Binary()
	if bin == "" {
		return
	}
	_, _ = t.exec(bin, "funnel", "--bg", "reset")
}

func (t *Tailscale) Enable(ctx context.Context, port int, hostname string) TailscaleEnableResult {
	if !t.Installed() {
		return TailscaleEnableResult{Success: false, Error: "Tailscale is not installed. Install it with: brew install tailscale"}
	}
	authURL, alreadyLoggedIn, err := t.StartLogin(hostname)
	if err != nil {
		return TailscaleEnableResult{Success: false, Error: err.Error()}
	}
	if !alreadyLoggedIn && authURL != "" {
		return TailscaleEnableResult{NeedsLogin: true, AuthURL: authURL}
	}
	tunnelURL, funnelNotEnabled, enableURL, err := t.StartFunnel(port)
	if funnelNotEnabled {
		return TailscaleEnableResult{FunnelNotEnabled: true, EnableURL: enableURL}
	}
	if err != nil {
		if strings.Contains(err.Error(), "not logged in") || strings.Contains(err.Error(), "NoState") {
			if authURL, _, loginErr := t.StartLogin(hostname); loginErr == nil && authURL != "" {
				return TailscaleEnableResult{NeedsLogin: true, AuthURL: authURL}
			}
		}
		return TailscaleEnableResult{Success: false, Error: err.Error()}
	}
	_ = ctx
	if !ProbeURLAlive(ctx, tunnelURL) {
		// DNS may still be propagating; watchdog can recover.
	}
	return TailscaleEnableResult{Success: true, TunnelURL: tunnelURL}
}
