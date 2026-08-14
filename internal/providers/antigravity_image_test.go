package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

func antigravityImageCredential() store.Credential {
	return store.Credential{AuthType: "oauth", OAuthToken: &store.OAuthToken{Extra: map[string]any{"project_id": "cloud-project"}}}
}

// Cloud Code routes image generation through a separate request type; sending
// the agent type makes it reject or ignore the generation request.
func TestAntigravityImageModelUsesImageRequestType(t *testing.T) {
	imageRequestID := regexp.MustCompile(`^image_gen/\d+/[0-9a-f-]{36}/12$`)
	for _, model := range []string{"gemini-3-pro-image", "gemini-3.1-flash-image", "Gemini-Imagen-4"} {
		body, err := antigravityBody(canonical.Request{RequestID: "request", UpstreamModel: model}, antigravityImageCredential())
		if err != nil {
			t.Fatal(err)
		}
		if body["requestType"] != "image_gen" {
			t.Errorf("%s requestType=%#v, want image_gen", model, body["requestType"])
		}
		requestID, _ := body["requestId"].(string)
		if !imageRequestID.MatchString(requestID) {
			t.Errorf("%s requestId=%q does not match the IDE image_gen form", model, requestID)
		}
	}
}

func TestAntigravityTextModelKeepsAgentRequestType(t *testing.T) {
	body, err := antigravityBody(canonical.Request{RequestID: "request", UpstreamModel: "gemini-3-pro"}, antigravityImageCredential())
	if err != nil {
		t.Fatal(err)
	}
	if body["requestType"] != "agent" {
		t.Fatalf("requestType=%#v, want agent", body["requestType"])
	}
	// Cloud Code reads requestId as an IDE trace identifier:
	// agent/<conversation>/<millis>/<trajectory>/<step>.
	requestID, _ := body["requestId"].(string)
	agentRequestID := regexp.MustCompile(`^agent/[0-9a-f-]{36}/\d+/[0-9a-f-]{36}/\d+$`)
	if !agentRequestID.MatchString(requestID) {
		t.Fatalf("requestId=%q does not match the IDE agent form", requestID)
	}
}

// Cloud Code has no streaming image endpoint. A streaming caller must still be
// served, so the adapter issues a unary generateContent and replays it as
// events rather than calling streamGenerateContent and failing.
func TestAntigravityImageStreamUsesUnaryEndpoint(t *testing.T) {
	var paths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if payload["requestType"] != "image_gen" {
			t.Errorf("upstream requestType=%#v, want image_gen", payload["requestType"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{"candidates":[{"content":{"parts":[{"text":"an image"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2}}}`))
	}))
	defer upstream.Close()

	adapter := &antigravityAdapter{client: upstream.Client()}
	provider := store.Provider{Type: "antigravity", BaseURL: upstream.URL}
	events, err := adapter.ExecuteStream(context.Background(), provider, antigravityImageCredential(), canonical.Request{
		RequestID: "request", UpstreamModel: "gemini-3-pro-image", Stream: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	var text strings.Builder
	var sawStart, sawEnd bool
	for event := range events {
		switch event.Type {
		case canonical.EventMessageStart:
			sawStart = true
		case canonical.EventTextDelta:
			text.WriteString(event.Text)
		case canonical.EventMessageEnd:
			sawEnd = true
		}
	}
	if len(paths) != 1 || paths[0] != "/v1internal:generateContent" {
		t.Fatalf("upstream paths = %v, want a single unary generateContent call", paths)
	}
	if !sawStart || !sawEnd {
		t.Fatalf("stream missing start/end events (start=%v end=%v)", sawStart, sawEnd)
	}
	if text.String() != "an image" {
		t.Fatalf("streamed text = %q, want %q", text.String(), "an image")
	}
}

func TestAntigravityTextStreamStillUsesStreamingEndpoint(t *testing.T) {
	var paths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}]},\"finishReason\":\"STOP\"}]}}\n\n"))
	}))
	defer upstream.Close()

	adapter := &antigravityAdapter{client: upstream.Client()}
	provider := store.Provider{Type: "antigravity", BaseURL: upstream.URL}
	events, err := adapter.ExecuteStream(context.Background(), provider, antigravityImageCredential(), canonical.Request{
		RequestID: "request", UpstreamModel: "gemini-3-pro", Stream: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	if len(paths) != 1 || paths[0] != "/v1internal:streamGenerateContent" {
		t.Fatalf("upstream paths = %v, want streamGenerateContent", paths)
	}
}

// The reason lives in the response body, not a header, so the whole path from
// upstream JSON to the router's cooldown decision has to carry it.
func TestUpstreamErrorCarriesGoogleErrorInfoReason(t *testing.T) {
	body := []byte(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"Quota exceeded","details":[
		{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"QUOTA_EXHAUSTED"},
		{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"30s"}]}}`)
	err := upstreamBodyErrorForProvider(429, body, "", "")
	if got := Reason(err); got != "QUOTA_EXHAUSTED" {
		t.Fatalf("Reason = %q, want QUOTA_EXHAUSTED", got)
	}
	if got := RetryAfter(err); got != "30s" {
		t.Fatalf("RetryAfter = %q, want 30s", got)
	}
}

func TestUpstreamErrorWithoutErrorInfoHasNoReason(t *testing.T) {
	err := upstreamBodyErrorForProvider(429, []byte(`{"error":{"code":429,"message":"slow down"}}`), "", "")
	if got := Reason(err); got != "" {
		t.Fatalf("Reason = %q, want empty", got)
	}
}
