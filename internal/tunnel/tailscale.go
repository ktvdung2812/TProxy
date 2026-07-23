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
	Success           bool   `json:"success"`
	TunnelURL         string `json:"tunnelUrl,omitempty"`
	NeedsLogin        bool   `json:"needsLogin,omitempty"`
	AuthURL           string `json:"authUrl,omitempty"`
	FunnelNotEnabled  bool   `json:"funnelNotEnabled,omitempty"`
	EnableURL         string `json:"enableUrl,omitempty"`
	Error             string `json:"error,omitempty"`
}

type Tailscale struct{}

func NewTailscale() *Tailscale { return &Tailscale{} }

func (t *Tailscale) Binary() string {
	path, err := exec.LookPath("tailscale")
	if err != nil {
		return ""
	}
	return path
}

func (t *Tailscale) Installed() bool {
	return t.Binary() != ""
}

func (t *Tailscale) Status() TailscaleProbe {
	status := TailscaleProbe{Installed: t.Installed()}
	if !status.Installed {
		return status
	}
	status.LoggedIn = t.loggedIn()
	status.Running = status.LoggedIn
	if url := t.funnelURL(); url != "" {
		status.URL = url
	}
	return status
}

func (t *Tailscale) loggedIn() bool {
	bin := t.Binary()
	if bin == "" {
		return false
	}
	out, err := exec.Command(bin, "status", "--json").Output()
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

func (t *Tailscale) funnelURL() string {
	bin := t.Binary()
	if bin == "" {
		return ""
	}
	out, err := exec.Command(bin, "status", "--json").Output()
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
	bin := t.Binary()
	if bin == "" {
		return ""
	}
	out, err := exec.Command(bin, "status", "--json").Output()
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
	_ = exec.Command(bin, "funnel", "--bg", "reset").Run()
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
	_ = exec.Command(bin, "funnel", "--bg", "reset").Run()
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
