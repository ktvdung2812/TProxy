package clitools

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Claude Desktop in Cowork ("3p") mode reads its inference settings from a config
// library keyed by an applied config id, not from a single settings file. Applying
// means: flip the 1p config into 3p mode, make sure a config id exists, then write
// the gateway + managed MCP servers into that id's file.

const coworkProvider = "gateway"

// CoworkPlugin is a managed MCP server entry shown in the dashboard.
type CoworkPlugin struct {
	Name      string   `json:"name"`
	Title     string   `json:"title,omitempty"`
	URL       string   `json:"url"`
	Transport string   `json:"transport,omitempty"`
	OAuth     bool     `json:"oauth,omitempty"`
	ToolNames []string `json:"toolNames,omitempty"`
	Custom    bool     `json:"custom,omitempty"`
}

// DefaultCoworkPlugins mirrors 9router's DEFAULT_PLUGINS — remote HTTPS MCP servers
// only. 9router also bridges local stdio plugins through its own /api/mcp/ SSE
// endpoint; tproxy exposes no such bridge, so stdio plugins are out of scope here.
func DefaultCoworkPlugins() []CoworkPlugin {
	return []CoworkPlugin{
		{
			Name:      "exa",
			Title:     "Exa",
			URL:       "https://mcp.exa.ai/mcp",
			Transport: "http",
			ToolNames: []string{"web_search_exa", "web_fetch_exa"},
		},
		{
			Name:      "tavily",
			Title:     "Tavily",
			URL:       "https://mcp.tavily.com/mcp",
			Transport: "http",
			OAuth:     true,
			ToolNames: []string{"tavily_search", "tavily_extract", "tavily_crawl", "tavily_map"},
		},
	}
}

// coworkSecurityRelax is applied on every write so Cowork can actually reach the
// gateway and the managed MCP servers without per-session prompts.
func coworkSecurityRelax() map[string]any {
	return map[string]any{
		"coworkEgressAllowedHosts":            []any{"*"},
		"disabledBuiltinTools":                []any{},
		"isLocalDevMcpEnabled":                true,
		"isDesktopExtensionEnabled":           true,
		"isDesktopExtensionDirectoryEnabled":  true,
		"isDesktopExtensionSignatureRequired": false,
		"isClaudeCodeForDesktopEnabled":       true,
		"disableEssentialTelemetry":           true,
		"disableNonessentialTelemetry":        true,
		"disableNonessentialServices":         true,
	}
}

func coworkCandidateRoots() []string {
	home := homeDir()
	switch runtime.GOOS {
	case "darwin":
		base := filepath.Join(home, "Library", "Application Support")
		return []string{filepath.Join(base, "Claude-3p"), filepath.Join(base, "Claude")}
	case "windows":
		localApp := os.Getenv("LOCALAPPDATA")
		if localApp == "" {
			localApp = filepath.Join(home, "AppData", "Local")
		}
		roaming := os.Getenv("APPDATA")
		if roaming == "" {
			roaming = filepath.Join(home, "AppData", "Roaming")
		}
		return []string{
			filepath.Join(localApp, "Claude-3p"),
			filepath.Join(roaming, "Claude-3p"),
			filepath.Join(localApp, "Claude"),
			filepath.Join(roaming, "Claude"),
		}
	default:
		return []string{
			filepath.Join(home, ".config", "Claude-3p"),
			filepath.Join(home, ".config", "Claude"),
		}
	}
}

func coworkAppInstallPaths() []string {
	home := homeDir()
	switch runtime.GOOS {
	case "darwin":
		return []string{"/Applications/Claude.app", filepath.Join(home, "Applications", "Claude.app")}
	case "windows":
		localApp := os.Getenv("LOCALAPPDATA")
		if localApp == "" {
			localApp = filepath.Join(home, "AppData", "Local")
		}
		programFiles := os.Getenv("ProgramFiles")
		if programFiles == "" {
			programFiles = `C:\Program Files`
		}
		return []string{
			filepath.Join(localApp, "AnthropicClaude"),
			filepath.Join(programFiles, "Claude"),
			filepath.Join(programFiles, "AnthropicClaude"),
		}
	default:
		return nil
	}
}

// coworkReadRoot prefers a root that already has a configLibrary; falls back to the
// first candidate so a fresh install still gets a deterministic write target.
func coworkReadRoot() string {
	candidates := coworkCandidateRoots()
	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "configLibrary")); err == nil {
			return dir
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}

func coworkWriteRoot() string {
	if roots := coworkCandidateRoots(); len(roots) > 0 {
		return roots[0]
	}
	return ""
}

