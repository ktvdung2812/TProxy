package rtk

import (
	"fmt"

	"github.com/tproxy/tproxy/internal/canonical"
)

type Stats struct {
	BytesBefore     int
	BytesAfter      int
	MessagesChanged int
	TokensSaved     int
	Hits            []Hit
}

type Hit struct {
	Shape  string
	Filter string
	Saved  int
}

// CompressRequest applies RTK tool-output compression to a canonical request.
// Ported from 9router open-sse/rtk/index.js (fail-open).
func CompressRequest(request *canonical.Request) Stats {
	if request == nil {
		return Stats{}
	}
	stats := Stats{}
	if request.Raw != nil {
		compressBodyMap(request.Raw, &stats)
	}
	for index := range request.Messages {
		compressMessage(&request.Messages[index], &stats)
	}
	stats.TokensSaved = estimateTokens(stats.BytesBefore - stats.BytesAfter)
	if stats.TokensSaved < 0 {
		stats.TokensSaved = 0
	}
	return stats
}

func compressBodyMap(body map[string]any, stats *Stats) {
	if body == nil {
		return
	}
	if conversationState, ok := body["conversationState"].(map[string]any); ok {
		compressKiroConversation(conversationState, stats)
	}
	items, ok := bodyArray(body["messages"])
	if !ok {
		items, ok = bodyArray(body["input"])
	}
	if !ok {
		return
	}
	for _, item := range items {
		compressBodyItem(item, stats)
	}
}

func compressKiroConversation(state map[string]any, stats *Stats) {
	all := []map[string]any{}
	if history, ok := bodyArray(state["history"]); ok {
		all = append(all, history...)
	}
	if current, ok := state["currentMessage"].(map[string]any); ok {
		all = append(all, current)
	}
	for _, msg := range all {
		userInput, _ := msg["userInputMessage"].(map[string]any)
		ctx, _ := userInput["userInputMessageContext"].(map[string]any)
		toolResults, ok := bodyArray(ctx["toolResults"])
		if !ok {
			continue
		}
		for _, tr := range toolResults {
			if fmt.Sprint(tr["status"]) == "error" {
				continue
			}
			content, ok := bodyArray(tr["content"])
			if !ok {
				continue
			}
			for _, part := range content {
				if text, ok := part["text"].(string); ok {
					part["text"] = compressText(text, stats, "kiro-tool-result")
				}
			}
		}
	}
}

func compressBodyItem(msg map[string]any, stats *Stats) {
	if msg == nil {
		return
	}
	if msg["type"] == "function_call_output" {
		switch output := msg["output"].(type) {
		case string:
			msg["output"] = compressText(output, stats, "openai-responses-string")
		case []any:
			for _, item := range output {
				part, _ := item.(map[string]any)
				if part != nil && part["type"] == "input_text" {
					if text, ok := part["text"].(string); ok {
						part["text"] = compressText(text, stats, "openai-responses-array")
					}
				}
			}
		}
		return
	}
	if fmt.Sprint(msg["role"]) == "tool" {
		switch content := msg["content"].(type) {
		case string:
			msg["content"] = compressText(content, stats, "openai-tool")
		case []any:
			for _, item := range content {
				part, _ := item.(map[string]any)
				if part != nil && part["type"] == "text" {
					if text, ok := part["text"].(string); ok {
						part["text"] = compressText(text, stats, "openai-tool-array")
					}
				}
			}
		}
		return
	}
	content, ok := bodyArray(msg["content"])
	if !ok {
		return
	}
	for _, block := range content {
		if block == nil || fmt.Sprint(block["type"]) != "tool_result" {
			continue
		}
		if block["is_error"] == true {
			continue
		}
		switch value := block["content"].(type) {
		case string:
			block["content"] = compressText(value, stats, "claude-string")
		case []any:
			for _, item := range value {
				part, _ := item.(map[string]any)
				if part != nil && part["type"] == "text" {
					if text, ok := part["text"].(string); ok {
						part["text"] = compressText(text, stats, "claude-array")
					}
				}
			}
		}
	}
}

func compressMessage(message *canonical.Message, stats *Stats) {
	if message == nil {
		return
	}
	if message.Role == "tool" {
		if value, ok := message.Content.(string); ok {
			message.Content = compressText(value, stats, "canonical-tool")
		}
	}
	message.Content = compressContentBlocks(message.Content, stats)
}

func compressContentBlocks(content any, stats *Stats) any {
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
		if block["is_error"] == true {
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
		clone["content"] = compressText(value, stats, "canonical-block")
		result[index] = clone
	}
	return result
}

func compressText(text string, stats *Stats, shape string) string {
	bytesIn := len(text)
	stats.BytesBefore += bytesIn
	if bytesIn < minCompressSize || bytesIn > rawCap {
		stats.BytesAfter += bytesIn
		return text
	}
	filter := autoDetectFilter(text)
	if filter.fn == nil {
		stats.BytesAfter += bytesIn
		return text
	}
	out := safeApply(filter, text)
	if out == "" || len(out) >= bytesIn {
		stats.BytesAfter += bytesIn
		return text
	}
	stats.BytesAfter += len(out)
	stats.MessagesChanged++
	stats.Hits = append(stats.Hits, Hit{Shape: shape, Filter: filter.name, Saved: bytesIn - len(out)})
	return out
}

func bodyArray(value any) ([]map[string]any, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, block)
	}
	return out, len(out) > 0
}

func estimateTokens(characters int) int {
	if characters <= 0 {
		return 0
	}
	return (characters + 3) / 4
}
