package providers

import "testing"

func TestUsageCacheNormalizationRoundTrip(t *testing.T) {
	usage := parseClaudeUsage(map[string]any{"input_tokens": 10, "cache_read_input_tokens": 40, "cache_creation_input_tokens": 20, "output_tokens": 5})
	if usage.InputTokens != 70 || usage.CachedTokens != 40 || usage.CacheCreationTokens != 20 {
		t.Fatalf("usage: %+v", usage)
	}
	claude := usage.ClaudeUsage()
	if claude["input_tokens"] != 10 || claude["cache_creation_input_tokens"] != 20 {
		t.Fatalf("double counted Claude cache: %+v", claude)
	}
	openai := usage.OpenAIUsage(true)
	if openai["total_tokens"] != 75 {
		t.Fatalf("total: %#v", openai)
	}
	parsed := parseResponsesUsage(openai)
	if parsed != usage {
		t.Fatalf("roundtrip: %+v want %+v", parsed, usage)
	}
	gemini := parseGeminiUsage(map[string]any{"promptTokenCount": 10, "candidatesTokenCount": 5, "thoughtsTokenCount": 3, "cachedContentTokenCount": 4})
	if gemini.OutputTokens != 8 || gemini.ReasoningTokens != 3 || gemini.InputTokens != 10 {
		t.Fatalf("Gemini normalization: %+v", gemini)
	}
}
