package providers

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

type captureTransport struct{ request *http.Request }

func (t *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.request = req
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl-1","choices":[]}`)),
	}, nil
}

func TestOpenAIAdapterSendsOpenCodeSession(t *testing.T) {
	transport := &captureTransport{}
	adapter := &openAIAdapter{client: &http.Client{Transport: transport}}
	provider := store.Provider{Type: "openai-compatible", BaseURL: "https://opencode.ai/zen/go/v1"}
	request := canonical.Request{
		RequestID: "req-1",
		Messages:  []canonical.Message{{Role: "user", Content: "hello"}},
	}
	if _, err := adapter.Execute(context.Background(), provider, store.Credential{AuthType: "none"}, request); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if transport.request == nil {
		t.Fatal("no upstream request captured")
	}
	if got := transport.request.Header.Get("x-opencode-session"); got == "" {
		t.Fatal("x-opencode-session missing on upstream request")
	}
	if got := transport.request.Header.Get("x-opencode-request"); got != "req-1" {
		t.Fatalf("x-opencode-request = %q, want req-1", got)
	}
	if got := transport.request.Header.Get("User-Agent"); !strings.HasPrefix(got, "tproxy/") {
		t.Fatalf("User-Agent = %q, want tproxy/*", got)
	}
}

func TestIsOpenCodeUpstream(t *testing.T) {
	cases := []struct {
		baseURL string
		want    bool
	}{
		{"https://opencode.ai/zen/go/v1", true},
		{"https://api.opencode.ai/v1", true},
		{"https://opencode.ai.evil.com/v1", false},
		{"https://api.z.ai/api/paas/v4", false},
		{"", false},
		{"not a url", false},
	}
	for _, tc := range cases {
		if got := isOpenCodeUpstream(store.Provider{BaseURL: tc.baseURL}); got != tc.want {
			t.Errorf("isOpenCodeUpstream(%q) = %v, want %v", tc.baseURL, got, tc.want)
		}
	}
}

func TestApplyOpenCodeHeadersSkipsNonOpenCode(t *testing.T) {
	headers := http.Header{}
	applyOpenCodeHeaders(headers, store.Provider{BaseURL: "https://api.z.ai/api/paas/v4"}, canonical.Request{RequestID: "req-1"})
	if headers.Get("x-opencode-session") != "" {
		t.Fatal("non-opencode upstream must not get opencode headers")
	}
}

func TestApplyOpenCodeHeadersUsesClientSession(t *testing.T) {
	headers := http.Header{}
	request := canonical.Request{RequestID: "req-1", SessionID: "ses_client"}
	applyOpenCodeHeaders(headers, store.Provider{BaseURL: "https://opencode.ai/zen/go/v1"}, request)
	if got := headers.Get("x-opencode-session"); got != "ses_client" {
		t.Fatalf("x-opencode-session = %q, want client session", got)
	}
	if got := headers.Get("x-opencode-request"); got != "req-1" {
		t.Fatalf("x-opencode-request = %q, want request ID", got)
	}
	if got := headers.Get("User-Agent"); got == "" || got == "Go-http-client/1.1" {
		t.Fatalf("User-Agent = %q, want non-generic agent", got)
	}
}

func TestApplyOpenCodeHeadersDerivesStableSession(t *testing.T) {
	provider := store.Provider{BaseURL: "https://opencode.ai/zen/go/v1"}
	turn1 := canonical.Request{RequestID: "req-1", Messages: []canonical.Message{
		{Role: "system", Content: "you are a coder"},
		{Role: "user", Content: "hello"},
	}}
	turn2 := canonical.Request{RequestID: "req-2", Messages: []canonical.Message{
		{Role: "system", Content: "you are a coder"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "user", Content: "next question"},
	}}
	other := canonical.Request{RequestID: "req-3", Messages: []canonical.Message{
		{Role: "user", Content: "different conversation"},
	}}

	h1 := http.Header{}
	applyOpenCodeHeaders(h1, provider, turn1)
	h2 := http.Header{}
	applyOpenCodeHeaders(h2, provider, turn2)
	h3 := http.Header{}
	applyOpenCodeHeaders(h3, provider, other)

	if h1.Get("x-opencode-session") == "" {
		t.Fatal("derived session must not be empty")
	}
	if h1.Get("x-opencode-session") != h2.Get("x-opencode-session") {
		t.Fatal("same conversation must derive the same session across turns")
	}
	if h1.Get("x-opencode-session") == h3.Get("x-opencode-session") {
		t.Fatal("different conversations must derive different sessions")
	}
}

func TestApplyOpenCodeHeadersRespectsProviderConfig(t *testing.T) {
	headers := http.Header{}
	headers.Set("x-opencode-session", "configured-session")
	applyOpenCodeHeaders(headers, store.Provider{BaseURL: "https://opencode.ai/zen/go/v1"}, canonical.Request{RequestID: "req-1"})
	if got := headers.Get("x-opencode-session"); got != "configured-session" {
		t.Fatalf("provider-configured header was overwritten: %q", got)
	}
}

func TestApplyOpenCodeHeadersFallsBackToRequestID(t *testing.T) {
	headers := http.Header{}
	applyOpenCodeHeaders(headers, store.Provider{BaseURL: "https://opencode.ai/zen/go/v1"}, canonical.Request{RequestID: "req-9"})
	if got := headers.Get("x-opencode-session"); got != "req-9" {
		t.Fatalf("x-opencode-session = %q, want request ID fallback", got)
	}
}
