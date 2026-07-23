package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapConfigWritesDefaultWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "config.yaml")
	if err := BootstrapConfig(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("expected default config content")
	}
	if string(data) != string(defaultConfigYAML) {
		t.Fatalf("bootstrapped config mismatch")
	}
	if err := BootstrapConfig(path); err != nil {
		t.Fatal(err)
	}
}

func TestResolveConfigPathUsesHomeWhenDefaultMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := ResolveConfigPath("config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".tproxy", "config.yaml")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("bootstrapped config missing: %v", err)
	}
}
