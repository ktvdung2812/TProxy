package clitools

import (
	"encoding/json"
	"fmt"
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
	Env     map[string]string `json:"env"`
}

type StatusResult struct {
	Installed    bool   `json:"installed"`
	HasTproxy    bool   `json:"has_tproxy"`
	Has9Router   bool   `json:"has_9router"`
	SettingsPath string `json:"settings_path,omitempty"`
	ConfigPath   string `json:"config_path,omitempty"`
	Message      string `json:"message,omitempty"`
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

func endpointLooksLikeProxy(url string) bool {
	lower := strings.ToLower(strings.TrimSpace(url))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "localhost") || strings.Contains(lower, "127.0.0.1") || strings.Contains(lower, "0.0.0.0") {
		return true
	}
	if strings.Contains(lower, providerKey) || strings.Contains(lower, legacyProviderKey) {
		return true
	}
	return false
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
