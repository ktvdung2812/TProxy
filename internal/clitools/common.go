package clitools

import (
	"encoding/json"
	"fmt"
	"net"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const providerKey = "tproxy"
const legacyProviderKey = "9router"

var trailingCommaRE = regexp.MustCompile(`,(\s*[}\]])`)

type ApplyRequest struct {
	BaseURL string   `json:"baseUrl"`
	APIKey  string   `json:"apiKey"`
	Model   string   `json:"model"`
	Models  []string `json:"models"`
	// SubagentModel overrides the model used for spawned subagents. Empty means
	// "same as the primary model" — the behaviour every handler had before.
	SubagentModel string            `json:"subagentModel"`
	Env           map[string]string `json:"env"`
	// Plugins/CustomPlugins are Cowork-only (managed MCP servers).
	Plugins       []CoworkPlugin `json:"plugins"`
	CustomPlugins []CoworkPlugin `json:"customPlugins"`
}

type StatusResult struct {
	Installed    bool   `json:"installed"`
	HasTproxy    bool   `json:"has_tproxy"`
	Has9Router   bool   `json:"has_9router"`
	SettingsPath string `json:"settings_path,omitempty"`
	ConfigPath   string `json:"config_path,omitempty"`
	Message      string `json:"message,omitempty"`
	// Endpoint/Model expose what the tool is currently pointed at so the dashboard
	// can tell "configured for this tproxy" from "configured for something else"
	// instead of guessing from a substring match.
	Endpoint string `json:"endpoint,omitempty"`
	Model    string `json:"model,omitempty"`
	// Cowork-only: the managed-MCP catalog and which entries are currently written.
	Plugins       []CoworkPlugin `json:"plugins,omitempty"`
	ActivePlugins []string       `json:"active_plugins,omitempty"`
	Models        []string       `json:"models,omitempty"`
}

// subagentOf resolves the subagent model for a request, defaulting to primary.
func subagentOf(req ApplyRequest, primary string) string {
	if s := strings.TrimSpace(req.SubagentModel); s != "" {
		return s
	}
	return primary
}

func homeDir() string {
	if dir, err := os.UserHomeDir(); err == nil && dir != "" {
		return dir
	}
	return ""
}

func expandHome(path string) string {
	if path == "" {
		return path
	}
	if path[0] == '~' {
		return filepath.Join(homeDir(), strings.TrimPrefix(path, "~/"))
	}
	return path
}

func commandInstalled(binary string) bool {
	if binary == "" {
		return false
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("where", binary)
	} else {
		cmd = exec.Command("which", binary)
	}
	if err := cmd.Run(); err == nil {
		return true
	}
	return false
}

func installed(binary string, configPath string) bool {
	if commandInstalled(binary) {
		return true
	}
	if configPath == "" {
		return false
	}
	_, err := os.Stat(configPath)
	return err == nil
}

func ensureDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func writeFile(path string, content []byte) error {
	if err := ensureDir(path); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

func readJSONFile(path string) (map[string]any, error) {
	raw, err := readFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	stripped := trailingCommaRE.ReplaceAllString(string(raw), "$1")
	var out map[string]any
	if err := json.Unmarshal([]byte(stripped), &out); err != nil {
		return map[string]any{}, nil
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func writeJSONFile(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(path, raw)
}

func normalizeBaseURL(baseURL string, withV1 bool) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" {
		return ""
	}
	if withV1 {
		if strings.HasSuffix(trimmed, "/v1") {
			return trimmed
		}
		return trimmed + "/v1"
	}
	return strings.TrimSuffix(trimmed, "/v1")
}

// endpointLooksLikeProxy reports whether a configured endpoint plausibly points at
// a self-hosted tproxy. Loopback is the obvious case, but the dashboard also hands
// out LAN and tunnel URLs, so those must not be reported as "not configured".
func endpointLooksLikeProxy(url string) bool {
	lower := strings.ToLower(strings.TrimSpace(url))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, providerKey) || strings.Contains(lower, legacyProviderKey) {
		return true
	}
	host := hostOf(lower)
	if host == "" {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	// Cloudflare quick tunnels and Tailscale Funnel hostnames handed out by our own
	// tunnel manager (internal/tunnel).
	if strings.HasSuffix(host, ".trycloudflare.com") || strings.HasSuffix(host, ".ts.net") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast()
	}
	return false
}

// hostOf extracts the bare hostname from a URL or host:port string.
func hostOf(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "http://" + trimmed
	}
	parsed, err := neturl.Parse(trimmed)
	if err != nil {
		return ""
	}
	return strings.Trim(parsed.Hostname(), "[]")
}

func asMap(value any) map[string]any {
	if m, ok := value.(map[string]any); ok && m != nil {
		return m
	}
	return map[string]any{}
}

func asSlice(value any) []any {
	if s, ok := value.([]any); ok {
		return s
	}
	return nil
}

func asString(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func modelsFromRequest(req ApplyRequest) []string {
	if len(req.Models) > 0 {
		return req.Models
	}
	if strings.TrimSpace(req.Model) != "" {
		return []string{strings.TrimSpace(req.Model)}
	}
	return nil
}

func applyError(tool string, err error) error {
	return fmt.Errorf("%s: %w", tool, err)
}
