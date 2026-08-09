package api

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// parsePositiveDuration reads an optional interval; anything invalid or
// non-positive disables the feature rather than failing startup.
func parsePositiveDuration(raw string) time.Duration {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0
	}
	parsed, err := time.ParseDuration(trimmed)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

// Long model turns can go a minute or more without producing a byte — reasoning
// models especially. Load balancers, corporate proxies and some client HTTP
// stacks drop an idle connection well before that, which the user sees as a
// truncated stream or a hung request. These helpers emit protocol-legal filler
// while the upstream is quiet.

// keepaliveWriter wraps an SSE response writer and emits a comment line
// (": keepalive") whenever nothing has been written for the configured
// interval. Writes are serialised because the ticker runs on its own goroutine.
type keepaliveWriter struct {
	inner    http.ResponseWriter
	flusher  http.Flusher
	interval time.Duration

	mu       sync.Mutex
	lastByte time.Time
	stopped  bool

	stop chan struct{}
	done chan struct{}
}

// newKeepaliveWriter returns w unchanged when keepalives are disabled, so the
// zero-config path keeps its original behaviour and cost.
func newKeepaliveWriter(w http.ResponseWriter, interval time.Duration) (http.ResponseWriter, func()) {
	if interval <= 0 {
		return w, func() {}
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		return w, func() {}
	}
	writer := &keepaliveWriter{
		inner:    w,
		flusher:  flusher,
		interval: interval,
		lastByte: time.Now(),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go writer.run()
	return writer, writer.close
}

func (k *keepaliveWriter) Header() http.Header { return k.inner.Header() }

func (k *keepaliveWriter) WriteHeader(status int) { k.inner.WriteHeader(status) }

func (k *keepaliveWriter) Write(data []byte) (int, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.lastByte = time.Now()
	return k.inner.Write(data)
}

func (k *keepaliveWriter) Flush() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.flusher.Flush()
}

func (k *keepaliveWriter) run() {
	defer close(k.done)
	ticker := time.NewTicker(k.interval)
	defer ticker.Stop()
	for {
		select {
		case <-k.stop:
			return
		case now := <-ticker.C:
			k.mu.Lock()
			if k.stopped {
				k.mu.Unlock()
				return
			}
			if now.Sub(k.lastByte) < k.interval {
				k.mu.Unlock()
				continue
			}
			// An SSE comment is ignored by every compliant client but still resets
			// idle timers along the path.
			if _, err := k.inner.Write([]byte(": keepalive\n\n")); err != nil {
				k.stopped = true
				k.mu.Unlock()
				return
			}
			k.flusher.Flush()
			k.lastByte = now
			k.mu.Unlock()
		}
	}
}

func (k *keepaliveWriter) close() {
	k.mu.Lock()
	if k.stopped {
		k.mu.Unlock()
		<-k.done
		return
	}
	k.stopped = true
	k.mu.Unlock()
	close(k.stop)
	<-k.done
}

// startNonStreamKeepalive writes newlines while a non-streaming request waits on
// the upstream. Leading whitespace is insignificant in JSON, so the eventual
// body still parses. It returns a stop function that must run before the real
// payload is written.
func startNonStreamKeepalive(w http.ResponseWriter, interval time.Duration) func() {
	if interval <= 0 {
		return func() {}
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		return func() {}
	}
	// Headers must be settled before the first byte, otherwise the status and
	// content type are locked in by the first newline.
	w.Header().Set("Content-Type", "application/json")

	var mu sync.Mutex
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				mu.Lock()
				if _, err := w.Write([]byte("\n")); err != nil {
					mu.Unlock()
					return
				}
				flusher.Flush()
				mu.Unlock()
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			<-done
		})
	}
}
