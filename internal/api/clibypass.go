package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
)

// Claude Code fires several housekeeping turns that never need a real model:
// a warmup ping, a token "count" probe, and a topic-naming call that asks the
// model to emit {"isNewTopic":…,"title":…}. Answering them locally keeps them off
// the provider bill. Ported from 9router's open-sse/utils/bypassHandler.js.

const bypassDefaultText = "CLI Command Execution: Clear Terminal"

// claudeCLIRequest reports whether the caller is the Claude Code CLI. The bypass
// must never fire for ordinary API clients that happen to send similar messages.
func claudeCLIRequest(r *http.Request) bool {
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	return strings.Contains(ua, "claude-cli") || strings.Contains(ua, "claude-code")
}

// detectClaudeBypass returns the canned reply for a housekeeping turn, or ok=false
// when the request should go to a provider as normal.
func detectClaudeBypass(request canonical.Request, filterNaming bool) (string, bool) {
	if len(request.Messages) == 0 {
		return "", false
	}

	// Title extraction: Claude Code primes the assistant turn with a bare "{".
	last := request.Messages[len(request.Messages)-1]
	if last.Role == "assistant" && strings.TrimSpace(messageText(last.Content)) == "{" {
		return bypassDefaultText, true
	}

	first := strings.TrimSpace(messageText(request.Messages[0].Content))
	if first == "Warmup" {
		return bypassDefaultText, true
	}
	if len(request.Messages) == 1 && request.Messages[0].Role == "user" && first == "count" {
		return bypassDefaultText, true
	}

	if filterNaming && strings.Contains(systemPromptText(request), "isNewTopic") {
		return namingReply(request), true
	}
	return "", false
}

// namingReply fabricates the JSON payload Claude Code expects from a naming turn,
// deriving the title from the first user message instead of asking a model.
func namingReply(request canonical.Request) string {
	title := ""
	for _, message := range request.Messages {
		if message.Role != "user" {
			continue
		}
		if fields := strings.Fields(messageText(message.Content)); len(fields) > 0 {
			if len(fields) > 3 {
				fields = fields[:3]
			}
			title = strings.Join(fields, " ")
		}
		break
	}
	payload, err := json.Marshal(map[string]any{"isNewTopic": true, "title": title})
	if err != nil {
		return bypassDefaultText
	}
	return string(payload)
}

// systemPromptText flattens the system prompt, which Claude sends either as a
// top-level string, a list of text blocks, or an in-band system message.
func systemPromptText(request canonical.Request) string {
	parts := []string{messageText(request.System)}
	for _, message := range request.Messages {
		if message.Role == "system" {
			parts = append(parts, messageText(message.Content))
		}
	}
	return strings.Join(parts, " ")
}

// messageText extracts the plain text of a content value, ignoring non-text blocks.
func messageText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		parts := make([]string, 0, len(value))
		for _, item := range value {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if stringValue(block["type"]) != "text" {
				continue
			}
			if text := stringValue(block["text"]); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, " ")
	case map[string]any:
		return stringValue(value["text"])
	default:
		return ""
	}
}

// writeClaudeBypass renders the canned reply in the shape the client asked for,
// reusing the normal Claude renderers so the payload stays wire-identical.
func writeClaudeBypass(w http.ResponseWriter, r *http.Request, request canonical.Request, text string) {
	response := &canonical.Response{
		ID:           request.RequestID,
		Model:        request.PublicModelID,
		Role:         "assistant",
		Content:      text,
		FinishReason: "stop",
		Usage:        canonical.Usage{InputTokens: 1, OutputTokens: 1},
	}
	if !request.Stream {
		writeJSON(w, http.StatusOK, renderClaude(response, request.RequestID, request.PublicModelID))
		return
	}

	events := make(chan canonical.Event, 4)
	events <- canonical.Event{Type: canonical.EventMessageStart, ID: request.RequestID, Model: request.PublicModelID}
	events <- canonical.Event{Type: canonical.EventTextDelta, Text: text}
	events <- canonical.Event{Type: canonical.EventUsage, Usage: &response.Usage}
	events <- canonical.Event{Type: canonical.EventMessageEnd, FinishReason: "stop"}
	close(events)
	writeClaudeStream(w, r, events, request.RequestID, request.PublicModelID)
}
