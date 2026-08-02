package providers

import (
	"net/url"
	"strings"

	cursorpkg "github.com/tproxy/tproxy/internal/providers/cursor"
	"github.com/tproxy/tproxy/internal/store"
)

// openAIResourceURL joins an OpenAI-compatible provider base URL with a resource
// suffix such as /chat/completions or /models. It mirrors how the OpenAI SDK
// resolves paths when base_url already embeds a version segment (Z.AI paas/v4,
// BytePlus coding/v3, DeepSeek host root, Groq /openai/v1, etc.).
func openAIResourceURL(baseURL, resource string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	resource = "/" + strings.TrimLeft(strings.TrimSpace(resource), "/")
	if base == "" {
		return resource
	}
	if isTerminalOpenAIResourceBase(base) {
		return base
	}
	lowerBase := strings.ToLower(base)
	if strings.HasSuffix(lowerBase, "/api/chat") && resource == "/chat/completions" {
		return base
	}
	return base + openAIV1Prefix(base) + resource
}

func openAIV1Prefix(base string) string {
	if usesEmbeddedAPIVersion(base) {
		return ""
	}
	if strings.HasSuffix(base, "/v1") || strings.Contains(base, "/openai/v1") {
		return ""
	}
	if isAPIHostRoot(base) {
		if hostRootSkipsV1(base) {
			return ""
		}
		return "/v1"
	}
	return "/v1"
}

func hostRootSkipsV1(base string) bool {
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" {
		return false
	}
	switch strings.ToLower(parsed.Host) {
	case "api.deepseek.com", "api.perplexity.ai":
		return true
	default:
		return false
	}
}

func usesEmbeddedAPIVersion(base string) bool {
	return strings.Contains(base, "/paas/v4") ||
		strings.Contains(base, "/coding/paas/v") ||
		strings.Contains(base, "/coding/v3") ||
		strings.HasSuffix(base, "/v2")
}

func isAPIHostRoot(base string) bool {
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" {
		return false
	}
	path := strings.Trim(parsed.Path, "/")
	return path == ""
}

func isTerminalOpenAIResourceBase(base string) bool {
	lower := strings.ToLower(base)
	return strings.HasSuffix(lower, "/chat/completions") ||
		strings.HasSuffix(lower, "/chat") ||
		strings.HasSuffix(lower, "/generate") ||
		strings.HasSuffix(lower, "/responses") ||
		strings.HasSuffix(lower, "/conversations/new") ||
		strings.Contains(lower, "/agent_chat_generation") ||
		strings.Contains(lower, "/perplexity_ask")
}

func discoveryPathForProvider(provider store.Provider) string {
	base := strings.TrimRight(provider.BaseURL, "/")
	switch provider.Type {
	case "gemini", "plugin-http", "codex", "ollama":
		return "/configured"
	case "anthropic-compatible", "claude", "kiro", "qoder", "tavily", "elevenlabs", "antigravity", "vertex", "copilot":
		return ""
	}
	if provider.ID == "opencode" {
		return "/configured"
	}
	if isTerminalOpenAIResourceBase(base) {
		return ""
	}
	return "/configured"
}

func modelsDiscoveryURL(provider store.Provider) string {
	switch provider.Type {
	case "gemini":
		return endpoint(provider.BaseURL, "/v1beta/models")
	case "plugin-http":
		return endpoint(provider.BaseURL, "/models")
	case "codex":
		return endpoint(provider.BaseURL, "/models?client_version=1.0.0")
	case "ollama":
		return ollamaDiscoveryURL(provider.BaseURL)
	case "cline":
		// Extension account catalog (OAuth / Cline account models).
		return openAIResourceURL(provider.BaseURL, "/ai/cline/models")
	case "clinepass":
		// ClinePass subscription catalog is the OpenAI-style /models list
		// (ids prefixed with cline-pass/). Mirrors 9router resolveClinepassModels.
		return openAIResourceURL(provider.BaseURL, "/models")
	}
	if provider.ID == "opencode" {
		return endpoint(provider.BaseURL, "/zen/v1/models")
	}
	return openAIResourceURL(provider.BaseURL, "/models")
}

func ollamaDiscoveryURL(baseURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" {
		return strings.TrimRight(baseURL, "/") + "/api/tags"
	}
	return parsed.Scheme + "://" + parsed.Host + "/api/tags"
}

func shouldSkipModelDiscovery(provider store.Provider) bool {
	if discoveryPathForProvider(provider) == "" {
		return true
	}
	switch provider.Type {
	case "tavily", "elevenlabs", "antigravity", "kiro", "qoder", "cursor":
		return true
	}
	if isTerminalOpenAIResourceBase(strings.TrimRight(provider.BaseURL, "/")) {
		return true
	}
	return false
}

