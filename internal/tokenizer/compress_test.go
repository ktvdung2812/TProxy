package tokenizer

import (
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
)

func TestCompressToolOutputAndLeaveUserContentUntouched(t *testing.T) {
	repeated := strings.Repeat("same log line\n", 100)
	request := canonical.Request{Messages: []canonical.Message{{Role: "user", Content: repeated}, {Role: "tool", Content: repeated}}}
	stats := Compress(&request)
	if stats.TokensSaved <= 0 || stats.MessagesChanged != 1 {
		t.Fatalf("stats=%+v", stats)
	}
	if request.Messages[0].Content != repeated {
		t.Fatal("user content was modified")
	}
	if len(request.Messages[1].Content.(string)) >= len(repeated) {
		t.Fatal("tool content was not compressed")
	}
}

func TestCompressionFailsOpenWhenOutputDoesNotShrink(t *testing.T) {
	input := strings.Repeat("unique line ", 60)
	request := canonical.Request{Messages: []canonical.Message{{Role: "tool", Content: input}}}
	stats := Compress(&request)
	if request.Messages[0].Content != input || stats.TokensSaved != 0 {
		t.Fatalf("content changed unexpectedly: %+v", stats)
	}
}
