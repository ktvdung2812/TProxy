package api

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestKeepaliveWriterEmitsCommentsWhileIdle(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer, stop := newKeepaliveWriter(recorder, 20*time.Millisecond)
	time.Sleep(90 * time.Millisecond)
	stop()

	body := recorder.Body.String()
	if !strings.Contains(body, ": keepalive\n\n") {
		t.Fatalf("expected SSE keepalive comments, got %q", body)
	}
	if writer == recorder {
		t.Error("a positive interval must wrap the writer")
	}
}

// Real output must reset the idle timer, otherwise a busy stream is padded with
// pointless comment lines.
func TestKeepaliveWriterStaysQuietWhileDataFlows(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer, stop := newKeepaliveWriter(recorder, 40*time.Millisecond)
	deadline := time.Now().Add(120 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := writer.Write([]byte("data: x\n\n")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()

	if strings.Contains(recorder.Body.String(), "keepalive") {
		t.Errorf("a busy stream must not be padded: %q", recorder.Body.String())
	}
}

func TestKeepaliveDisabledReturnsOriginalWriter(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer, stop := newKeepaliveWriter(recorder, 0)
	stop()
	if writer != recorder {
		t.Error("a zero interval must leave the writer untouched")
	}
	if recorder.Body.Len() != 0 {
		t.Errorf("nothing should have been written: %q", recorder.Body.String())
	}
}

// The filler for non-streaming replies must keep the eventual body parseable.
func TestNonStreamKeepaliveWritesJSONSafeFiller(t *testing.T) {
	recorder := httptest.NewRecorder()
	stop := startNonStreamKeepalive(recorder, 20*time.Millisecond)
	time.Sleep(70 * time.Millisecond)
	stop()
	_, _ = recorder.Write([]byte(`{"ok":true}`))

	body := recorder.Body.String()
	if !strings.HasPrefix(body, "\n") {
		t.Fatalf("expected leading newlines, got %q", body)
	}
	if strings.TrimLeft(body, "\n") != `{"ok":true}` {
		t.Errorf("filler must only be whitespace, got %q", body)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content type = %q", got)
	}
}

func TestParsePositiveDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"":       0,
		"  ":     0,
		"0":      0,
		"-5s":    0,
		"banana": 0,
		"15s":    15 * time.Second,
		" 2m ":   2 * time.Minute,
	}
	for raw, want := range cases {
		if got := parsePositiveDuration(raw); got != want {
			t.Errorf("parsePositiveDuration(%q) = %v, want %v", raw, got, want)
		}
	}
}
