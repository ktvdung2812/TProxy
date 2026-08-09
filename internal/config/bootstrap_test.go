package config

import (
	"os"
	"path/filepath"
	"runtime"
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
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("config mode = %v, want 0600", info.Mode().Perm())
		}
		info, err = os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("config directory mode = %v, want 0700", info.Mode().Perm())
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := BootstrapConfig(path); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("existing config mode = %v, want repaired 0600", info.Mode().Perm())
		}
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
