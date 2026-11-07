package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tproxy/tproxy/internal/security"
)

var linkUpgrader = websocket.Upgrader{
	HandshakeTimeout: linkHandshakeTimeout,
	ReadBufferSize:   32 << 10,
	WriteBufferSize:  32 << 10,
	// Members are servers dialing out, never browsers. Refusing any Origin
	// keeps a web page from opening a link with a token it should never hold.
	CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" },
}

type linkConnKey struct{}

// unencryptedHTTP2 selects HTTP/2 with prior knowledge: the WebSocket under a
// link is already authenticated and, on https hubs, encrypted.
func unencryptedHTTP2() *http.Protocols {
	protocols := &http.Protocols{}
	protocols.SetUnencryptedHTTP2(true)
	return protocols
}

// newLinkTransport builds the hub's HTTP/2 client for links. It never dials:
// NewClientConn receives the member's connection through the context. The
// HTTP/2 health check closes a link whose member stops answering PINGs.
func newLinkTransport(pingInterval, pingTimeout time.Duration) *http.Transport {
	return &http.Transport{
		Protocols: unencryptedHTTP2(),
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			if conn, ok := ctx.Value(linkConnKey{}).(net.Conn); ok {
				return conn, nil
			}
			return nil, errors.New("cluster: links are dialed by members")
		},
		DisableCompression: true,
		HTTP2: &http.HTTP2Config{
			MaxReadFrameSize: linkFrameSize,
			SendPingTimeout:  pingInterval,
			PingTimeout:      pingTimeout,
		},
	}
}

// hubLink is the hub's HTTP/2 client connection to one member, carried by the
// WebSocket the member dialed.
type hubLink struct {
	nodeID      string
	name        string
	version     string
	addr        string
	conn        *wsConn
	cc          *http.ClientConn
	connectedAt time.Time
	rttMS       atomic.Int64
	streams     chan struct{}
	waitTimeout time.Duration
	finished    chan struct{} // closed once the supervisor has cleaned up
}

func (l *hubLink) close() {
	_ = l.cc.Close()
	_ = l.conn.Close()
}

type linkRegistry struct {
	mu     sync.Mutex
	byNode map[string]*hubLink
}

// remove unregisters l unless a newer link already replaced it.
func (r *linkRegistry) remove(l *hubLink) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byNode[l.nodeID] != l {
		return false
	}
	delete(r.byNode, l.nodeID)
	return true
}

func (r *linkRegistry) get(nodeID string) *hubLink {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byNode[nodeID]
}

func (r *linkRegistry) takeAll() []*hubLink {
	r.mu.Lock()
	defer r.mu.Unlock()
	links := make([]*hubLink, 0, len(r.byNode))
	for id, l := range r.byNode {
		links = append(links, l)
		delete(r.byNode, id)
	}
	return links
}

func (r *linkRegistry) all() []*hubLink {
	r.mu.Lock()
	defer r.mu.Unlock()
	links := make([]*hubLink, 0, len(r.byNode))
	for _, l := range r.byNode {
		links = append(links, l)
	}
	return links
}

// RoundTripper returns the transport bound to nodeID's current link.
func (r *linkRegistry) RoundTripper(nodeID string) (http.RoundTripper, bool) {
	l := r.get(nodeID)
	if l == nil {
		return nil, false
	}
	return l, true
}

func (s *Service) hubLinkHandler(w http.ResponseWriter, r *http.Request) {
	node, ok := s.authNode(w, r)
	if !ok {
		return
	}
	if r.Header.Get(HeaderProtocol) != ProtocolVersion {
		writeError(w, http.StatusUpgradeRequired, CodeProtocolMismatch, "cluster protocol "+ProtocolVersion+" is required")
		return
	}
	if !websocket.IsWebSocketUpgrade(r) {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "WebSocket upgrade required")
		return
	}
	ws, err := linkUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn := newWSConn(ws)
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), linkConnKey{}, net.Conn(conn)), linkHandshakeTimeout)
	cc, err := s.h2.NewClientConn(ctx, "http", linkHost(node.ID)+":80")
	cancel()
	if err != nil {
		_ = conn.Close()
		s.logf("[cluster] link from node %s: %v", node.ID, err)
		return
	}
	link := &hubLink{
		nodeID:      node.ID,
		name:        node.Name,
		version:     node.Version,
		addr:        remoteHost(r),
		conn:        conn,
		cc:          cc,
		connectedAt: s.now().UTC(),
		streams:     make(chan struct{}, maxLinkStreams),
		finished:    make(chan struct{}),
	}
	if !s.addLink(link, HashSecret(security.BearerToken(r))) {
		link.close()
	}
}

