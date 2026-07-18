package api

import (
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/store"
)

func TestLiveRequestLogBufferRecentAndNotify(t *testing.T) {
	buffer := NewLiveRequestLogBuffer(3)
	notify, unsubscribe := buffer.Subscribe()
	defer unsubscribe()

	buffer.Push(store.RequestLog{RequestID: "a", Method: "GET", Path: "/one", Status: 200, CreatedAt: time.Now().UTC()})
	buffer.Push(store.RequestLog{RequestID: "b", Method: "GET", Path: "/two", Status: 200, CreatedAt: time.Now().UTC()})

	recent := buffer.Recent(10)
	if len(recent) != 2 || recent[0].RequestID != "b" || recent[1].RequestID != "a" {
		t.Fatalf("recent=%+v", recent)
	}

	buffer.Push(store.RequestLog{RequestID: "c", Method: "GET", Path: "/three", Status: 200, CreatedAt: time.Now().UTC()})
	buffer.Push(store.RequestLog{RequestID: "d", Method: "GET", Path: "/four", Status: 200, CreatedAt: time.Now().UTC()})

	recent = buffer.Recent(10)
	if len(recent) != 3 || recent[0].RequestID != "d" {
		t.Fatalf("trimmed recent=%+v", recent)
	}

	select {
	case <-notify:
	default:
		t.Fatal("expected notify after push")
	}
}
