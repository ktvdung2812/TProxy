package providers

import (
	"testing"

	"github.com/tproxy/tproxy/internal/ninerouter"
	"github.com/tproxy/tproxy/internal/store"
)

func TestOpenAIResourceURL(t *testing.T) {
	cases := []struct {
		name     string
		base     string
		resource string
		want     string
	}{
		{name: "glm paas v4 models", base: "https://api.z.ai/api/paas/v4", resource: "/models", want: "https://api.z.ai/api/paas/v4/models"},
		{name: "glm paas v4 chat", base: "https://api.z.ai/api/paas/v4", resource: "/chat/completions", want: "https://api.z.ai/api/paas/v4/chat/completions"},
		{name: "glm cn coding", base: "https://open.bigmodel.cn/api/coding/paas/v4", resource: "/chat/completions", want: "https://open.bigmodel.cn/api/coding/paas/v4/chat/completions"},
		{name: "openai host with v1 suffix", base: "https://api.openai.com/v1", resource: "/chat/completions", want: "https://api.openai.com/v1/chat/completions"},
		{name: "openai host root", base: "https://api.openai.com", resource: "/chat/completions", want: "https://api.openai.com/v1/chat/completions"},
		{name: "deepseek host root", base: "https://api.deepseek.com", resource: "/chat/completions", want: "https://api.deepseek.com/chat/completions"},
		{name: "deepseek host root models", base: "https://api.deepseek.com", resource: "/models", want: "https://api.deepseek.com/models"},
		{name: "perplexity host root", base: "https://api.perplexity.ai", resource: "/chat/completions", want: "https://api.perplexity.ai/chat/completions"},
		{name: "perplexity v1 base", base: "https://api.perplexity.ai/v1", resource: "/chat/completions", want: "https://api.perplexity.ai/v1/chat/completions"},
		{name: "byteplus coding v3", base: "https://ark.ap-southeast.bytepluses.com/api/coding/v3", resource: "/chat/completions", want: "https://ark.ap-southeast.bytepluses.com/api/coding/v3/chat/completions"},
		{name: "volcengine coding v3", base: "https://ark.cn-beijing.volces.com/api/coding/v3", resource: "/models", want: "https://ark.cn-beijing.volces.com/api/coding/v3/models"},
		{name: "groq openai v1", base: "https://api.groq.com/openai/v1", resource: "/chat/completions", want: "https://api.groq.com/openai/v1/chat/completions"},
		{name: "codebuddy v2", base: "https://copilot.tencent.com/v2", resource: "/chat/completions", want: "https://copilot.tencent.com/v2/chat/completions"},
		{name: "kimi coding v1", base: "https://api.kimi.com/coding/v1", resource: "/chat/completions", want: "https://api.kimi.com/coding/v1/chat/completions"},
		{name: "ollama cloud chat", base: "https://ollama.com/api/chat", resource: "/chat/completions", want: "https://ollama.com/api/chat"},
		{name: "ollama local chat", base: "http://localhost:11434/api/chat", resource: "/chat/completions", want: "http://localhost:11434/api/chat"},
		{name: "commandcode terminal generate", base: "https://api.commandcode.ai/alpha/generate", resource: "/chat/completions", want: "https://api.commandcode.ai/alpha/generate"},
		{name: "perplexity web terminal", base: "https://www.perplexity.ai/rest/sse/perplexity_ask", resource: "/chat/completions", want: "https://www.perplexity.ai/rest/sse/perplexity_ask"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := openAIResourceURL(tc.base, tc.resource); got != tc.want {
				t.Fatalf("openAIResourceURL(%q, %q) = %q, want %q", tc.base, tc.resource, got, tc.want)
			}
		})
	}
}

func TestEndpointOpenAICompatiblePaths(t *testing.T) {
	cases := []struct {
		base, path, want string
	}{
		{"https://api.z.ai/api/paas/v4", "/v1/models", "https://api.z.ai/api/paas/v4/models"},
		{"https://api.z.ai/api/paas/v4/", "/v1/chat/completions", "https://api.z.ai/api/paas/v4/chat/completions"},
		{"https://api.openai.com/v1", "/v1/models", "https://api.openai.com/v1/models"},
		{"https://api.openai.com", "/v1/models", "https://api.openai.com/v1/models"},
		{"https://api.deepseek.com", "/v1/chat/completions", "https://api.deepseek.com/chat/completions"},
		{"https://api.groq.com/openai/v1", "/v1/chat/completions", "https://api.groq.com/openai/v1/chat/completions"},
	}
	for _, tc := range cases {
		if got := endpoint(tc.base, tc.path); got != tc.want {
			t.Fatalf("endpoint(%q, %q) = %q, want %q", tc.base, tc.path, got, tc.want)
		}
	}
}

