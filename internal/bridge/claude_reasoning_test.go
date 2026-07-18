package bridge

import "testing"

func TestNormalizeReasoningEffort(t *testing.T) {
	cases := map[string]string{
		"":         "",
		"default":  "",
		"inherit":  "",
		" HIGH ":   "high",
		"none":     "none",
		"minimal":  "minimal",
		"xhigh":    "xhigh",
		"max":      "max",
		"ultra":    "",
		"invalid":  "",
	}
	for input, want := range cases {
		if got := NormalizeReasoningEffort(input); got != want {
			t.Fatalf("NormalizeReasoningEffort(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestReasoningEffortForClientModel(t *testing.T) {
	resolver := NewResolver(Config{})
	resolver.SetReasoningEffortOverrides(ReasoningEffortOverrides{
		RoleFable:  "high",
		RoleSonnet: "medium",
	})
	if got := resolver.ReasoningEffortForClientModel("fable"); got != "high" {
		t.Fatalf("fable effort = %q", got)
	}
	if got := resolver.ReasoningEffortForClientModel("claude-sonnet"); got != "medium" {
		t.Fatalf("sonnet effort = %q", got)
	}
	if got := resolver.ReasoningEffortForClientModel("claude-opus-4.7"); got != "" {
		t.Fatalf("real model effort = %q", got)
	}
}

func TestCodexWireReasoningEffort(t *testing.T) {
	if got := CodexWireReasoningEffort("max"); got != "max" {
		t.Fatalf("max wire = %q", got)
	}
}

func TestReasoningEffortOptionsForTarget(t *testing.T) {
	sol := ReasoningEffortOptionsForTarget("codex-gpt-5.6-sol")
	if !containsString(sol, "max") {
		t.Fatalf("sol options = %#v", sol)
	}
	if containsString(sol, "ultra") {
		t.Fatalf("ultra should not be offered: %#v", sol)
	}
	luna := ReasoningEffortOptionsForTarget("gpt-5.6-luna")
	if !containsString(luna, "max") {
		t.Fatalf("luna options = %#v", luna)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
