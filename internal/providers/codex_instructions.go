package providers

import (
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/translator/claudeopenai"
)

func codexInstructionsString(request canonical.Request) string {
	if text := claudeopenai.SystemInstructionsText(request.System); text != "" {
		return text
	}
	for _, message := range request.Messages {
		if message.Role != "system" && message.Role != "developer" {
			continue
		}
		if text := codexMessageText(message.Content); text != "" {
			return text
		}
	}
	return ""
}

func codexMessagesWithoutInstructions(messages []canonical.Message) []canonical.Message {
	if len(messages) == 0 {
		return messages
	}
	filtered := make([]canonical.Message, 0, len(messages))
	for _, message := range messages {
		if message.Role == "system" || message.Role == "developer" {
			continue
		}
		filtered = append(filtered, message)
	}
	return filtered
}

func codexMessageText(content any) string {
	switch value := content.(type) {
	case string:
		return strings.TrimSpace(value)
	case []any:
		var parts []string
		for _, item := range value {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text := strings.TrimSpace(stringValue(block["text"])); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return strings.TrimSpace(stringValue(content))
	}
}