func coworkConfigDir() string      { return filepath.Join(coworkReadRoot(), "configLibrary") }
func coworkWriteConfigDir() string { return filepath.Join(coworkWriteRoot(), "configLibrary") }
func coworkMetaPath() string       { return filepath.Join(coworkConfigDir(), "_meta.json") }
func coworkWriteMetaPath() string  { return filepath.Join(coworkWriteConfigDir(), "_meta.json") }

// cowork1pConfigPath is the classic (non-Cowork) Claude Desktop config — the only
// place the deploymentMode switch lives.
func cowork1pConfigPath() string {
	home := homeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		roaming := os.Getenv("APPDATA")
		if roaming == "" {
			roaming = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(roaming, "Claude", "claude_desktop_config.json")
	default:
		return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
	}
}

func coworkConfigPath() string {
	meta, err := readJSONFile(coworkMetaPath())
	if err != nil {
		return coworkMetaPath()
	}
	if id := asString(meta["appliedId"]); id != "" {
		return filepath.Join(coworkConfigDir(), id+".json")
	}
	return coworkMetaPath()
}

func coworkInstalled() bool {
	for _, dir := range append(coworkCandidateRoots(), coworkAppInstallPaths()...) {
		if _, err := os.Stat(dir); err == nil {
			return true
		}
	}
	return false
}

func coworkStatus() (StatusResult, error) {
	path := coworkConfigPath()
	if !coworkInstalled() {
		return StatusResult{
			Installed:  false,
			Message:    "Claude Desktop (Cowork mode) not detected",
			ConfigPath: path,
			Plugins:    DefaultCoworkPlugins(),
		}, nil
	}
	config, err := readJSONFile(path)
	if err != nil {
		return StatusResult{}, err
	}
	endpoint := asString(config["inferenceGatewayBaseUrl"])
	configured := asString(config["inferenceProvider"]) == coworkProvider && endpoint != ""

	models := make([]string, 0)
	for _, entry := range asSlice(config["inferenceModels"]) {
		if name := asString(asMap(entry)["name"]); name != "" {
			models = append(models, name)
		}
	}
	active := make([]string, 0)
	for _, entry := range asSlice(config["managedMcpServers"]) {
		if name := asString(asMap(entry)["name"]); name != "" {
			active = append(active, name)
		}
	}
	model := ""
	if len(models) > 0 {
		model = models[0]
	}
	return StatusResult{
		Installed:     true,
		HasTproxy:     configured,
		Has9Router:    configured,
		SettingsPath:  path,
		ConfigPath:    path,
		Endpoint:      endpoint,
		Model:         model,
		Models:        models,
		Plugins:       DefaultCoworkPlugins(),
		ActivePlugins: active,
	}, nil
}

func coworkApply(req ApplyRequest) error {
	models := modelsFromRequest(req)
	if req.BaseURL == "" || req.APIKey == "" {
		return fmt.Errorf("baseUrl and apiKey are required")
	}
	if len(models) == 0 {
		return fmt.Errorf("at least one model is required")
	}

	if err := coworkBootstrapDeploymentMode(); err != nil {
		return err
	}
	meta, err := coworkEnsureMeta()
	if err != nil {
		return err
	}
	appliedID := asString(meta["appliedId"])
	if appliedID == "" {
		return fmt.Errorf("could not resolve Cowork config id")
	}

	// A nil Plugins means "caller did not choose" → use defaults. An explicitly
	// empty slice means "user turned everything off" and must be honoured.
	plugins := req.Plugins
	if plugins == nil {
		plugins = DefaultCoworkPlugins()
	}
	managed := buildManagedMcpServers(append(append([]CoworkPlugin{}, plugins...), coworkCustomOnly(req.CustomPlugins)...))

	modelEntries := make([]any, 0, len(models))
	for _, model := range models {
		modelEntries = append(modelEntries, map[string]any{"name": model})
	}

	config := coworkSecurityRelax()
	config["inferenceProvider"] = coworkProvider
	config["inferenceGatewayBaseUrl"] = normalizeBaseURL(req.BaseURL, true)
	config["inferenceGatewayApiKey"] = req.APIKey
	config["inferenceModels"] = modelEntries
	if len(managed) > 0 {
		config["managedMcpServers"] = managed
	}

	configPath := filepath.Join(coworkWriteConfigDir(), appliedID+".json")
	if err := writeJSONFile(configPath, config); err != nil {
		return err
	}
	return coworkWriteSkipApprovals(managed)
}

