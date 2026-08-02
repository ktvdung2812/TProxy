package providers

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

func TestNormalizeGrokSSOCookie(t *testing.T) {
	if got := normalizeGrokSSOCookie("sso=abc123"); got != "abc123" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeGrokSSOCookie("  rawtoken  "); got != "rawtoken" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizePplxSessionCookie(t *testing.T) {
	if got := normalizePplxSessionCookie("__Secure-next-auth.session-token=tok"); got != "tok" {
		t.Fatalf("got %q", got)
	}
	if got := normalizePplxSessionCookie("next-auth.session-token=tok2"); got != "tok2" {
		t.Fatalf("got %q", got)
	}
}

func TestFlattenMessagesForWeb(t *testing.T) {
	msg := flattenMessagesForWeb(canonical.Request{
		Messages: []canonical.Message{
			{Role: "system", Content: "be brief"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
			{Role: "user", Content: "again"},
		},
	})
	if !strings.Contains(msg, "system: be brief") || !strings.Contains(msg, "assistant: hi") {
		t.Fatalf("history formatting = %q", msg)
	}
	if !strings.HasSuffix(strings.TrimSpace(msg), "again") {
		t.Fatalf("last user should be unprefixed: %q", msg)
	}
}

func TestGrokWebAdapterStreamsTokens(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "sso=test-sso" {
			t.Errorf("cookie = %q", r.Header.Get("Cookie"))
		}
		if r.Header.Get("Origin") != "https://grok.com" {
			t.Errorf("origin = %q", r.Header.Get("Origin"))
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["modelName"] != "grok-4" {
			t.Errorf("modelName = %#v", body["modelName"])
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, `{"result":{"response":{"token":"Hel"}}}`+"\n")
		_, _ = io.WriteString(w, `{"result":{"response":{"token":"lo"}}}`+"\n")
		_, _ = io.WriteString(w, `{"result":{"response":{"modelResponse":{"message":"Hello"}}}}`+"\n")
	}))
	defer upstream.Close()

	adapter := &grokWebAdapter{client: upstream.Client()}
	events, err := adapter.ExecuteStream(context.Background(),
		store.Provider{ID: "grok-web", Type: "grok-web", BaseURL: upstream.URL},
		store.Credential{Secret: "sso=test-sso"},
		canonical.Request{UpstreamModel: "grok-4", Messages: []canonical.Message{{Role: "user", Content: "hi"}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for event := range events {
		if event.Type == canonical.EventError {
			t.Fatalf("error event: %v", event.Err)
		}
		if event.Type == canonical.EventTextDelta {
			text.WriteString(event.Text)
		}
	}
	if !strings.Contains(text.String(), "Hel") && !strings.Contains(text.String(), "Hello") {
		t.Fatalf("text = %q", text.String())
	}
}

func TestPerplexityWebAdapterStreamsMarkdown(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Cookie"), "__Secure-next-auth.session-token=sess") {
			t.Errorf("cookie = %q", r.Header.Get("Cookie"))
		}
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("accept = %q", r.Header.Get("Accept"))
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		params, _ := body["params"].(map[string]any)
		if params["model_preference"] != "claude46sonnet" {
			t.Errorf("model_preference = %#v", params["model_preference"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		payload := map[string]any{
			"blocks": []any{
				map[string]any{
					"intended_usage": "ask_text_markdown",
					"markdown_block": map[string]any{
						"progress": "PARTIAL",
						"chunks":   []any{"Perplexity ", "says hi"},
					},
				},
			},
			"backend_uuid": "uuid-1",
		}
		raw, _ := json.Marshal(payload)
		_, _ = io.WriteString(w, "data: "+string(raw)+"\n\n")
		done := map[string]any{"status": "COMPLETED", "backend_uuid": "uuid-1"}
		rawDone, _ := json.Marshal(done)
		_, _ = io.WriteString(w, "data: "+string(rawDone)+"\n\n")
	}))
	defer upstream.Close()

	adapter := &perplexityWebAdapter{client: upstream.Client()}
	events, err := adapter.ExecuteStream(context.Background(),
		store.Provider{ID: "perplexity-web", Type: "perplexity-web", BaseURL: upstream.URL},
		store.Credential{Secret: "sess"},
		canonical.Request{UpstreamModel: "pplx-sonnet", Messages: []canonical.Message{{Role: "user", Content: "hello"}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for event := range events {
		if event.Type == canonical.EventError {
			t.Fatalf("error: %v", event.Err)
		}
		if event.Type == canonical.EventTextDelta {
			text.WriteString(event.Text)
		}
	}
	if !strings.Contains(text.String(), "Perplexity") {
		t.Fatalf("text = %q", text.String())
	}
}

func TestCleanPplxResponseStripsCitations(t *testing.T) {
	got := cleanPplxResponse("Hello [1] world [2]", true)
	if strings.Contains(got, "[1]") || strings.Contains(got, "[2]") {
		t.Fatalf("got %q", got)
	}
}

func TestGrokWebModelMapDefault(t *testing.T) {
	if _, ok := grokWebModelMap["grok-4.1-fast"]; !ok {
		t.Fatal("default model missing")
	}
	// ensure scanner helper still available for compile
	_ = bufio.NewScanner
}

func TestRegistryHasWebCookieAdapters(t *testing.T) {
	reg := NewRegistry()
	for _, typ := range []string{"grok-web", "perplexity-web"} {
		if _, err := reg.Adapter(typ); err != nil {
			t.Fatalf("%s adapter missing: %v", typ, err)
		}
		caps := reg.Capabilities(typ)
		if len(caps) == 0 {
			t.Fatalf("%s capabilities empty", typ)
		}
	}
}
