package compression

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
)

const ccrMinBytes = 1800
const ccrPreviewBytes = 240

// applyCCR archives oversized message blocks behind retrieve markers (fail-open).
func applyCCR(request *canonical.Request) int {
	if request == nil {
		return 0
	}
	saved := 0
	for index := range request.Messages {
		content := messageText(request.Messages[index].Content)
		if len(content) < ccrMinBytes || strings.Contains(content, "[CCR:") {
			continue
		}
		hash := ccrHash(content)
		preview := content
		if len(preview) > ccrPreviewBytes {
			preview = preview[:ccrPreviewBytes]
		}
		replacement := fmt.Sprintf("[CCR:%s preview=%q … use X-TProxy-CCR-Retrieve: %s]", hash[:12], preview, hash)
		before := len(content)
		request.Messages[index].Content = replacement
		if request.Metadata == nil {
			request.Metadata = map[string]any{}
		}
		blocks, _ := request.Metadata["ccr_blocks"].(map[string]string)
		if blocks == nil {
			blocks = map[string]string{}
		}
		blocks[hash] = content
		request.Metadata["ccr_blocks"] = blocks
		saved += estimateTokens(before - len(replacement))
	}
	return saved
}

func ccrHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
