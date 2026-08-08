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

func TestEndpointLooksLikeProxyAcceptsLANAndTunnel(t *testing.T) {
	cases := map[string]bool{
		"http://localhost:28120/v1":            true,
		"http://127.0.0.1:28120/v1":            true,
		"http://192.168.1.50:28120/v1":         true,
		"http://10.0.0.4:28120/v1":             true,
		"http://172.20.1.9:28120/v1":           true,
		"https://foo-bar.trycloudflare.com/v1": true,
		"https://box.tail1234.ts.net/v1":       true,
		"https://api.anthropic.com/v1":         false,
		"https://api.openai.com/v1":            false,
		"":                                     false,
	}
	for endpoint, want := range cases {
		if got := endpointLooksLikeProxy(endpoint); got != want {
			t.Errorf("endpointLooksLikeProxy(%q) = %v, want %v", endpoint, got, want)
		}
	}
}

func TestClaudeApplyEnvOverridesTierSlots(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	req := ApplyRequest{
		BaseURL:       "http://127.0.0.1:28120",
		APIKey:        "test-key",
		Model:         "sonnet",
		SubagentModel: "haiku",
		Env:           map[string]string{"ANTHROPIC_DEFAULT_SONNET_MODEL": "my-virtual-model"},
	}
	if err := claudeApply(req); err != nil {
		t.Fatalf("claudeApply: %v", err)
	}
	settings, err := readJSONFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("readJSONFile: %v", err)
	}
	env := asMap(settings["env"])
	if got := asString(env["ANTHROPIC_DEFAULT_SONNET_MODEL"]); got != "my-virtual-model" {
		t.Errorf("env override lost: got %q", got)
	}
	if got := asString(env["CLAUDE_CODE_SUBAGENT_MODEL"]); got != "haiku" {
		t.Errorf("subagent model = %q, want haiku", got)
	}
	if got := asString(env["ANTHROPIC_MODEL"]); got != "sonnet" {
		t.Errorf("primary model = %q, want sonnet", got)
	}
}

func TestOpencodeApplyPreservesExistingModels(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	first := ApplyRequest{BaseURL: "http://127.0.0.1:28120", APIKey: "k", Models: []string{"model-a"}}
	if err := opencodeApply(first); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	second := ApplyRequest{BaseURL: "http://127.0.0.1:28120", APIKey: "k", Models: []string{"model-b"}, SubagentModel: "model-a"}
	if err := opencodeApply(second); err != nil {
		t.Fatalf("second apply: %v", err)
	}

	config, err := readJSONFile(filepath.Join(dir, ".config", "opencode", "opencode.json"))
	if err != nil {
		t.Fatalf("readJSONFile: %v", err)
	}
	models := asMap(asMap(asMap(config["provider"])[providerKey])["models"])
	if _, ok := models["model-a"]; !ok {
		t.Errorf("model-a dropped on re-apply: %v", models)
	}
	if _, ok := models["model-b"]; !ok {
		t.Errorf("model-b missing: %v", models)
	}
	explorer := asString(asMap(asMap(config["agent"])["explorer"])["model"])
	if explorer != providerKey+"/model-a" {
		t.Errorf("subagent model = %q", explorer)
	}
}

func TestBuildManagedMcpServersDedupesAndPrefixesPolicy(t *testing.T) {
	out := buildManagedMcpServers([]CoworkPlugin{
		{Name: "exa", URL: "https://mcp.exa.ai/mcp", ToolNames: []string{"web_search_exa", "exa-web_search_exa"}},
		{Name: "exa", URL: "https://duplicate.example/mcp"},
		{Name: "", URL: "https://no-name.example/mcp"},
	})
	if len(out) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(out))
	}
	entry := asMap(out[0])
	if asString(entry["transport"]) != "http" {
		t.Errorf("transport = %q, want http", asString(entry["transport"]))
	}
	policy := asMap(entry["toolPolicy"])
	if len(policy) != 2 {
		t.Errorf("expected bare + prefixed keys, got %v", policy)
	}
	if asString(policy["web_search_exa"]) != "allow" || asString(policy["exa-web_search_exa"]) != "allow" {
		t.Errorf("policy keys wrong: %v", policy)
	}
}
