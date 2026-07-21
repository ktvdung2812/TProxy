package bridge

import "testing"

func TestResolvePlaceholderToCodexTarget(t *testing.T) {
	resolver := NewResolver(Config{DefaultCodexProvider: "openai-codex"})
	got := resolver.ResolveModel("claude-sonnet")
	if got != "openai-codex:gpt-5.4" {
		t.Fatalf("resolved = %q", got)
	}
}

func TestResolveFablePlaceholder(t *testing.T) {
	resolver := NewResolver(Config{DefaultCodexProvider: "codex"})
	got := resolver.ResolveModel("fable")
	if got != "codex:gpt-5.6-luna" {
		t.Fatalf("resolved = %q", got)
	}
	if got := resolver.ResolveModel("claude-fable"); got != "codex:gpt-5.6-luna" {
		t.Fatalf("claude-fable resolved = %q", got)
	}
}

func TestResolvePlaceholderUsesOverride(t *testing.T) {
	resolver := NewResolver(Config{})
	resolver.SetOverrides(Overrides{RoleSonnet: "td-coder"})
	if got := resolver.ResolveModel("sonnet"); got != "td-coder" {
		t.Fatalf("resolved = %q", got)
	}
}

func TestResolveRealModelPassthrough(t *testing.T) {
	resolver := NewResolver(Config{})
	if got := resolver.ResolveModel("claude-opus-4-7"); got != "claude-opus-4-7" {
		t.Fatalf("resolved = %q", got)
	}
}

func TestResolveGptCodenamePlaceholders(t *testing.T) {
	resolver := NewResolver(Config{})
	resolver.SetOverrides(Overrides{
		RoleFable: "codex-gpt-5.6-sol",
		RoleOpus:  "codex-gpt-5.6-terra",
		RoleHaiku: "codex-gpt-5.6-luna",
	})
	cases := map[string]string{
		"gpt-sol":   "codex-gpt-5.6-sol",
		"gpt-terra": "codex-gpt-5.6-terra",
		"gpt-luna":  "codex-gpt-5.6-luna",
	}
	for placeholder, want := range cases {
		if got := resolver.ResolveModel(placeholder); got != want {
			t.Fatalf("%s resolved = %q, want %q", placeholder, got, want)
		}
	}
}

func TestFormatTargetProviderSelector(t *testing.T) {
	if got := FormatTarget("codex::gpt-5.6-luna", ""); got != "codex:gpt-5.6-luna" {
		t.Fatalf("formatted = %q", got)
	}
}
