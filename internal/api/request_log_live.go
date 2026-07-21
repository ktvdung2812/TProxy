package api

import (
	"sync"

	"github.com/tproxy/tproxy/internal/store"
)

const defaultLiveRequestLogLimit = 200

// LiveRequestLogBuffer keeps recent request logs in memory for real-time dashboards.
type LiveRequestLogBuffer struct {
	mu    sync.Mutex
	limit int
	items []store.RequestLog
	subs  map[chan struct{}]struct{}
}

func NewLiveRequestLogBuffer(limit int) *LiveRequestLogBuffer {
	if limit <= 0 {
		limit = defaultLiveRequestLogLimit
	}
	return &LiveRequestLogBuffer{limit: limit, subs: make(map[chan struct{}]struct{})}
}

func (b *LiveRequestLogBuffer) Push(item store.RequestLog) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.items = append([]store.RequestLog{item}, b.items...)
	if len(b.items) > b.limit {
		b.items = b.items[:b.limit]
	}
	for ch := range b.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (b *LiveRequestLogBuffer) Recent(limit int) []store.RequestLog {
	return b.RecentByCredential("", limit)
}

func (b *LiveRequestLogBuffer) RecentByCredential(credentialID string, limit int) []store.RequestLog {
	if limit <= 0 {
		limit = 50
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if credentialID == "" {
		if len(b.items) <= limit {
			out := make([]store.RequestLog, len(b.items))
			copy(out, b.items)
			return out
		}
		out := make([]store.RequestLog, limit)
		copy(out, b.items[:limit])
		return out
	}
	out := make([]store.RequestLog, 0, limit)
	for _, item := range b.items {
		if item.CredentialID != credentialID {
			continue
		}
		out = append(out, item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (b *LiveRequestLogBuffer) Subscribe() (notify <-chan struct{}, unsubscribe func()) {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}
