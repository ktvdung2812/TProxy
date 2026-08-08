package clitools

import (
	"fmt"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

type toolHandler struct {
	binary     string
	configPath func() string
	status     func() (StatusResult, error)
	apply      func(ApplyRequest) error
	reset      func() error
}

var registry = map[string]toolHandler{}

func init() {
	register("claude", toolHandler{
		binary:     "claude",
		configPath: claudeSettingsPath,
		status:     claudeStatus,
		apply:      claudeApply,
		reset:      claudeReset,
	})
	register("codex", toolHandler{
		binary:     "codex",
		configPath: codexConfigPath,
		status:     codexStatus,
		apply:      codexApply,
		reset:      codexReset,
	})
	register("openclaw", toolHandler{
		binary:     "openclaw",
		configPath: openclawSettingsPath,
		status:     openclawStatus,
		apply:      openclawApply,
		reset:      openclawReset,
	})
	register("opencode", toolHandler{
		binary:     "opencode",
		configPath: opencodeConfigPath,
		status:     opencodeStatus,
		apply:      opencodeApply,
		reset:      opencodeReset,
	})
	register("cline", toolHandler{
		binary:     "cline",
		configPath: clineGlobalStatePath,
		status:     clineStatus,
		apply:      clineApply,
		reset:      clineReset,
	})
	register("droid", toolHandler{
		binary:     "droid",
		configPath: droidSettingsPath,
		status:     droidStatus,
		apply:      droidApply,
		reset:      droidReset,
	})
	register("kilo", toolHandler{
		binary:     "kilo",
		configPath: kiloAuthPath,
		status:     kiloStatus,
		apply:      kiloApply,
		reset:      kiloReset,
	})
	register("deepseek-tui", toolHandler{
		binary:     "deepseek",
		configPath: deepseekConfigPath,
		status:     deepseekStatus,
		apply:      deepseekApply,
		reset:      deepseekReset,
	})
	register("copilot", toolHandler{
		binary:     "",
		configPath: copilotConfigPath,
		status:     copilotStatus,
		apply:      copilotApply,
		reset:      copilotReset,
	})
	register("hermes", toolHandler{
		binary:     "hermes",
		configPath: hermesConfigPath,
		status:     hermesStatus,
		apply:      hermesApply,
		reset:      hermesReset,
	})
	register("grok-build", toolHandler{
		binary:     "grok",
		configPath: grokConfigPath,
		status:     grokStatus,
		apply:      grokApply,
		reset:      grokReset,
	})
	register("cowork", toolHandler{
		binary:     "",
		configPath: coworkConfigPath,
		status:     coworkStatus,
		apply:      coworkApply,
		reset:      coworkReset,
	})
	register("jcode", toolHandler{
		binary:     "jcode",
		configPath: jcodeConfigPath,
		status:     jcodeStatus,
		apply:      jcodeApply,
		reset:      jcodeReset,
	})
}

func register(id string, handler toolHandler) {
	registry[id] = handler
}

func ToolIDs() []string {
	ids := make([]string, 0, len(registry))
	for id := range registry {
		ids = append(ids, id)
	}
	return ids
}

func Status(toolID string) (StatusResult, error) {
	handler, ok := registry[toolID]
	if !ok {
		return StatusResult{}, fmt.Errorf("unknown cli tool %q", toolID)
	}
	return handler.status()
}

func AllStatuses() map[string]StatusResult {
	out := make(map[string]StatusResult, len(registry))
	for id, handler := range registry {
		result, err := handler.status()
		if err != nil {
			out[id] = StatusResult{Installed: false, Message: err.Error()}
			continue
		}
		out[id] = result
	}
	return out
}

func Apply(toolID string, req ApplyRequest) error {
	handler, ok := registry[toolID]
	if !ok {
		return fmt.Errorf("unknown cli tool %q", toolID)
	}
	return handler.apply(req)
}

func Reset(toolID string) error {
	handler, ok := registry[toolID]
	if !ok {
		return fmt.Errorf("unknown cli tool %q", toolID)
	}
	return handler.reset()
}

func setNestedMap(root map[string]any, dotted string, value map[string]any) {
	parts := strings.Split(dotted, ".")
	current := root
	for i := 0; i < len(parts)-1; i++ {
		next := asMap(current[parts[i]])
		current[parts[i]] = next
		current = next
	}
	current[parts[len(parts)-1]] = value
}

func deleteNestedMap(root map[string]any, dotted string) {
	parts := strings.Split(dotted, ".")
	current := root
	for i := 0; i < len(parts)-1; i++ {
		next, ok := current[parts[i]].(map[string]any)
		if !ok || next == nil {
			return
		}
		current = next
	}
	delete(current, parts[len(parts)-1])
}

func mergeTomlMap(existing string, updates map[string]any) (string, error) {
	parsed := map[string]any{}
	if strings.TrimSpace(existing) != "" {
		if err := toml.Unmarshal([]byte(existing), &parsed); err != nil {
			return "", err
		}
	}
	mergeMaps(parsed, updates)
	raw, err := toml.Marshal(parsed)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func mergeMaps(dst map[string]any, src map[string]any) {
	for key, value := range src {
		if child, ok := value.(map[string]any); ok {
			existing := asMap(dst[key])
			mergeMaps(existing, child)
			dst[key] = existing
			continue
		}
		dst[key] = value
	}
}

func statusFromInstalled(binary string, configPath string, configured bool) StatusResult {
	return StatusResult{
		Installed:    installed(binary, configPath),
		HasTproxy:    configured,
		Has9Router:   configured,
		SettingsPath: configPath,
		ConfigPath:   configPath,
	}
}

// with attaches the endpoint/model the tool is currently pointed at. The dashboard
// compares Endpoint against the base URLs it knows about (local, LAN, tunnel,
// Tailscale) to distinguish "connected to us" from "connected elsewhere".
func (s StatusResult) with(endpoint, model string) StatusResult {
	s.Endpoint = strings.TrimSpace(endpoint)
	s.Model = strings.TrimSpace(model)
	return s
}