func (s *Service) addLink(l *hubLink, tokenHash string) bool {
	s.links.mu.Lock()
	s.mu.Lock()
	if s.stopped || s.dissolving || s.membership.Role != RoleHub {
		s.mu.Unlock()
		s.links.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	node, err := s.hubStore.NodeByTokenHash(ctx, tokenHash)
	cancel()
	if err != nil || node.ID != l.nodeID || node.Status != NodeActive {
		s.links.mu.Unlock()
		s.mu.Unlock()
		return false
	}
	old := s.links.byNode[l.nodeID]
	s.links.byNode[l.nodeID] = l
	s.links.mu.Unlock()
	s.linkWG.Add(1)
	s.mu.Unlock()
	if old != nil {
		old.close()
	}
	go s.superviseLink(l)
	return true
}

// superviseLink measures the member's round trip until the link dies, then
// records it as offline. It runs for as long as the link is registered.
func (s *Service) superviseLink(l *hubLink) {
	defer s.linkWG.Done()
	defer close(l.finished)
	s.logf("[cluster] node %s (%s) linked from %s", l.nodeID, l.name, l.addr)
	s.linkOpened(l)
	ticker := time.NewTicker(s.pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-l.conn.Done():
			s.linkClosed(l, nil)
			return
		case <-ticker.C:
			if err := s.pingLink(l); err != nil {
				l.close()
				s.linkClosed(l, err)
				return
			}
		}
	}
}

// pingLink times a minimal request over the link. A link at its stream limit
// is plainly alive and is not probed, so load never reads as a dead member;
// HTTP/2 PINGs from the transport's health check cover that case.
func (s *Service) pingLink(l *hubLink) error {
	if l.cc.Available() == 0 {
		return l.cc.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.pingTimeout)
	defer cancel()
	start := time.Now()
	resp, err := s.linkRequest(ctx, l, http.MethodGet, "ping", nil)
	if err != nil {
		return err
	}
	drain(resp.Body)
	l.rttMS.Store(time.Since(start).Milliseconds())
	return nil
}

func (s *Service) linkOpened(l *hubLink) {
	if err := s.pingLink(l); err != nil {
		l.close()
		return
	}
	if !s.isActiveHub() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	if err := s.hubStore.TouchNode(ctx, l.nodeID, l.addr, l.version, s.now()); err != nil {
		s.logf("[cluster] touch node %s: %v", l.nodeID, err)
	}
	s.refreshAndNotify(ctx)
	// Fetch the new member's inventory now rather than on the next tick.
	s.kick()
}

func (s *Service) linkClosed(l *hubLink, cause error) {
	removed := s.links.remove(l)
	if !removed && s.links.get(l.nodeID) != nil {
		return // a newer link from the same node took over
	}
	if cause != nil {
		s.logf("[cluster] link to node %s lost: %v", l.nodeID, cause)
	} else {
		s.logf("[cluster] node %s disconnected", l.nodeID)
	}
	s.mu.Lock()
	delete(s.inventories, l.nodeID)
	s.mu.Unlock()
	if !s.isActiveHub() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	if err := s.hubStore.TouchNode(ctx, l.nodeID, l.addr, l.version, s.now()); err != nil {
		s.logf("[cluster] touch node %s: %v", l.nodeID, err)
	}
	s.refreshAndNotify(ctx)
}

func (s *Service) isActiveHub() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.stopped && !s.dissolving && s.membership.Role == RoleHub
}

func (s *Service) closeLinks() {
	for _, l := range s.links.takeAll() {
		l.close()
	}
}

func (s *Service) linkRequest(ctx context.Context, l *hubLink, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+linkHost(l.nodeID)+LinkPathPrefix+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return l.RoundTrip(req)
}

func (s *Service) notifyLink(ctx context.Context, l *hubLink, event string) error {
	resp, err := s.linkRequest(ctx, l, http.MethodPost, "notify", map[string]string{"event": event})
	if err != nil {
		return err
	}
	drain(resp.Body)
	if resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("notify %s: status %d", l.nodeID, resp.StatusCode)
	}
	return nil
}

// notifyMembers tells every linked member to poll now. Delivery is best
// effort; members poll on their own schedule anyway.
func (s *Service) notifyMembers(event string) {
	for _, l := range s.links.all() {
		s.goBackground(func() {
			ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
			defer cancel()
			_ = s.notifyLink(ctx, l, event)
		})
	}
}

// notifyAndWait delivers event to every given link before returning, for
// messages that must arrive before the links close.
func (s *Service) notifyAndWait(links []*hubLink, event string) {
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	var wg sync.WaitGroup
	for _, l := range links {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.notifyLink(ctx, l, event); err != nil {
				s.logf("[cluster] notify node %s: %v", l.nodeID, err)
			}
		}()
	}
	wg.Wait()
}

// RoundTrip reserves a stream until its response body has been consumed.
func (l *hubLink) RoundTrip(req *http.Request) (*http.Response, error) {
	timeout := l.waitTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var done <-chan struct{}
	if l.conn != nil {
		done = l.conn.Done()
	}
	select {
	case <-done:
		closeRequestBody(req)
		return syntheticResponse(req, http.StatusServiceUnavailable, CodeNodeOffline, "cluster node is offline"), nil
	case <-req.Context().Done():
		closeRequestBody(req)
		return nil, req.Context().Err()
	case <-timer.C:
		closeRequestBody(req)
		return syntheticResponse(req, http.StatusServiceUnavailable, CodeNodeBusy, "cluster node is busy"), nil
	case l.streams <- struct{}{}:
	}
	resp, err := l.cc.RoundTrip(req)
	if err != nil {
		<-l.streams
		return nil, err
	}
	resp.Body = &streamBody{ReadCloser: resp.Body, release: func() { <-l.streams }}
	return resp, nil
}

type streamBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *streamBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.once.Do(b.release)
	}
	return n, err
}
func (b *streamBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}
