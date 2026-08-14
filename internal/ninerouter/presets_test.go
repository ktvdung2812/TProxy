package ninerouter

import "testing"

func TestLookupAliases(t *testing.T) {
	cases := map[string]string{
		"cx":   "codex",
		"gh":   "github",
		"gcli": "grok-cli",
		"glm":  "glm",
	}
	for alias, wantID := range cases {
		preset, ok := Lookup(alias)
		if !ok {
			t.Fatalf("Lookup(%q) = false", alias)
		}
		if preset.ID != wantID {
			t.Fatalf("Lookup(%q).ID = %q, want %q", alias, preset.ID, wantID)
		}
	}
}

func TestPresetCount(t *testing.T) {
	if len(AllPresets()) < 110 {
		t.Fatalf("expected at least 110 presets, got %d", len(AllPresets()))
	}
}

func TestWave2PresetLookup(t *testing.T) {
	preset, ok := Lookup("groq")
	if !ok || preset.ID != "groq" {
		t.Fatalf("wave-2 groq preset missing: %+v", preset)
	}
}

func TestGrokCliMapsToXAI(t *testing.T) {
	preset, ok := Lookup("grok-cli")
	if !ok {
		t.Fatal("grok-cli preset missing")
	}
	if preset.Type != "xai" {
		t.Fatalf("grok-cli type = %q, want xai", preset.Type)
	}
}

func TestGeminiCLIMapsToAntigravity(t *testing.T) {
	preset, ok := Lookup("gemini-cli")
	if !ok {
		t.Fatal("gemini-cli preset missing")
	}
	if preset.Type != "antigravity" {
		t.Fatalf("gemini-cli type = %q, want antigravity", preset.Type)
	}
}
