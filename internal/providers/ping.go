package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

// PingResult is returned by dashboard model probes.
type PingResult struct {
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
	Status    int    `json:"status,omitempty"`
}

// PingModel sends a minimal upstream request to verify a model is reachable.
func (r *Registry) PingModel(ctx context.Context, provider store.Provider, credential store.Credential, modelID, kind string) PingResult {
	if strings.TrimSpace(kind) == "" {
		kind = "llm"
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	start := time.Now()
	switch kind {
	case "embedding":
		return r.pingEmbedding(ctx, provider, credential, modelID, start)
	case "image":
		return r.pingImage(ctx, provider, credential, modelID, start)
	default:
		return r.pingLLM(ctx, provider, credential, modelID, start)
	}
}

func (r *Registry) pingLLM(ctx context.Context, provider store.Provider, credential store.Credential, modelID string, start time.Time) PingResult {
	adapter, err := r.Adapter(provider.Type)
	if err != nil {
		return failPing(start, 0, err.Error())
	}
	ctx = withCredentialProxy(ctx, credential)
	request := canonical.Request{
		RequestID:     fmt.Sprintf("model-test-%d", start.UnixNano()),
		Source:        defaultProtocol(provider.Type),
		UpstreamModel: modelID,
		MaxTokens:     pingMaxTokens(provider, modelID),
		Messages:      []canonical.Message{{Role: "user", Content: "hi"}},
	}
	response, err := adapter.Execute(ctx, provider, credential, request)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		msg := truncatePingError(err.Error())
		if pe, ok := err.(*ProviderError); ok {
			msg = truncatePingError(enrichGLMUpstreamMessage(provider.BaseURL, pe.Message, pe.Status))
		}
		return PingResult{OK: false, LatencyMS: latency, Error: msg, Status: Status(err)}
	}
	if !llmPingSucceeded(response) {
		return PingResult{OK: false, LatencyMS: latency, Error: "Provider returned no completion content for this model"}
	}
	return PingResult{OK: true, LatencyMS: latency}
}

func (r *Registry) pingEmbedding(ctx context.Context, provider store.Provider, credential store.Credential, modelID string, start time.Time) PingResult {
	body, _ := json.Marshal(map[string]any{"model": modelID, "input": "test"})
	return r.pingJSON(ctx, provider, credential, "/v1/embeddings", body, start, func(payload map[string]any) bool {
		data, _ := payload["data"].([]any)
		if len(data) == 0 {
			return false
		}
		first, _ := data[0].(map[string]any)
		embedding, _ := first["embedding"].([]any)
		return len(embedding) > 0
	}, "Provider returned no embedding data")
}

func (r *Registry) pingImage(ctx context.Context, provider store.Provider, credential store.Credential, modelID string, start time.Time) PingResult {
	body, _ := json.Marshal(map[string]any{"model": modelID, "prompt": "test"})
	return r.pingJSON(ctx, provider, credential, "/v1/images/generations", body, start, func(payload map[string]any) bool {
		data, _ := payload["data"].([]any)
		return len(data) > 0
	}, "Provider returned no image data for this model")
}

func (r *Registry) pingJSON(ctx context.Context, provider store.Provider, credential store.Credential, path string, body []byte, start time.Time, validate func(map[string]any) bool, emptyMessage string) PingResult {
	ctx = withCredentialProxy(ctx, credential)
	headers := authHeaders(provider, credential)
	headers.Set("Content-Type", "application/json")
	if provider.Type == "codex" {
		headers = codexHeaders(provider, credential, false, canonical.Request{})
		headers.Set("Content-Type", "application/json")
	}
	target := endpoint(provider.BaseURL, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return failPing(start, 0, err.Error())
	}
	req.Header = correlationHeaders(headers, "")
	response, err := r.client.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return PingResult{OK: false, LatencyMS: latency, Error: truncatePingError(err.Error())}
	}
	defer response.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if readErr != nil {
		return PingResult{OK: false, LatencyMS: latency, Error: truncatePingError(readErr.Error()), Status: response.StatusCode}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		err := upstreamResponseErrorForProvider(response, raw, provider.BaseURL)
		return PingResult{OK: false, LatencyMS: latency, Error: truncatePingError(err.Error()), Status: response.StatusCode}
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return PingResult{OK: false, LatencyMS: latency, Error: "Provider returned invalid JSON", Status: response.StatusCode}
	}
	if errValue := payload["error"]; errValue != nil {
		detail := pingErrorDetail(errValue)
		return PingResult{OK: false, LatencyMS: latency, Error: truncatePingError(detail), Status: response.StatusCode}
	}
	if !validate(payload) {
		return PingResult{OK: false, LatencyMS: latency, Error: emptyMessage, Status: response.StatusCode}
	}
	return PingResult{OK: true, LatencyMS: latency, Status: response.StatusCode}
}

func pingMaxTokens(provider store.Provider, modelID string) int {
	lowerModel := strings.ToLower(modelID)
	// Reasoning-heavy models often spend the first tokens on thinking; keep
	// enough headroom so probe content (or reasoning) is non-empty.
	if isGLMProvider(provider) ||
		strings.Contains(lowerModel, "glm") ||
		provider.Type == "cline" ||
		provider.Type == "clinepass" ||
		strings.Contains(lowerModel, "kimi") ||
		strings.Contains(lowerModel, "claude") ||
		strings.Contains(lowerModel, "deepseek") {
		return 256
	}
	return 16
}

func defaultProtocol(providerType string) canonical.Protocol {
	switch providerType {
	case "codex":
		return canonical.ProtocolResponses
	case "claude", "anthropic-compatible":
		return canonical.ProtocolClaude
	case "gemini", "vertex", "antigravity":
		return canonical.ProtocolGemini
	default:
		return canonical.ProtocolOpenAI
	}
}

func llmPingSucceeded(response *canonical.Response) bool {
	if response == nil {
		return false
	}
	if strings.TrimSpace(response.Reasoning) != "" {
		return true
	}
	if len(response.ToolCalls) > 0 {
		return true
	}
	if response.Usage.OutputTokens > 0 || response.Usage.ReasoningTokens > 0 {
		return true
	}
	switch content := response.Content.(type) {
	case string:
		return strings.TrimSpace(content) != ""
	case nil:
		return false
	default:
		return strings.TrimSpace(fmt.Sprint(content)) != ""
	}
}

func failPing(start time.Time, status int, message string) PingResult {
	return PingResult{OK: false, LatencyMS: time.Since(start).Milliseconds(), Error: truncatePingError(message), Status: status}
}

func truncatePingError(message string) string {
	message = strings.TrimSpace(message)
	if len(message) <= 240 {
		return message
	}
	return message[:240]
}

func pingErrorDetail(value any) string {
	if nested, ok := value.(map[string]any); ok {
		if detail := stringValue(firstValue(nested, "message", "type")); detail != "" {
			return detail
		}
	}
	return stringValue(value)
}
