package providers

import (
	"encoding/json"
	"fmt"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

// PreviewRequest runs the same wire-body builders as the adapters, without
// resolving credentials, refreshing tokens, or making a network request.
func PreviewRequest(target string, request canonical.Request) (map[string]any, error) {
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	var cloned canonical.Request
	if err = json.Unmarshal(data, &cloned); err != nil {
		return nil, err
	}
	switch target {
	case "openai":
		return openAIBody(store.Provider{Type: "openai-compatible"}, cloned), nil
	case "responses", "codex":
		body := codexBody(cloned)
		applyCodexResponsesLiteContext(body, cloned)
		codexReverseToolNames(body)
		return body, nil
	case "claude":
		return anthropicBody(cloned), nil
	case "gemini":
		return geminiBody(cloned), nil
	default:
		return nil, fmt.Errorf("unsupported target protocol %q", target)
	}
}
