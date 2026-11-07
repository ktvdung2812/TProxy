package cluster

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// wsPair returns the client end of a WebSocket whose server end echoes every
// byte back through its own wsConn.
func wsPair(t *testing.T) *wsConn {
	t.Helper()
	serverDone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := linkUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn := newWSConn(ws)
		go func() {
			defer close(serverDone)
			defer conn.Close()
			_, _ = io.Copy(conn, conn)
		}()
	}))
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	conn := newWSConn(ws)
	t.Cleanup(func() {
		_ = conn.Close()
		select {
		case <-serverDone:
		case <-time.After(5 * time.Second):
			t.Error("echo server did not stop")
		}
		srv.Close()
	})
	return conn
}

func TestWSConnEcho(t *testing.T) {
	conn := wsPair(t)
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte(" world")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("hello world"))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello world" {
		t.Fatalf("echo = %q", got)
	}
	if n, err := conn.Read(nil); n != 0 || err != nil {
		t.Fatalf("empty read = %d, %v", n, err)
	}
}

func TestWSConnLargeWrite(t *testing.T) {
	conn := wsPair(t)
	payload := make([]byte, 3<<20)
	_, _ = rand.Read(payload)
	errc := make(chan error, 1)
	go func() {
		_, err := conn.Write(payload)
		errc <- err
	}()
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("large payload corrupted")
	}
}

func TestWSConnConcurrentWrites(t *testing.T) {
	conn := wsPair(t)
	const writers, perWriter, size = 8, 40, 1024
	go func() {
		var wg sync.WaitGroup
		for w := range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range perWriter {
					record := bytes.Repeat([]byte{byte(w)}, size)
					binary.BigEndian.PutUint32(record, uint32(w*perWriter+i))
					if _, err := conn.Write(record); err != nil {
						return
					}
				}
			}()
		}
		wg.Wait()
	}()
	seen := map[uint32]bool{}
	record := make([]byte, size)
	for range writers * perWriter {
		if _, err := io.ReadFull(conn, record); err != nil {
			t.Fatal(err)
		}
		id := binary.BigEndian.Uint32(record)
		writer := byte(id / perWriter)
		if !bytes.Equal(record[4:], bytes.Repeat([]byte{writer}, size-4)) {
			t.Fatalf("record %d was interleaved with another write", id)
		}
		seen[id] = true
	}
	if len(seen) != writers*perWriter {
		t.Fatalf("saw %d distinct records", len(seen))
	}
}

func TestWSConnCloseSignalsDone(t *testing.T) {
	conn := wsPair(t)
	select {
	case <-conn.Done():
		t.Fatal("done before close")
	default:
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	select {
	case <-conn.Done():
	case <-time.After(time.Second):
		t.Fatal("done not closed")
	}
	if _, err := conn.Write([]byte("x")); err == nil {
		t.Fatal("write after close succeeded")
	}
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("read after close succeeded")
	}
}

func TestWSConnPeerCloseSignalsDone(t *testing.T) {
	accepted := make(chan *wsConn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := linkUpgrader.Upgrade(w, r, nil)
		if err == nil {
			accepted <- newWSConn(ws)
		}
	}))
	defer srv.Close()
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := newWSConn(ws)
	defer client.Close()
	server := <-accepted
	readErr := make(chan error, 1)
	go func() {
		_, err := server.Read(make([]byte, 16))
		readErr <- err
	}()
	_ = client.Close()
	select {
	case err := <-readErr:
		if err == nil {
			t.Fatal("read succeeded after peer closed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read did not fail after peer closed")
	}
	select {
	case <-server.Done():
	case <-time.After(time.Second):
		t.Fatal("server conn not done after peer closed")
	}
}
