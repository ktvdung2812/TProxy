package api

import (
	"net/http/httptest"
	"testing"
)

func TestClassifyProxyIngress(t *testing.T) {
	tests := []struct {
		path     string
		wantPath string
		provider string
		gemini   string
		ok       bool
	}{
		{path: "/v1/chat/completions", wantPath: "/v1/chat/completions", ok: true},
		{path: "/openai/v1/chat/completions", wantPath: "/v1/chat/completions", provider: "openai", ok: true},
		{path: "/anthropic/v1/messages", wantPath: "/v1/messages", provider: "anthropic", ok: true},
		{path: "/custom-provider/v1/models", wantPath: "/v1/models", provider: "custom-provider", ok: true},
		{path: "/gemini/v1beta/models/gemini-pro:generateContent", wantPath: "/v1beta/models/gemini-pro:generateContent", provider: "gemini", ok: true},
		{path: "/v1internal:generateContent", wantPath: "/v1internal:generateContent", provider: "gemini-cli", gemini: "generateContent", ok: true},
		{path: "/v1internal:unsupported", ok: false},
		{path: "/api/admin/snapshot", ok: false},
		{path: "/openaievil", ok: false},
	}

	for _, test := range tests {
		route, ok := classifyProxyIngress(test.path)
		if ok != test.ok {
			t.Fatalf("classifyProxyIngress(%q) ok=%v want %v", test.path, ok, test.ok)
		}
		if !test.ok {
			continue
		}
		if route.CanonicalPath != test.wantPath {
			t.Fatalf("classifyProxyIngress(%q) canonical=%q want %q", test.path, route.CanonicalPath, test.wantPath)
		}
		if route.RouteProvider != test.provider {
			t.Fatalf("classifyProxyIngress(%q) provider=%q want %q", test.path, route.RouteProvider, test.provider)
		}
		if route.GeminiAction != test.gemini {
			t.Fatalf("classifyProxyIngress(%q) gemini=%q want %q", test.path, route.GeminiAction, test.gemini)
		}
		if test.provider != "" && !route.DisableFallback {
			t.Fatalf("classifyProxyIngress(%q) expected fallback disabled", test.path)
		}
	}
}

func TestParseModelSelector(t *testing.T) {
	if parsed := parseModelSelector("openai::gpt-4"); parsed.Provider != "openai" || parsed.Model != "gpt-4" {
		t.Fatalf("double-colon selector = %+v", parsed)
	}
	if parsed := parseModelSelector("mock:coder"); parsed.Provider != "mock" || parsed.Model != "coder" {
		t.Fatalf("single-colon selector = %+v", parsed)
	}
	if parsed := parseModelSelector("td-coder"); parsed.Provider != "" || parsed.Model != "td-coder" {
		t.Fatalf("plain model = %+v", parsed)
	}
}

func TestRejectQueryCredentials(t *testing.T) {
	request := httptest.NewRequest("GET", "/v1/models?api_key=secret", nil)
	if err := rejectQueryCredentials(request); err == nil {
		t.Fatal("expected query credential rejection")
	}
}

func TestExtractClientAPIKeyAmbiguous(t *testing.T) {
	request := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	request.Header.Set("Authorization", "Bearer one")
	request.Header.Set("X-Api-Key", "two")
	if _, err := extractClientAPIKey(request); err == nil {
		t.Fatal("expected ambiguous credential error")
	}
}
