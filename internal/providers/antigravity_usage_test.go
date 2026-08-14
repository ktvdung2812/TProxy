package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

// Cloud Code reports token accounting under two different names and at two
// different levels of the envelope. Every combination has to land on the same
// canonical usage, because anything it misses is billed to the operator as a
// free request and never counted against the account's quota.
func TestAntigravityUsageIsReadFromEveryEnvelopeShape(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{"inner usageMetadata", `{"response":{"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20}}}`},
		{"inner cpaUsageMetadata", `{"response":{"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}],"cpaUsageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20}}}`},
		{"envelope usageMetadata", `{"response":{"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}]},"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20}}`},
		{"envelope cpaUsageMetadata", `{"response":{"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}]},"cpaUsageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20}}`},
		{"no envelope", `{"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20}}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var raw map[string]any
			if err := json.Unmarshal([]byte(testCase.payload), &raw); err != nil {
				t.Fatal(err)
			}
			response := canonicalGeminiResponse(unwrapAntigravityResponse(raw), "gemini-3-pro", nil)
			if response.Usage.InputTokens != 10 || response.Usage.OutputTokens != 20 {
				t.Fatalf("usage = in:%d out:%d, want in:10 out:20", response.Usage.InputTokens, response.Usage.OutputTokens)
			}
		})
	}
}

// cpaUsageMetadata is the Cloud Code layer's own accounting, which is what the
// account's quota is measured against, so it wins over the model-level counts.
func TestAntigravityPrefersCloudCodeUsageWhenBothPresent(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(`{"response":{"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2},
		"cpaUsageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20}}}`), &raw); err != nil {
		t.Fatal(err)
	}
	response := canonicalGeminiResponse(unwrapAntigravityResponse(raw), "gemini-3-pro", nil)
	if response.Usage.InputTokens != 10 || response.Usage.OutputTokens != 20 {
		t.Fatalf("usage = in:%d out:%d, want the cpa counts in:10 out:20", response.Usage.InputTokens, response.Usage.OutputTokens)
	}
}

func TestAntigravityUsageKeepsAllTokenClasses(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(`{"response":{"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20,"thoughtsTokenCount":7,"cachedContentTokenCount":3}}}`), &raw); err != nil {
		t.Fatal(err)
	}
	usage := canonicalGeminiResponse(unwrapAntigravityResponse(raw), "gemini-3-pro", nil).Usage
	if usage.ReasoningTokens != 7 {
		t.Errorf("reasoning tokens = %d, want 7", usage.ReasoningTokens)
	}
	if usage.CachedTokens != 3 {
		t.Errorf("cached tokens = %d, want 3", usage.CachedTokens)
	}
}

func antigravityStreamUsage(t *testing.T, sse string) (canonical.Usage, string) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse))
	}))
	defer upstream.Close()

	adapter := &antigravityAdapter{client: upstream.Client()}
	credential := store.Credential{AuthType: "oauth", OAuthToken: &store.OAuthToken{Extra: map[string]any{"project_id": "p"}}}
	events, err := adapter.ExecuteStream(context.Background(), store.Provider{Type: "antigravity", BaseURL: upstream.URL}, credential,
		canonical.Request{RequestID: "r", UpstreamModel: "gemini-3-pro", Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	var usage canonical.Usage
	var finishReason string
	for event := range events {
		if event.Type == canonical.EventUsage && event.Usage != nil {
			usage = *event.Usage
		}
		if event.Type == canonical.EventMessageEnd {
			finishReason = event.FinishReason
		}
	}
	return usage, finishReason
}

// Cloud Code often reports the final token counts in a chunk that arrives after
// the one carrying finishReason. Ending the stream on finishReason dropped that
// chunk and recorded the request as costing nothing.
func TestAntigravityStreamCapturesUsageAfterFinishReason(t *testing.T) {
	usage, finishReason := antigravityStreamUsage(t,
		"data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}]},\"finishReason\":\"STOP\"}]}}\n\n"+
			"data: {\"response\":{\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":20}}}\n\n")
	if usage.InputTokens != 10 || usage.OutputTokens != 20 {
		t.Fatalf("usage = in:%d out:%d, want in:10 out:20 from the trailing chunk", usage.InputTokens, usage.OutputTokens)
	}
	if finishReason != "STOP" {
		t.Fatalf("finish reason = %q, want STOP to survive the deferred terminal event", finishReason)
	}
}

// Per-chunk usage is cumulative, not incremental, so the last report wins
// rather than being summed.
func TestAntigravityStreamUsageIsNotAccumulated(t *testing.T) {
	usage, _ := antigravityStreamUsage(t,
		"data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"a\"}]}}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":5}}}\n\n"+
			"data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"b\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":20}}}\n\n")
	if usage.InputTokens != 10 || usage.OutputTokens != 20 {
		t.Fatalf("usage = in:%d out:%d, want the last cumulative report in:10 out:20", usage.InputTokens, usage.OutputTokens)
	}
}

func TestAntigravityStreamEmitsTerminalEventWithoutFinishReason(t *testing.T) {
	_, finishReason := antigravityStreamUsage(t,
		"data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}]}}]}}\n\n")
	if finishReason != "" {
		t.Fatalf("finish reason = %q, want empty when the upstream never sent one", finishReason)
	}
}
