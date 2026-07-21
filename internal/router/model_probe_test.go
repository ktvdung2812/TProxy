package router

import "testing"

func TestResolveUpstreamProbeKind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		modelID string
		kind    string
		want    string
	}{
		{modelID: "gpt-5.5", kind: "embedding", want: "llm"},
		{modelID: "gpt-5.6-sol", kind: "embedding", want: "llm"},
		{modelID: "text-embedding-3-small", kind: "embedding", want: "embedding"},
		{modelID: "bge-large", kind: "embedding", want: "llm"},
		{modelID: "gpt-5.5", kind: "", want: "llm"},
		{modelID: "gpt-5.5", kind: "image", want: "image"},
	}
	for _, test := range tests {
		if got := resolveUpstreamProbeKind(test.modelID, test.kind); got != test.want {
			t.Fatalf("resolveUpstreamProbeKind(%q, %q)=%q want %q", test.modelID, test.kind, got, test.want)
		}
	}
}
