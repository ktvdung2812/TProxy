package providers

import "testing"

func TestEnrichGLMUpstreamMessage(t *testing.T) {
	got := enrichGLMUpstreamMessage(
		"https://api.z.ai/api/paas/v4",
		"Invalid API parameter, please check the documentation.",
		400,
	)
	if got == "Invalid API parameter, please check the documentation." {
		t.Fatalf("expected enriched message, got %q", got)
	}
	if alternateGLMBaseURL("https://api.z.ai/api/paas/v4") != "https://api.z.ai/api/coding/paas/v4" {
		t.Fatal("alternate URL mismatch")
	}
}
