package compression

import (
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
)

func TestCavemanCompressionReducesProse(t *testing.T) {
	request := &canonical.Request{
		Messages: []canonical.Message{{
			Role:    "assistant",
			Content: "The reason your component is re-rendering is likely because you are creating a new object reference on each render cycle. I would recommend using useMemo to memoize the object.",
		}},
	}
	stats := CompressRequest(request, ModeCaveman)
	if stats.TokensSaved <= 0 {
		t.Fatalf("expected caveman savings, got %+v content=%q", stats, request.Messages[0].Content)
	}
	if content, ok := request.Messages[0].Content.(string); !ok || strings.Contains(content, "I would recommend") {
		t.Fatalf("filler should be removed: %q", request.Messages[0].Content)
	}
}

func TestStackedCompressionPreservesCodeBlocks(t *testing.T) {
	request := &canonical.Request{
		Messages: []canonical.Message{{
			Role:    "assistant",
			Content: "```go\nfunc main() {}\n```\nThe reason is that you should really use memoization here.",
		}},
	}
	CompressRequest(request, ModeStacked)
	if content, ok := request.Messages[0].Content.(string); !ok || !strings.Contains(content, "func main()") {
		t.Fatalf("code block must be preserved: %v", request.Messages[0].Content)
	}
}

func TestParseMode(t *testing.T) {
	if ParseMode("stacked") != ModeStacked {
		t.Fatal("expected stacked mode")
	}
	if ParseMode("off") != ModeOff {
		t.Fatal("expected off mode")
	}
}
