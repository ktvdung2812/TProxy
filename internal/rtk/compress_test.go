package rtk

import (
	"strconv"
	"strings"
	"testing"
)

func TestCompressTextGitStatus(t *testing.T) {
	lines := []string{"On branch main", "Changes not staged for commit:"}
	for i := 0; i < 40; i++ {
		lines = append(lines, "  modified:   internal/foo"+strconv.Itoa(i)+".go")
	}
	lines = append(lines, "", "Untracked files:", "  docs/new.md")
	input := strings.Join(lines, "\n")
	out := compressText(input, &Stats{}, "test")
	if out == input {
		t.Fatalf("expected compressed git status, got same output")
	}
	if !strings.Contains(out, "main") {
		t.Fatalf("output = %q", out)
	}
}

func TestCompressRequestOpenAIToolMessage(t *testing.T) {
	request := map[string]any{
		"messages": []any{
			map[string]any{
				"role": "tool",
				"content": strings.Repeat("error: build failed\n", 80),
			},
		},
	}
	stats := Stats{}
	compressBodyMap(request, &stats)
	content := request["messages"].([]any)[0].(map[string]any)["content"].(string)
	if len(content) >= len(strings.Repeat("error: build failed\n", 80)) {
		t.Fatalf("expected compression, content len=%d", len(content))
	}
}

func TestCompressRequestSkipsSmallToolOutput(t *testing.T) {
	request := map[string]any{
		"messages": []any{
			map[string]any{"role": "tool", "content": "short"},
		},
	}
	stats := Stats{}
	compressBodyMap(request, &stats)
	if stats.BytesBefore != len("short") || stats.BytesAfter != len("short") {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestCompressRequestSkipsErrorToolResult(t *testing.T) {
	request := map[string]any{
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"type":      "tool_result",
						"is_error":  true,
						"content":   strings.Repeat("x", 600),
						"tool_use_id": "t1",
					},
				},
			},
		},
	}
	stats := Stats{}
	compressBodyMap(request, &stats)
	block := request["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	if len(block["content"].(string)) != 600 {
		t.Fatalf("error tool result should be preserved")
	}
}
