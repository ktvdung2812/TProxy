package compression

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
)

func TestCCRArchivesLargeBlocks(t *testing.T) {
	large := strings.Repeat("log line with stack trace details. ", 80)
	request := &canonical.Request{
		Messages: []canonical.Message{{Role: "tool", Content: large}},
	}
	saved := applyCCR(request)
	if saved <= 0 {
		t.Fatalf("expected CCR savings")
	}
	if !strings.Contains(messageText(request.Messages[0].Content), "[CCR:") {
		t.Fatalf("expected CCR marker")
	}
}

func TestHeadroomCompactsJSONArray(t *testing.T) {
	items := make([]map[string]any, 0, 12)
	for i := 1; i <= 12; i++ {
		items = append(items, map[string]any{"id": i, "name": fmt.Sprintf("item-%d", i), "status": "ok"})
	}
	payloadBytes, _ := json.Marshal(items)
	request := &canonical.Request{
		Messages: []canonical.Message{{Role: "tool", Content: string(payloadBytes)}},
	}
	saved := applyHeadroom(request)
	if saved <= 0 {
		t.Fatalf("expected headroom savings")
	}
	if !strings.Contains(messageText(request.Messages[0].Content), "_headroom") {
		t.Fatalf("expected headroom payload")
	}
}

func TestLLMLinguaPrunesIrrelevantSentences(t *testing.T) {
	request := &canonical.Request{
		Messages: []canonical.Message{
			{Role: "user", Content: "How do I fix the React useMemo dependency warning?"},
			{Role: "assistant", Content: "The weather is nice today. useMemo needs a stable dependency array. I like coffee."},
		},
	}
	saved := applyLLMLingua(request)
	if saved <= 0 {
		t.Fatalf("expected llmlingua savings")
	}
	if strings.Contains(messageText(request.Messages[1].Content), "weather") {
		t.Fatalf("irrelevant sentence should be pruned")
	}
}

func TestUltraModeRunsAllEngines(t *testing.T) {
	rows := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		rows = append(rows, fmt.Sprintf(`{"id":%d,"msg":"kubernetes pod crash stack trace line %d"}`, i, i))
	}
	request := &canonical.Request{
		Messages: []canonical.Message{
			{Role: "user", Content: "debug kubernetes pod crash"},
			{Role: "tool", Content: "[" + strings.Join(rows, ",") + "]"},
			{Role: "assistant", Content: strings.Repeat("The weather is nice today. ", 12) + "Check kubectl logs for the crashing container."},
		},
	}
	stats := CompressRequest(request, ModeUltra)
	if stats.TokensSaved <= 0 {
		t.Fatalf("expected ultra pipeline savings, got %+v", stats)
	}
}
