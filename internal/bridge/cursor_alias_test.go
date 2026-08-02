package bridge

import "testing"

func TestCursorResolverResolve(t *testing.T) {
	resolver := NewCursorResolver()
	resolver.SetAliases(CursorAliases{
		"claude-4.5-sonnet": "virtual-fast",
		"My-Model":          "provider:upstream",
	})
	if got := resolver.Resolve("claude-4.5-sonnet"); got != "virtual-fast" {
		t.Fatalf("claude-4.5-sonnet: got %q", got)
	}
	if got := resolver.Resolve("CLAUDE-4.5-SONNET"); got != "virtual-fast" {
		t.Fatalf("case-insensitive: got %q", got)
	}
	if got := resolver.Resolve("my-model"); got != "provider:upstream" {
		t.Fatalf("normalized key: got %q", got)
	}
	if got := resolver.Resolve("unknown"); got != "unknown" {
		t.Fatalf("passthrough: got %q", got)
	}
}

func TestNormalizeCursorAliasesKeepsIdentityAndTrims(t *testing.T) {
	got := NormalizeCursorAliases(map[string]string{
		"claude-4.5-sonnet": "claude-4.5-sonnet",
		"smart":             "  virtual-smart  ",
		"":                  "x",
		"x":                 "",
	})
	if got["claude-4.5-sonnet"] != "claude-4.5-sonnet" {
		t.Fatalf("identity alias should be kept for transparent same-id routing, got %#v", got)
	}
	if got["smart"] != "virtual-smart" {
		t.Fatalf("expected trimmed target, got %#v", got)
	}
	if _, ok := got[""]; ok {
		t.Fatalf("empty source should be dropped")
	}
	if _, ok := got["x"]; ok {
		t.Fatalf("empty target should be dropped")
	}
}

func TestCursorPlaceholderRowsOnlyConfigured(t *testing.T) {
	resolver := NewCursorResolver()
	resolver.SetAliases(CursorAliases{
		"claude-4.5-sonnet": "v-fast",
		"custom-a":          "v-a",
	})
	catalog := []CursorModel{
		{ID: "claude-4.5-sonnet", Name: "Claude 4.5 Sonnet"},
		{ID: "gpt-5.2", Name: "GPT 5.2"},
	}
	rows := resolver.PlaceholderRows(catalog)
	if len(rows) != 2 {
		t.Fatalf("expected only configured mappings, got %d: %v", len(rows), rows)
	}
	foundSonnet, foundCustom := false, false
	for _, row := range rows {
		if row["name"] == "claude-4.5-sonnet" && row["resolves"] == "v-fast" && row["label"] == "Claude 4.5 Sonnet" {
			foundSonnet = true
		}
		if row["name"] == "custom-a" && row["resolves"] == "v-a" {
			foundCustom = true
		}
	}
	if !foundSonnet || !foundCustom {
		t.Fatalf("missing rows: sonnet=%v custom=%v rows=%v", foundSonnet, foundCustom, rows)
	}
}
