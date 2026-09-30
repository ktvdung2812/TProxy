package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
)

func TestStreamUsagePreservesCacheAndReasoning(t *testing.T) {
	usage := canonical.Usage{InputTokens: 70, OutputTokens: 8, CachedTokens: 40, CacheCreationTokens: 20, ReasoningTokens: 3}
	for _, protocol := range []string{"openai", "gemini"} {
		t.Run(protocol, func(t *testing.T) {
			events := make(chan canonical.Event, 3)
			events <- canonical.Event{Type: canonical.EventTextDelta, Text: "hello"}
			events <- canonical.Event{Type: canonical.EventUsage, Usage: &usage}
			events <- canonical.Event{Type: canonical.EventMessageEnd, FinishReason: "stop"}
			close(events)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/", nil)
			key := "usage"
			if protocol == "openai" {
				writeOpenAIStream(response, request, events, "req", "m")
			} else {
				key = "usageMetadata"
				writeGeminiStream(response, request, events)
			}
			var found map[string]any
			for _, line := range strings.Split(response.Body.String(), "\n") {
				var chunk map[string]any
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk) == nil {
					if value, ok := chunk[key].(map[string]any); ok {
						found = value
					}
				}
			}
			if found == nil {
				t.Fatalf("missing usage: %s", response.Body.String())
			}
			if protocol == "openai" {
				input, _ := found["prompt_tokens_details"].(map[string]any)
				output, _ := found["completion_tokens_details"].(map[string]any)
				if found["total_tokens"] != float64(78) || input["cached_tokens"] != float64(40) || input["cache_creation_tokens"] != float64(20) || output["reasoning_tokens"] != float64(3) {
					t.Fatalf("normalized usage lost: %+v", found)
				}
			} else if found["totalTokenCount"] != float64(78) || found["candidatesTokenCount"] != float64(5) || found["thoughtsTokenCount"] != float64(3) || found["cachedContentTokenCount"] != float64(40) {
				t.Fatalf("Gemini usage double counted or missing: %+v", found)
			}
		})
	}
}
