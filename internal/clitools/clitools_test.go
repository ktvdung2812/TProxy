package clitools

import (
	"os"
	"path/filepath"
	"strings"
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

func TestGrokApplyUpsertsAndResetRestoresDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	initial := `[models]
default = "grok-build"

[model.other]
model = "grok-4"
`
	if err := writeFile(grokConfigPath(), []byte(initial)); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	req := ApplyRequest{
		BaseURL: "http://127.0.0.1:28120",
		APIKey:  "tp_test_key",
		Model:   "codex-gpt-5.5",
	}
	if err := grokApply(req); err != nil {
		t.Fatalf("grokApply: %v", err)
	}

	raw, err := readFile(grokConfigPath())
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, `default = "tproxy"`) {
		t.Fatalf("expected tproxy default, got:\n%s", content)
	}
	if !strings.Contains(content, `api_backend = "chat_completions"`) {
		t.Fatalf("expected chat_completions backend, got:\n%s", content)
	}
	if !strings.Contains(content, `description = "Routed via TProxy gateway"`) {
		t.Fatalf("expected description, got:\n%s", content)
	}
	if !strings.Contains(content, `[model.other]`) {
		t.Fatalf("expected other model section preserved, got:\n%s", content)
	}
	if !strings.Contains(content, `# tproxy-prev-default = "grok-build"`) {
		t.Fatalf("expected prev-default marker, got:\n%s", content)
	}

	if err := grokReset(); err != nil {
		t.Fatalf("grokReset: %v", err)
	}
	raw, err = readFile(grokConfigPath())
	if err != nil {
		t.Fatalf("read config after reset: %v", err)
	}
	content = string(raw)
	if strings.Contains(content, "[model.tproxy]") {
		t.Fatalf("expected tproxy section removed, got:\n%s", content)
	}
	if !strings.Contains(content, `default = "grok-build"`) {
		t.Fatalf("expected previous default restored, got:\n%s", content)
	}
	if !strings.Contains(content, `[model.other]`) {
		t.Fatalf("expected other model section preserved after reset, got:\n%s", content)
	}
}
