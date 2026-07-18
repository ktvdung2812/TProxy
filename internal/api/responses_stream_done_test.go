package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
)

func TestWriteResponsesStreamEndsWithDone(t *testing.T) {
	events := make(chan canonical.Event, 4)
	events <- canonical.Event{
		Type:     canonical.EventResponsesSSE,
		SSEEvent: "response.completed",
		SSEData:  []byte(`{"type":"response.completed","response":{"status":"completed"}}`),
	}
	close(events)

	rec := httptest.NewRecorder()
	writeResponsesStream(rec, httptest.NewRequest("POST", "/v1/responses", nil), events, "req_done", "gpt-5.6-terra")

	body := rec.Body.String()
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("missing [DONE] in stream:\n%s", body)
	}
}
