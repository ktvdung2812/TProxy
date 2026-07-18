package bridge

import "testing"

func TestClassifyAliasTargetGPT(t *testing.T) {
	target, ok := ClassifyAliasTarget("codex:gpt-5.6-sol")
	if !ok || target.Kind != TargetKindChatGPT || target.Model != "gpt-5.6-sol" {
		t.Fatalf("target = %+v ok=%v", target, ok)
	}
	target, ok = ClassifyAliasTarget("cx/gpt-5.6-terra")
	if !ok || target.Kind != TargetKindChatGPT || target.Model != "gpt-5.6-terra" {
		t.Fatalf("slash target = %+v ok=%v", target, ok)
	}
}

func TestClassifyAliasTargetVirtualModel(t *testing.T) {
	target, ok := ClassifyAliasTarget("mapping-fable")
	if !ok || target.Kind != TargetKindVirtual || target.Model != "mapping-fable" {
		t.Fatalf("target = %+v ok=%v", target, ok)
	}
}

func TestResolveModelVirtualModelFromEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_DEFAULT_FABLE_MODEL", "mapping-fable")
	t.Setenv("ANTHROPIC_DEFAULT_OPUS_MODEL", "mapping-opus")
	resolver := NewResolver(Config{})
	resolver.SetEnvOverrides(EnvOverrides())
	if got := resolver.ResolveModel("fable"); got != "mapping-fable" {
		t.Fatalf("fable resolved = %q", got)
	}
	if got := resolver.ResolveModel("opus"); got != "mapping-opus" {
		t.Fatalf("opus resolved = %q", got)
	}
}

func TestClassifyAliasTargetClaudeNative(t *testing.T) {
	target, ok := ClassifyAliasTarget("claude-opus-4.7")
	if !ok || target.Kind != TargetKindClaude || target.Model != "claude-opus-4.7" {
		t.Fatalf("target = %+v ok=%v", target, ok)
	}
	if _, ok := ClassifyAliasTarget("claude-opus"); ok {
		t.Fatal("placeholder should not classify as target")
	}
}

func TestResolveModelClaudeNativeSubstitution(t *testing.T) {
	resolver := NewResolver(Config{})
	resolver.SetOverrides(Overrides{RoleSonnet: "claude-sonnet-4.6"})
	if got := resolver.ResolveModel("sonnet"); got != "claude-sonnet-4.6" {
		t.Fatalf("resolved = %q", got)
	}
}

func TestIsClaudePlaceholderAnthropicPrefix(t *testing.T) {
	if !IsClaudePlaceholder("anthropic/claude-sonnet") {
		t.Fatal("expected anthropic/claude-sonnet placeholder")
	}
	if IsClaudePlaceholder("claude-sonnet-4.6") {
		t.Fatal("real model should not be placeholder")
	}
}

func TestResolveModelDisplaySuffix(t *testing.T) {
	resolver := NewResolver(Config{DefaultCodexProvider: "codex"})
	if got := resolver.ResolveModel("claude-haiku[1m]"); got != "codex:gpt-5.1-codex-mini" {
		t.Fatalf("resolved = %q", got)
	}
}