func TestModelsDiscoveryURL(t *testing.T) {
	cases := []struct {
		name string
		p    store.Provider
		want string
	}{
		{
			name: "glm",
			p:    store.Provider{ID: "glm", Type: "openai-compatible", BaseURL: "https://api.z.ai/api/paas/v4"},
			want: "https://api.z.ai/api/paas/v4/models",
		},
		{
			name: "deepseek",
			p:    store.Provider{ID: "deepseek", Type: "openai-compatible", BaseURL: "https://api.deepseek.com"},
			want: "https://api.deepseek.com/models",
		},
		{
			name: "perplexity",
			p:    store.Provider{ID: "perplexity", Type: "openai-compatible", BaseURL: "https://api.perplexity.ai"},
			want: "https://api.perplexity.ai/models",
		},
		{
			name: "groq",
			p:    store.Provider{ID: "groq", Type: "openai-compatible", BaseURL: "https://api.groq.com/openai/v1"},
			want: "https://api.groq.com/openai/v1/models",
		},
		{
			name: "ollama cloud",
			p:    store.Provider{ID: "ollama", Type: "ollama", BaseURL: "https://ollama.com/api/chat"},
			want: "https://ollama.com/api/tags",
		},
		{
			name: "ollama local",
			p:    store.Provider{ID: "ollama-local", Type: "ollama", BaseURL: "http://localhost:11434/api/chat"},
			want: "http://localhost:11434/api/tags",
		},
		{
			name: "cline",
			p:    store.Provider{ID: "cline", Type: "cline", BaseURL: "https://api.cline.bot/api/v1"},
			want: "https://api.cline.bot/api/v1/ai/cline/models",
		},
		{
			name: "clinepass",
			p:    store.Provider{ID: "clinepass", Type: "clinepass", BaseURL: "https://api.cline.bot/api/v1"},
			want: "https://api.cline.bot/api/v1/ai/cline/models",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := modelsDiscoveryURL(tc.p); got != tc.want {
				t.Fatalf("modelsDiscoveryURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestShouldSkipModelDiscovery(t *testing.T) {
	skip := []store.Provider{
		{ID: "commandcode", Type: "openai-compatible", BaseURL: "https://api.commandcode.ai/alpha/generate"},
		{ID: "perplexity-web", Type: "openai-compatible", BaseURL: "https://www.perplexity.ai/rest/sse/perplexity_ask"},
		{ID: "kiro", Type: "kiro", BaseURL: "https://runtime.us-east-1.kiro.dev/generateAssistantResponse"},
		{ID: "cursor", Type: "cursor", BaseURL: "https://api2.cursor.sh"},
		{ID: "minimax", Type: "anthropic-compatible", BaseURL: "https://api.minimax.io/anthropic"},
	}
	for _, provider := range skip {
		if !shouldSkipModelDiscovery(provider) {
			t.Fatalf("expected skip for %+v", provider)
		}
	}
	allow := store.Provider{ID: "glm", Type: "openai-compatible", BaseURL: "https://api.z.ai/api/paas/v4"}
	if shouldSkipModelDiscovery(allow) {
		t.Fatalf("glm discovery should not be skipped")
	}
}

func TestNinerouterPresetOpenAIURLs(t *testing.T) {
	cases := map[string]struct {
		chatURL   string
		modelsURL string
	}{
		"glm": {
			chatURL:   "https://api.z.ai/api/paas/v4/chat/completions",
			modelsURL: "https://api.z.ai/api/paas/v4/models",
		},
		"glm-cn": {
			chatURL:   "https://open.bigmodel.cn/api/coding/paas/v4/chat/completions",
			modelsURL: "https://open.bigmodel.cn/api/coding/paas/v4/models",
		},
		"deepseek": {
			chatURL:   "https://api.deepseek.com/chat/completions",
			modelsURL: "https://api.deepseek.com/models",
		},
		"perplexity": {
			chatURL:   "https://api.perplexity.ai/chat/completions",
			modelsURL: "https://api.perplexity.ai/models",
		},
		"byteplus": {
			chatURL:   "https://ark.ap-southeast.bytepluses.com/api/coding/v3/chat/completions",
			modelsURL: "https://ark.ap-southeast.bytepluses.com/api/coding/v3/models",
		},
		"groq": {
			chatURL:   "https://api.groq.com/openai/v1/chat/completions",
			modelsURL: "https://api.groq.com/openai/v1/models",
		},
		"codebuddy-cn": {
			chatURL:   "https://copilot.tencent.com/v2/chat/completions",
			modelsURL: "https://copilot.tencent.com/v2/models",
		},
		"kimi": {
			chatURL:   "https://api.kimi.com/coding/v1/chat/completions",
			modelsURL: "https://api.kimi.com/coding/v1/models",
		},
		"ollama": {
			chatURL:   "https://ollama.com/api/chat",
			modelsURL: "https://ollama.com/api/tags",
		},
	}
	for id, want := range cases {
		preset, ok := ninerouter.Presets[id]
		if !ok {
			t.Fatalf("preset %q missing", id)
		}
		provider := store.Provider{ID: preset.ID, Type: preset.Type, BaseURL: preset.BaseURL}
		if got := endpoint(preset.BaseURL, "/v1/chat/completions"); got != want.chatURL {
			t.Fatalf("%s chat URL = %q, want %q", id, got, want.chatURL)
		}
		if got := modelsDiscoveryURL(provider); got != want.modelsURL {
			t.Fatalf("%s models URL = %q, want %q", id, got, want.modelsURL)
		}
	}
}