func coworkReset() error {
	meta, err := readJSONFile(coworkMetaPath())
	if err != nil {
		return err
	}
	appliedID := asString(meta["appliedId"])
	if appliedID == "" {
		return nil
	}
	configPath := filepath.Join(coworkConfigDir(), appliedID+".json")
	if _, statErr := os.Stat(configPath); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil
		}
		return statErr
	}
	if err := writeJSONFile(configPath, map[string]any{}); err != nil {
		return err
	}
	return coworkWriteSkipApprovals(nil)
}

// coworkBootstrapDeploymentMode flips Claude Desktop into 3p mode. Without it the
// app keeps using Anthropic's own inference and ignores the gateway config.
func coworkBootstrapDeploymentMode() error {
	path := cowork1pConfigPath()
	config, err := readJSONFile(path)
	if err != nil {
		return err
	}
	if asString(config["deploymentMode"]) == "3p" {
		return nil
	}
	config["deploymentMode"] = "3p"
	return writeJSONFile(path, config)
}

func coworkEnsureMeta() (map[string]any, error) {
	writePath := coworkWriteMetaPath()
	meta, err := readJSONFile(writePath)
	if err != nil {
		return nil, err
	}
	if asString(meta["appliedId"]) != "" {
		return meta, nil
	}
	// Reuse the id Claude already applied if we are writing to a different root.
	if existing, readErr := readJSONFile(coworkMetaPath()); readErr == nil && asString(existing["appliedId"]) != "" {
		meta = existing
	} else {
		id, genErr := randomID()
		if genErr != nil {
			return nil, genErr
		}
		meta = map[string]any{
			"appliedId": id,
			"entries":   []any{map[string]any{"id": id, "name": "Default"}},
		}
	}
	if err := writeJSONFile(writePath, meta); err != nil {
		return nil, err
	}
	return meta, nil
}

// coworkWriteSkipApprovals auto-allows every managed server so Cowork does not
// prompt per tool call.
func coworkWriteSkipApprovals(servers []any) error {
	path := filepath.Join(coworkWriteRoot(), "config.json")
	config, err := readJSONFile(path)
	if err != nil {
		return err
	}
	skip := map[string]any{}
	for _, item := range servers {
		if name := asString(asMap(item)["name"]); name != "" {
			skip[name] = true
		}
	}
	config["operonSkipMcpApprovals"] = skip
	return writeJSONFile(path, config)
}

func coworkCustomOnly(plugins []CoworkPlugin) []CoworkPlugin {
	out := make([]CoworkPlugin, 0, len(plugins))
	for _, plugin := range plugins {
		if strings.TrimSpace(plugin.URL) == "" {
			continue
		}
		plugin.Custom = true
		out = append(out, plugin)
	}
	return out
}

// buildManagedMcpServers mirrors 9router's helper: dedupe by name, infer transport
// from the URL, and emit both bare and "{name}-" prefixed tool policy keys because
// Cowork names tools differently depending on the server.
func buildManagedMcpServers(plugins []CoworkPlugin) []any {
	out := make([]any, 0, len(plugins))
	seen := map[string]bool{}
	for _, plugin := range plugins {
		name := strings.TrimSpace(plugin.Name)
		url := strings.TrimSpace(plugin.URL)
		if name == "" || url == "" || seen[name] {
			continue
		}
		seen[name] = true

		transport := plugin.Transport
		if transport == "" {
			if strings.Contains(strings.ToLower(url), "/sse") {
				transport = "sse"
			} else {
				transport = "http"
			}
		}
		entry := map[string]any{"name": name, "url": url, "transport": transport}
		if plugin.OAuth {
			entry["oauth"] = true
		}
		if plugin.Custom {
			entry["custom"] = true
		}
		if len(plugin.ToolNames) > 0 {
			prefix := name + "-"
			policy := map[string]any{}
			for _, raw := range plugin.ToolNames {
				tool := raw
				for strings.HasPrefix(tool, prefix) {
					tool = strings.TrimPrefix(tool, prefix)
				}
				if tool == "" {
					continue
				}
				policy[tool] = "allow"
				policy[prefix+tool] = "allow"
			}
			if len(policy) > 0 {
				entry["toolPolicy"] = policy
			}
		}
		out = append(out, entry)
	}
	return out
}

func randomID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	// UUIDv4 shape — Claude only treats this as an opaque file name.
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	hexed := hex.EncodeToString(buf)
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexed[0:8], hexed[8:12], hexed[12:16], hexed[16:20], hexed[20:32]), nil
}
