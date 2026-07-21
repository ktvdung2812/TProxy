package compression

import (
	"encoding/json"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
)

// applyHeadroom compacts homogeneous JSON arrays in tool-like payloads (fail-open).
func applyHeadroom(request *canonical.Request) int {
	if request == nil {
		return 0
	}
	saved := 0
	for index := range request.Messages {
		content := strings.TrimSpace(messageText(request.Messages[index].Content))
		if !strings.HasPrefix(content, "[") {
			continue
		}
		var items []map[string]any
		if json.Unmarshal([]byte(content), &items) != nil || len(items) < 3 {
			continue
		}
		keys := headroomKeys(items[0])
		if len(keys) == 0 {
			continue
		}
		compact := headroomCompact(items, keys)
		encoded, err := json.Marshal(compact)
		if err != nil || len(encoded) >= len(content) {
			continue
		}
		request.Messages[index].Content = string(encoded)
		saved += estimateTokens(len(content) - len(encoded))
	}
	return saved
}

func headroomKeys(sample map[string]any) []string {
	keys := make([]string, 0, len(sample))
	for key := range sample {
		keys = append(keys, key)
	}
	if len(keys) < 2 {
		return nil
	}
	return keys
}

func headroomCompact(items []map[string]any, keys []string) map[string]any {
	rows := make([][]any, 0, len(items))
	for _, item := range items {
		row := make([]any, 0, len(keys))
		for _, key := range keys {
			row = append(row, item[key])
		}
		rows = append(rows, row)
	}
	return map[string]any{"_headroom": map[string]any{"keys": keys, "rows": rows}}
}
