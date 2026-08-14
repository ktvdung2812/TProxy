package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

func antigravityFallbackCredential() store.Credential {
	return store.Credential{AuthType: "oauth", OAuthToken: &store.OAuthToken{Extra: map[string]any{"project_id": "p"}}}
}

// Measured against a live account: prod answered 429 for over an hour while
// daily served the same request normally, so daily is tried first.
func TestAntigravityPrefersDailyHost(t *testing.T) {
	hosts := antigravityBaseURLs(store.Provider{Type: "antigravity", BaseURL: antigravityProdBaseURL})
	if len(hosts) != 2 {
		t.Fatalf("hosts = %v, want two", hosts)
	}
	if hosts[0] != antigravityDailyBaseURL {
		t.Fatalf("first host = %q, want daily", hosts[0])
	}
	if hosts[1] != antigravityProdBaseURL {
		t.Fatalf("second host = %q, want prod", hosts[1])
	}
}

func TestAntigravityDefaultsAlsoApplyToAnEmptyBaseURL(t *testing.T) {
	if hosts := antigravityBaseURLs(store.Provider{Type: "antigravity"}); len(hosts) != 2 {
		t.Fatalf("hosts = %v, want the default pair", hosts)
	}
}

// An operator who pinned a host meant it; falling back would defeat the point.
func TestAntigravityHonoursACustomHostExactly(t *testing.T) {
	hosts := antigravityBaseURLs(store.Provider{Type: "antigravity", BaseURL: "https://my-proxy.internal"})
	if len(hosts) != 1 || hosts[0] != "https://my-proxy.internal" {
		t.Fatalf("hosts = %v, want only the configured host", hosts)
	}
}

// antigravityFallbackServers stands up two hosts and reports which were called.
func antigravityFallbackServers(t *testing.T, first http.HandlerFunc, second http.HandlerFunc) (*antigravityAdapter, store.Provider, *[]string) {
	t.Helper()
	var called []string
	firstServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = append(called, "first")
		first(w, r)
	}))
	t.Cleanup(firstServer.Close)
	secondServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = append(called, "second")
		second(w, r)
	}))
	t.Cleanup(secondServer.Close)

	originalDaily, originalProd := antigravityDailyBaseURLForTest(firstServer.URL), antigravityProdBaseURLForTest(secondServer.URL)
	t.Cleanup(func() { originalDaily(); originalProd() })

	adapter := &antigravityAdapter{client: firstServer.Client()}
	return adapter, store.Provider{Type: "antigravity", BaseURL: ""}, &called
}

func antigravityOKHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"response":{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}}`))
}

func antigravityStatusHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func TestAntigravityFallsThroughOnRateLimit(t *testing.T) {
	adapter, provider, called := antigravityFallbackServers(t,
		antigravityStatusHandler(http.StatusTooManyRequests, `{"error":{"code":429,"message":"Resource has been exhausted (e.g. check quota)."}}`),
		antigravityOKHandler)

	response, err := adapter.Execute(context.Background(), provider, antigravityFallbackCredential(),
		canonical.Request{RequestID: "r", UpstreamModel: "gemini-3-flash"})
	if err != nil {
		t.Fatalf("request failed instead of falling through: %v", err)
	}
	if stringValue(response.Content) != "ok" {
		t.Fatalf("content = %q", stringValue(response.Content))
	}
	if len(*called) != 2 || (*called)[1] != "second" {
		t.Fatalf("hosts called = %v, want both in order", *called)
	}
}

// The hosts do not carry the same catalogue: the 3.7 Flash family answers 404
// on daily and is served by prod.
func TestAntigravityFallsThroughOnNotFound(t *testing.T) {
	adapter, provider, called := antigravityFallbackServers(t,
		antigravityStatusHandler(http.StatusNotFound, `{"error":{"code":404,"message":"model not found"}}`),
		antigravityOKHandler)

	if _, err := adapter.Execute(context.Background(), provider, antigravityFallbackCredential(),
		canonical.Request{RequestID: "r", UpstreamModel: "gemini-3.7-flash-high"}); err != nil {
		t.Fatalf("404 on the first host was not retried elsewhere: %v", err)
	}
	if len(*called) != 2 {
		t.Fatalf("hosts called = %v, want both", *called)
	}
}

// An auth failure travels with the request, so repeating it only burns quota.
func TestAntigravityDoesNotFallThroughOnAuthFailure(t *testing.T) {
	adapter, provider, called := antigravityFallbackServers(t,
		antigravityStatusHandler(http.StatusForbidden, `{"error":{"code":403,"message":"Verify your account to continue."}}`),
		antigravityOKHandler)

	_, err := adapter.Execute(context.Background(), provider, antigravityFallbackCredential(),
		canonical.Request{RequestID: "r", UpstreamModel: "gemini-3-flash"})
	if err == nil {
		t.Fatal("expected the auth failure to surface")
	}
	if Status(err) != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", Status(err))
	}
	if len(*called) != 1 {
		t.Fatalf("hosts called = %v, want only the first", *called)
	}
}

func TestAntigravityStopsAtTheFirstHostThatAnswers(t *testing.T) {
	adapter, provider, called := antigravityFallbackServers(t, antigravityOKHandler,
		func(w http.ResponseWriter, r *http.Request) { t.Error("second host must not be contacted") })

	if _, err := adapter.Execute(context.Background(), provider, antigravityFallbackCredential(),
		canonical.Request{RequestID: "r", UpstreamModel: "gemini-3-flash"}); err != nil {
		t.Fatal(err)
	}
	if len(*called) != 1 {
		t.Fatalf("hosts called = %v, want one", *called)
	}
}

// When every host refuses, the caller must see the real upstream failure.
func TestAntigravityReportsTheLastFailureWhenAllHostsRefuse(t *testing.T) {
	adapter, provider, called := antigravityFallbackServers(t,
		antigravityStatusHandler(http.StatusNotFound, `{"error":{"code":404,"message":"model not found"}}`),
		antigravityStatusHandler(http.StatusTooManyRequests, `{"error":{"code":429,"message":"Resource has been exhausted (e.g. check quota)."}}`))

	_, err := adapter.Execute(context.Background(), provider, antigravityFallbackCredential(),
		canonical.Request{RequestID: "r", UpstreamModel: "gemini-3.7-flash-high"})
	if Status(err) != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want the final host's 429 rather than the first host's 404", Status(err))
	}
	if len(*called) != 2 {
		t.Fatalf("hosts called = %v", *called)
	}
}

func TestAntigravityStreamFallsThroughToo(t *testing.T) {
	adapter, provider, called := antigravityFallbackServers(t,
		antigravityStatusHandler(http.StatusTooManyRequests, `{"error":{"code":429,"message":"Resource has been exhausted (e.g. check quota)."}}`),
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}]},\"finishReason\":\"STOP\"}]}}\n\n"))
		})

	events, err := adapter.ExecuteStream(context.Background(), provider, antigravityFallbackCredential(),
		canonical.Request{RequestID: "r", UpstreamModel: "gemini-3-flash", Stream: true})
	if err != nil {
		t.Fatalf("stream did not fall through: %v", err)
	}
	var text strings.Builder
	for event := range events {
		if event.Type == canonical.EventTextDelta {
			text.WriteString(event.Text)
		}
	}
	if text.String() != "hi" {
		t.Fatalf("streamed text = %q", text.String())
	}
	if len(*called) != 2 {
		t.Fatalf("hosts called = %v, want both", *called)
	}
}

// antigravityDailyBaseURLForTest and antigravityProdBaseURLForTest redirect the
// fallback at local servers and return restore functions.
func antigravityDailyBaseURLForTest(url string) func() {
	original := antigravityDailyBaseURL
	antigravityDailyBaseURL = url
	return func() { antigravityDailyBaseURL = original }
}

func antigravityProdBaseURLForTest(url string) func() {
	original := antigravityProdBaseURL
	antigravityProdBaseURL = url
	return func() { antigravityProdBaseURL = original }
}
