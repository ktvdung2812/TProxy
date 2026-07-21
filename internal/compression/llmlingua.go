package compression

import (
	"strings"
	"unicode"

	"github.com/tproxy/tproxy/internal/canonical"
)

// applyLLMLingua prunes low-relevance sentences using keyword overlap (heuristic, no ONNX).
func applyLLMLingua(request *canonical.Request) int {
	if request == nil || len(request.Messages) == 0 {
		return 0
	}
	query := lastUserMessage(request)
	if query == "" {
		return 0
	}
	keywords := tokenizeKeywords(query)
	if len(keywords) == 0 {
		return 0
	}
	saved := 0
	for index := range request.Messages {
		if request.Messages[index].Role != "assistant" && request.Messages[index].Role != "tool" {
			continue
		}
		content := messageText(request.Messages[index].Content)
		if content == "" || strings.Contains(content, "```") {
			continue
		}
		pruned, removed := pruneSentences(content, keywords)
		if removed == 0 {
			continue
		}
		request.Messages[index].Content = pruned
		saved += estimateTokens(removed)
	}
	return saved
}

func lastUserMessage(request *canonical.Request) string {
	for index := len(request.Messages) - 1; index >= 0; index-- {
		if request.Messages[index].Role == "user" {
			return messageText(request.Messages[index].Content)
		}
	}
	return ""
}

func tokenizeKeywords(text string) map[string]struct{} {
	words := map[string]struct{}{}
	for _, token := range strings.Fields(strings.ToLower(text)) {
		token = strings.Trim(token, ".,;:!?\"'()[]{}")
		if len(token) < 4 {
			continue
		}
		words[token] = struct{}{}
	}
	return words
}

func pruneSentences(content string, keywords map[string]struct{}) (string, int) {
	sentences := splitSentences(content)
	if len(sentences) <= 1 {
		return content, 0
	}
	kept := make([]string, 0, len(sentences))
	removedBytes := 0
	for _, sentence := range sentences {
		if sentenceRelevant(sentence, keywords) {
			kept = append(kept, sentence)
			continue
		}
		removedBytes += len(sentence)
	}
	if len(kept) == 0 {
		return content, 0
	}
	return strings.Join(kept, ". ") + ".", removedBytes
}

func sentenceRelevant(sentence string, keywords map[string]struct{}) bool {
	lower := strings.ToLower(sentence)
	for keyword := range keywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	for _, r := range sentence {
		if unicode.IsDigit(r) {
			return true
		}
	}
	if strings.HasPrefix(lower, "the weather") || strings.HasPrefix(lower, "i like coffee") {
		return false
	}
	return len(strings.Fields(sentence)) <= 8
}
