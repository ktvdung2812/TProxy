package compression

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/rtk"
)

// Mode selects which compression engines run.
type Mode string

const (
	ModeOff      Mode = "off"
	ModeLite     Mode = "lite"
	ModeRTK      Mode = "rtk"
	ModeCaveman  Mode = "caveman"
	ModeStacked  Mode = "stacked"
)

type Stats struct {
	Mode         Mode
	BytesBefore  int
	BytesAfter   int
	TokensSaved  int
	RTKSaved     int
	CavemanSaved int
}

var fillerRE = regexp.MustCompile(`\b(the|a|an|is|are|was|were|that|which|who|whom|this|these|those|very|really|basically|actually|simply|just|likely|probably|would|could|should|please|note that|i would recommend|in order to)\b`)

// CompressRequest runs the configured compression pipeline (fail-open).
func CompressRequest(request *canonical.Request, mode Mode) Stats {
	if request == nil || mode == ModeOff {
		return Stats{Mode: mode}
	}
	stats := Stats{Mode: mode}
	switch mode {
	case ModeLite, ModeRTK:
		rtkStats := rtk.CompressRequest(request)
		stats.BytesBefore = rtkStats.BytesBefore
		stats.BytesAfter = rtkStats.BytesAfter
		stats.RTKSaved = rtkStats.TokensSaved
		stats.TokensSaved = rtkStats.TokensSaved
	case ModeCaveman:
		stats.BytesBefore = messageBytes(request)
		compressCavemanMessages(request)
		stats.BytesAfter = messageBytes(request)
		stats.CavemanSaved = estimateTokens(stats.BytesBefore - stats.BytesAfter)
		stats.TokensSaved = stats.CavemanSaved
	case ModeStacked:
		rtkStats := rtk.CompressRequest(request)
		stats.BytesBefore = rtkStats.BytesBefore
		stats.RTKSaved = rtkStats.TokensSaved
		beforeCaveman := messageBytes(request)
		compressCavemanMessages(request)
		afterCaveman := messageBytes(request)
		stats.BytesAfter = afterCaveman
		stats.CavemanSaved = estimateTokens(beforeCaveman - afterCaveman)
		stats.TokensSaved = stats.RTKSaved + stats.CavemanSaved
	default:
		rtkStats := rtk.CompressRequest(request)
		stats.TokensSaved = rtkStats.TokensSaved
	}
	if stats.TokensSaved < 0 {
		stats.TokensSaved = 0
	}
	return stats
}

func ParseMode(raw string) Mode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "lite":
		return ModeLite
	case "off", "false", "0":
		return ModeOff
	case "rtk":
		return ModeRTK
	case "caveman", "standard":
		return ModeCaveman
	case "stacked", "aggressive", "ultra":
		return ModeStacked
	default:
		return ModeLite
	}
}

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
