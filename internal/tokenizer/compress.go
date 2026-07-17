package tokenizer

import (
	"fmt"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
)

const maxToolCharacters = 48_000

type Stats struct {
	MessagesChanged  int
	CharactersBefore int
	CharactersAfter  int
	TokensSaved      int
}

func Compress(request *canonical.Request) Stats {
	if request == nil {
		return Stats{}
	}
	var stats Stats
	for index := range request.Messages {
		message := &request.Messages[index]
		if message.Role == "tool" {
			if value, ok := message.Content.(string); ok {
				message.Content = compressString(value, &stats)
			}
		}
		message.Content = compressBlocks(message.Content, &stats)
	}
	stats.TokensSaved = estimateTokens(stats.CharactersBefore - stats.CharactersAfter)
	if stats.TokensSaved < 0 {
		stats.TokensSaved = 0
	}
	return stats
}

func compressBlocks(content any, stats *Stats) any {
	items, ok := content.([]any)
	if !ok {
		return content
	}
	result := make([]any, len(items))
	copy(result, items)
	for index, item := range result {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		typeName := fmt.Sprint(block["type"])
		if typeName != "tool_result" && typeName != "tool" {
			continue
		}
		value, ok := block["content"].(string)
		if !ok {
			continue
		}
		clone := make(map[string]any, len(block))
		for key, field := range block {
			clone[key] = field
		}
		clone["content"] = compressString(value, stats)
		result[index] = clone
	}
	return result
}

func compressString(input string, stats *Stats) string {
	if len(input) < 512 {
		return input
	}
	before := len(input)
	output := deduplicateConsecutiveLines(input)
	if len(output) > maxToolCharacters {
		head := output[:maxToolCharacters*2/3]
		tail := output[len(output)-maxToolCharacters/3:]
		output = head + "\n\n... [tproxy truncated repetitive tool output] ...\n\n" + tail
	}
	if len(output) >= before {
		return input
	}
	stats.MessagesChanged++
	stats.CharactersBefore += before
	stats.CharactersAfter += len(output)
	return output
}

func deduplicateConsecutiveLines(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) < 4 {
		return input
	}
	result := make([]string, 0, len(lines))
	previous := ""
	repeated := 0
	flushRepeated := func() {
		if repeated > 2 {
			result = append(result, fmt.Sprintf("[previous line repeated %d more times]", repeated-1))
		}
		repeated = 0
	}
	for _, line := range lines {
		if line == previous && line != "" {
			repeated++
			continue
		}
		flushRepeated()
		result = append(result, line)
		previous = line
		repeated = 1
	}
	flushRepeated()
	return strings.Join(result, "\n")
}

func estimateTokens(characters int) int {
	if characters <= 0 {
		return 0
	}
	return (characters + 3) / 4
}
