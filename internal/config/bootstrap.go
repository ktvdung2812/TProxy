package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed default.yaml
var defaultConfigYAML []byte

const defaultConfigFlagValue = "config.yaml"

// ResolveConfigPath picks the config file to load. When the default flag value is
// used and ./config.yaml is missing, a clean config is bootstrapped under ~/.tproxy/.
func ResolveConfigPath(requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		requested = defaultConfigFlagValue
	}
	if requested != defaultConfigFlagValue {
		return requested, nil
	}
	if _, err := os.Stat(requested); err == nil {
		return requested, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	userPath := filepath.Join(home, ".tproxy", "config.yaml")
	if err := BootstrapConfig(userPath); err != nil {
		return "", err
	}
	return userPath, nil
}

// BootstrapConfig writes the embedded empty starter config when the target is missing.
func BootstrapConfig(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("config path is required")
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat config %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.WriteFile(path, defaultConfigYAML, 0o644); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}
