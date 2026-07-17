package pricing

import (
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

func TestNormalizeModelID(t *testing.T) {
	if got := normalizeModelID("codex:gpt-5.6-luna"); got != "gpt-5.6-luna" {
		t.Fatalf("normalizeModelID() = %q", got)
	}
	if got := normalizeModelID("openai/gpt-5.4"); got != "gpt-5.4" {
		t.Fatalf("normalizeModelID() = %q", got)
	}
}

func TestBuildIndexLookupOpenAI(t *testing.T) {
	payload := map[string]any{
		"openai": map[string]any{
			"id": "openai",
			"models": map[string]any{
				"gpt-5.6-luna": map[string]any{
					"id": "gpt-5.6-luna",
					"cost": map[string]any{
						"input":      1.0,
						"output":     6.0,
						"cache_read": 0.1,
					},
				},
			},
		},
	}
	catalog := &Catalog{byModel: buildIndex(payload)}
	rates, ok := catalog.Lookup(store.Provider{Type: "openai-codex", ID: "codex-main"}, "codex:gpt-5.6-luna")
	if !ok {
		t.Fatal("expected pricing lookup to succeed")
	}
	if rates.InputPerMillion != 1 || rates.OutputPerMillion != 6 || rates.CacheReadPerMillion != 0.1 {
		t.Fatalf("rates = %+v", rates)
	}
}

func TestEstimateCostUsesCacheReadRate(t *testing.T) {
	cost := EstimateCost(canonical.Usage{
		InputTokens:  1000,
		OutputTokens: 500,
		CachedTokens: 400,
	}, Rates{
		InputPerMillion:     1,
		OutputPerMillion:    6,
		CacheReadPerMillion: 0.1,
	})
	// 600 input billable * 1/1M + 400 cached * 0.1/1M + 500 output * 6/1M
	want := 0.0006 + 0.00004 + 0.003
	if cost < want-1e-9 || cost > want+1e-9 {
		t.Fatalf("cost = %f want %f", cost, want)
	}
}
