package clitools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeBaseURL(t *testing.T) {
	if got := normalizeBaseURL("http://localhost:28120", true); got != "http://localhost:28120/v1" {
		t.Fatalf("expected /v1 suffix, got %q", got)
	}
	if got := normalizeBaseURL("http://localhost:28120/v1", true); got != "http://localhost:28120/v1" {
		t.Fatalf("expected unchanged, got %q", got)
	}
}

func TestClaudeApplyWritesSettings(t *testing.T) {
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", dir)
	defer func() {
		if origHome != "" {
			t.Setenv("HOME", origHome)
		}
	}()

	req := ApplyRequest{
		BaseURL: "http://127.0.0.1:28120",
		APIKey:  "test-key",
		Model:   "sonnet",
	}
	if err := claudeApply(req); err != nil {
		t.Fatalf("claudeApply: %v", err)
	}

	path := filepath.Join(dir, ".claude", "settings.json")
	settings, err := readJSONFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	env := asMap(settings["env"])
	if env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:28120/v1" {
		t.Fatalf("unexpected base url: %v", env["ANTHROPIC_BASE_URL"])
	}
	if env["ANTHROPIC_API_KEY"] != "test-key" {
		t.Fatalf("unexpected api key: %v", env["ANTHROPIC_API_KEY"])
	}
	if env["ANTHROPIC_MODEL"] != "sonnet" {
		t.Fatalf("unexpected primary model: %v", env["ANTHROPIC_MODEL"])
	}
	if env["ANTHROPIC_DEFAULT_SONNET_MODEL"] != "sonnet" {
		t.Fatalf("unexpected sonnet placeholder: %v", env["ANTHROPIC_DEFAULT_SONNET_MODEL"])
	}
	if env["CLAUDE_CODE_SUBAGENT_MODEL"] != "sonnet" {
		t.Fatalf("unexpected subagent model: %v", env["CLAUDE_CODE_SUBAGENT_MODEL"])
	}

	if err := claudeReset(); err != nil {
		t.Fatalf("claudeReset: %v", err)
	}
	settings, err = readJSONFile(path)
	if err != nil {
		t.Fatalf("read settings after reset: %v", err)
	}
	if _, ok := settings["env"]; ok {
		t.Fatalf("expected env removed, got %v", settings["env"])
	}
}
