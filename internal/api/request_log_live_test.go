package api

import (
	"testing"
	"time"

	"github.com/tproxy/tproxy/internal/store"
)

func TestLiveRequestLogBufferRecentByCredential(t *testing.T) {
	buffer := NewLiveRequestLogBuffer(10)
	buffer.Push(store.RequestLog{RequestID: "a", CredentialID: "cred-a", Method: "POST", Path: "/v1/responses", Status: 200, CreatedAt: time.Now().UTC()})
	buffer.Push(store.RequestLog{RequestID: "b", CredentialID: "cred-b", Method: "POST", Path: "/v1/responses", Status: 502, CreatedAt: time.Now().UTC()})
	buffer.Push(store.RequestLog{RequestID: "c", CredentialID: "cred-a", Method: "GET", Path: "/v1/models", Status: 200, CreatedAt: time.Now().UTC()})

	filtered := buffer.RecentByCredential("cred-a", 10)
	if len(filtered) != 2 || filtered[0].RequestID != "c" || filtered[1].RequestID != "a" {
		t.Fatalf("filtered=%+v", filtered)
	}
}

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
