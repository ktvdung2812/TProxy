package compression

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/tproxy/tproxy/internal/canonical"
)

var fillerRE = regexp.MustCompile(`\b(the|a|an|is|are|was|were|that|which|who|whom|this|these|those|very|really|basically|actually|simply|just|likely|probably|would|could|should|please|note that|i would recommend|in order to)\b`)

func compressCavemanMessages(request *canonical.Request) {
	for index := range request.Messages {
		msg := &request.Messages[index]
		if msg.Role != "assistant" && msg.Role != "user" && msg.Role != "system" {
			continue
		}
		content := messageText(msg.Content)
		if content == "" {
			continue
		}
		if strings.Contains(content, "```") {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(content), "{") || strings.HasPrefix(strings.TrimSpace(content), "[") {
			continue
		}
		msg.Content = compressCavemanText(content)
	}
}

func messageText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []canonical.ContentBlock:
		var parts []string
		for _, block := range value {
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return fmt.Sprint(content)
	}
}

func compressCavemanText(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" || len(trimmed) < 80 {
		return input
	}
	sentences := splitSentences(trimmed)
	if len(sentences) <= 1 {
		return cavemanSentence(trimmed)
	}
	out := make([]string, 0, len(sentences))
	for _, sentence := range sentences {
		out = append(out, cavemanSentence(sentence))
	}
	return strings.Join(out, " ")
}

func cavemanSentence(sentence string) string {
	lower := strings.ToLower(strings.TrimSpace(sentence))
	lower = fillerRE.ReplaceAllString(lower, "")
	lower = strings.Join(strings.Fields(lower), " ")
	lower = strings.Trim(lower, " ,.;")
	if lower == "" {
		return sentence
	}
	return capitalize(lower)
}

func splitSentences(text string) []string {
	parts := regexp.MustCompile(`(?m)[.!?]+\s+`).Split(text, -1)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func capitalize(text string) string {
	if text == "" {
		return text
	}
	runes := []rune(text)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func messageBytes(request *canonical.Request) int {
	total := 0
	for _, msg := range request.Messages {
		total += len(messageText(msg.Content))
	}
	return total
}

func estimateTokens(bytes int) int {
	if bytes <= 0 {
		return 0
	}
	return bytes / 4
}