func staticDiscoveryModels(provider store.Provider) []DiscoveredModel {
	if provider.Type == "cursor" || provider.ID == "cursor" {
		return cursorStaticModelEntries(provider)
	}
	if provider.Type == "kiro" || provider.ID == "kiro" {
		return kiroStaticModelEntries(NewRegistry(), provider)
	}
	if provider.Type == "cline" || provider.ID == "cline" {
		return clineStaticModelEntries(provider)
	}
	if provider.Type == "clinepass" || provider.ID == "clinepass" {
		return clinepassStaticModelEntries(provider)
	}
	switch provider.ID {
	case "glm", "glm-cn":
		return glmStaticModelEntries(provider)
	default:
		return nil
	}
}

// Static catalogs from 9router open-sse/providers/registry/cline.js + clinepass.js.
func clineStaticModelEntries(provider store.Provider) []DiscoveredModel {
	ids := []struct{ id, name string }{
		{"anthropic/claude-opus-4.7", "Claude Opus 4.7"},
		{"anthropic/claude-sonnet-4.6", "Claude Sonnet 4.6"},
		{"anthropic/claude-opus-4.6", "Claude Opus 4.6"},
		{"openai/gpt-5.3-codex", "GPT-5.3 Codex"},
		{"openai/gpt-5.4", "GPT-5.4"},
		{"google/gemini-3.1-pro-preview", "Gemini 3.1 Pro Preview"},
		{"google/gemini-3.1-flash-lite-preview", "Gemini 3.1 Flash Lite Preview"},
		{"kwaipilot/kat-coder-pro", "KAT Coder Pro"},
	}
	return clineDiscoveredFromIDs(provider, "cline", ids)
}

func clinepassStaticModelEntries(provider store.Provider) []DiscoveredModel {
	ids := []struct{ id, name string }{
		{"cline-pass/glm-5.2", "GLM-5.2 (ClinePass)"},
		{"cline-pass/kimi-k2.7-code", "Kimi K2.7 Code (ClinePass)"},
		{"cline-pass/kimi-k2.6", "Kimi K2.6 (ClinePass)"},
		{"cline-pass/deepseek-v4-pro", "DeepSeek V4 Pro (ClinePass)"},
		{"cline-pass/deepseek-v4-flash", "DeepSeek V4 Flash (ClinePass)"},
		{"cline-pass/mimo-v2.5", "MiMo-V2.5 (ClinePass)"},
		{"cline-pass/mimo-v2.5-pro", "MiMo-V2.5-Pro (ClinePass)"},
		{"cline-pass/minimax-m3", "MiniMax M3 (ClinePass)"},
		{"cline-pass/qwen3.7-max", "Qwen3.7 Max (ClinePass)"},
		{"cline-pass/qwen3.7-plus", "Qwen3.7 Plus (ClinePass)"},
	}
	return clineDiscoveredFromIDs(provider, "clinepass", ids)
}

func clineDiscoveredFromIDs(provider store.Provider, ownedBy string, ids []struct{ id, name string }) []DiscoveredModel {
	registry := NewRegistry()
	items := make([]DiscoveredModel, 0, len(ids))
	for _, model := range ids {
		items = append(items, DiscoveredModel{
			ID:           model.id,
			Name:         model.name,
			OwnedBy:      ownedBy,
			Capabilities: discoveryCapabilities(registry, provider, provider.Type, model.id),
		})
	}
	return items
}

func cursorStaticModelEntries(provider store.Provider) []DiscoveredModel {
	registry := NewRegistry()
	items := make([]DiscoveredModel, 0, len(cursorpkg.StaticCursorModels))
	for _, model := range cursorpkg.StaticCursorModels {
		items = append(items, DiscoveredModel{
			ID:           model.ID,
			Name:         model.Name,
			OwnedBy:      "cursor",
			Capabilities: discoveryCapabilities(registry, provider, provider.Type, model.ID),
		})
	}
	return items
}

func glmStaticModelEntries(provider store.Provider) []DiscoveredModel {
	ids := []struct{ id, name string }{
		{"glm-5.2", "GLM 5.2"},
		{"glm-5.1", "GLM 5.1"},
		{"glm-5", "GLM 5"},
		{"glm-4.7", "GLM 4.7"},
		{"glm-4.6v", "GLM 4.6V (Vision)"},
	}
	items := make([]DiscoveredModel, 0, len(ids))
	for _, model := range ids {
		items = append(items, DiscoveredModel{
			ID: model.id, Name: model.name, OwnedBy: "z.ai",
			Capabilities: discoveryCapabilities(NewRegistry(), provider, provider.Type, model.id),
		})
	}
	return items
}

func discoveryPath(provider store.Provider) string {
	return discoveryPathForProvider(provider)
}
