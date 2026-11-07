package cluster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const maxLinkStreams = 256

var errLinkClosed = errors.New("hub link closed")

// memberLinkLoop keeps one link to the hub up, redialing with jittered
// exponential backoff, until the run ends or the hub rejects the node token.
func (s *Service) memberLinkLoop(run *roleRun) {
	defer run.wg.Done()
	backoff := s.minBackoff
	for run.ctx.Err() == nil {
		s.mu.Lock()
		m, gen, selfID := s.membership, s.gen, s.identity.ID
		s.mu.Unlock()
		up, revokedStatus, err := s.memberLinkOnce(run, m, selfID)
		if run.ctx.Err() != nil {
			return
		}
		if revokedStatus != 0 {
			s.handleRevoked(run, gen, revokedStatus, "link_unauthorized")
			return
		}
		if up >= s.stableLink {
			backoff = s.minBackoff
		}
		wait := jitter(backoff)
		s.setLinkState(run, LinkError, err)
		if up > 0 {
			s.logf("[cluster] hub link closed after %s; reconnecting in %s", up.Round(time.Second), wait.Round(time.Millisecond))
		} else {
			s.logf("[cluster] hub link: %v; retrying in %s", err, wait.Round(time.Millisecond))
		}
		timer := time.NewTimer(wait)
		select {
		case <-run.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff = min(2*backoff, s.maxBackoff)
	}
}

// memberLinkOnce dials the hub and serves HTTP/2 on the link until it dies.
// It returns how long the link was up and, when the hub refused the node
// token, the status it refused with.
func (s *Service) memberLinkOnce(run *roleRun, m Membership, selfID string) (time.Duration, int, error) {
	target, err := linkURL(m.HubURL)
	if err != nil {
		return 0, 0, err
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+m.NodeToken)
	header.Set(HeaderProtocol, ProtocolVersion)
	header.Set(HeaderNode, selfID)
	header.Set("User-Agent", "tproxy-cluster/"+s.version)
	s.setLinkState(run, LinkConnecting, nil)
	ctx, cancel := context.WithTimeout(run.ctx, linkHandshakeTimeout)
	ws, resp, err := s.dialer().DialContext(ctx, target, header)
	cancel()
	if err != nil {
		if resp != nil {
			if revokedResponse(resp.StatusCode, resp.Header) {
				return 0, resp.StatusCode, fmt.Errorf("dial hub: %s", resp.Status)
			}
			return 0, 0, fmt.Errorf("dial hub: %s", resp.Status)
		}
		return 0, 0, fmt.Errorf("dial hub: %w", err)
	}
	conn := newWSConn(ws)
	connectedAt := time.Now()
	s.setLinkConnected(run)
	s.logf("[cluster] linked to hub %s", m.HubURL)
	s.serveLink(run, conn)
	return time.Since(connectedAt), 0, errLinkClosed
}

// serveLink runs the member's HTTP/2 server on the link until the link dies
// or the run ends.
func (s *Service) serveLink(run *roleRun, conn *wsConn) {
	server := &http.Server{
		Handler:   s.memberLinkHandler(run, conn.Done()),
		Protocols: unencryptedHTTP2(),
		HTTP2: &http.HTTP2Config{
			MaxConcurrentStreams: maxLinkStreams,
			MaxReadFrameSize:     linkFrameSize,
			// The hub probes every PingInterval; silence for twice that means
			// the hub or the path to it is gone even if TCP has not noticed.
			SendPingTimeout: 2 * s.pingInterval,
			PingTimeout:     s.pingTimeout,
		},
		BaseContext: func(net.Listener) context.Context { return run.ctx },
		ErrorLog:    log.New(io.Discard, "", 0),
	}
	listener := newConnListener(conn)
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = server.Serve(listener)
	}()
	select {
	case <-conn.Done():
	case <-run.ctx.Done():
	}
	_ = server.Close()
	_ = conn.Close()
	<-served
}

// connListener hands one connection to http.Server.Serve.
type connListener struct {
	conns     chan net.Conn
	addr      net.Addr
	closed    chan struct{}
	closeOnce sync.Once
}

func newConnListener(conn net.Conn) *connListener {
	l := &connListener{conns: make(chan net.Conn, 1), addr: conn.LocalAddr(), closed: make(chan struct{})}
	l.conns <- conn
	return l
}

func (l *connListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *connListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *connListener) Addr() net.Addr { return l.addr }

func (s *Service) dialer() *websocket.Dialer {
	d := &websocket.Dialer{
		HandshakeTimeout: linkHandshakeTimeout,
		Proxy:            http.ProxyFromEnvironment,
		NetDialContext:   (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ReadBufferSize:   32 << 10,
		WriteBufferSize:  32 << 10,
	}
	if transport, ok := s.client.Transport.(*http.Transport); ok {
		d.Proxy = transport.Proxy
		if transport.DialContext != nil {
			d.NetDialContext = transport.DialContext
		}
		if transport.TLSClientConfig != nil {
			cfg := transport.TLSClientConfig.Clone()
			// net/http adds "h2" to a config it uses; the upgrade needs HTTP/1.1.
			cfg.NextProtos = nil
			d.TLSClientConfig = cfg
		}
	}
	return d
}

func linkURL(hubURL string) (string, error) {
	u, err := url.Parse(hubURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported hub URL scheme %q", u.Scheme)
	}
	u.Path = HubPathPrefix + "link"
	return u.String(), nil
}

func (s *Service) setLinkState(run *roleRun, state string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run != run {
		return
	}
	s.link.State = state
	s.link.ConnectedAt = time.Time{}
	if err != nil {
		s.link.LastError = err.Error()
	}
}

func (s *Service) setLinkConnected(run *roleRun) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run != run {
		return
	}
	s.link.State = LinkConnected
	s.link.ConnectedAt = s.now().UTC()
	s.link.LastError = ""
}

func (s *Service) memberStateLoop(run *roleRun) {
	defer run.wg.Done()
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		ctx, cancel := context.WithTimeout(run.ctx, syncTimeout)
		_ = s.pollMember(ctx, run)
		cancel()
		select {
		case <-run.ctx.Done():
			return
		case <-ticker.C:
		case <-run.kick:
		}
	}
}

var errRevoked = newError(http.StatusConflict, CodeNotConfigured, "the hub revoked this node's membership")

// pollMember runs one member cycle: fetch the state (with If-None-Match),
// apply a changed catalog, and hand the peers to the gateway.
func (s *Service) pollMember(ctx context.Context, run *roleRun) error {
	if err := s.pollMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.pollMu.Unlock()
	s.mu.Lock()
	m, gen, etag, applied, selfID := s.membership, s.gen, s.stateETag, s.appliedCatalog, s.identity.ID
	s.mu.Unlock()
	if m.Role != RoleMember || m.Revoked {
		return newError(http.StatusConflict, CodeNotConfigured, "this node is not an active cluster member")
	}
	reqCtx, cancel := context.WithTimeout(ctx, hubCallTimeout)
	defer cancel()
	req, err := s.newHubRequest(reqCtx, m, http.MethodGet, "state")
	if err != nil {
		return err
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	start := time.Now()
	resp, err := s.client.Do(req)
	if err != nil {
		return s.memberPollFailed(ctx, gen, fmt.Errorf("state: %w", err))
	}
	rtt := time.Since(start).Milliseconds()
	var fresh *State
	switch resp.StatusCode {
	case http.StatusOK:
		var state State
		err := decodeBody(resp.Body, 8<<20, &state)
		drain(resp.Body)
		if err != nil {
			return s.memberPollFailed(ctx, gen, fmt.Errorf("state: %w", err))
		}
		fresh = &state
		etag = resp.Header.Get("ETag")
		if etag == "" {
			etag = quoteETag(state.Version)
		}
	case http.StatusNotModified:
		drain(resp.Body)
	default:
		if revokedResponse(resp.StatusCode, resp.Header) {
			drain(resp.Body)
			s.handleRevoked(run, gen, resp.StatusCode, "state_unauthorized")
			return errRevoked
		}
		code, message := readHubError(resp)
		_ = resp.Body.Close()
		return s.memberPollFailed(ctx, gen, fmt.Errorf("state: hub answered %d %s: %s", resp.StatusCode, code, message))
	}
	s.mu.Lock()
	if s.gen != gen {
		s.mu.Unlock()
		return nil
	}
	if fresh != nil {
		s.state = *fresh
		s.stateETag = etag
	}
	state := s.state
	recovered := s.failures >= unreachableAfter
	s.failures = 0
	s.peersOffline = false
	s.link.RTTMS = rtt
	s.mu.Unlock()
	if recovered {
		s.logf("[cluster] hub reachable again")
	}

	var catalogErr error
	if state.CatalogVersion != "" && state.CatalogVersion != applied {
		catalogErr = s.syncCatalog(ctx, run, m, gen)
		if errors.Is(catalogErr, errRevoked) {
			return catalogErr
		}
	}
	peers := make([]NodeState, 0, len(state.Nodes))
	for _, node := range state.Nodes {
		if node.ID != selfID {
			node.Offers = cloneOffers(node.Offers)
			peers = append(peers, node)
		}
	}
	key := peersKey(peers)
	s.mu.Lock()
	if s.gen != gen {
		s.mu.Unlock()
		return nil
	}
	s.peers = peers
	apply := key != s.appliedPeers
	s.publishRouteLocked()
	s.mu.Unlock()
	if apply {
		if err := s.local.ApplyPeers(ctx, peers); err != nil {
			return fmt.Errorf("apply peers: %w", err)
		}
		s.mu.Lock()
		if s.gen == gen {
			s.appliedPeers = key
		}
		s.mu.Unlock()
	}
	if catalogErr != nil && ctx.Err() == nil {
		s.logf("[cluster] catalog sync: %v", catalogErr)
	}
	return catalogErr
}

func (s *Service) syncCatalog(ctx context.Context, run *roleRun, m Membership, gen uint64) error {
	reqCtx, cancel := context.WithTimeout(ctx, hubCallTimeout)
	defer cancel()
	req, err := s.newHubRequest(reqCtx, m, http.MethodGet, "catalog")
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("catalog: %w", err)
	}
	defer drain(resp.Body)
	if revokedResponse(resp.StatusCode, resp.Header) {
		s.handleRevoked(run, gen, resp.StatusCode, "catalog_unauthorized")
		return errRevoked
	}
	if resp.StatusCode != http.StatusOK {
		code, message := readHubError(resp)
		return fmt.Errorf("catalog: hub answered %d %s: %s", resp.StatusCode, code, message)
	}
	var catalog Catalog
	if err := decodeBody(resp.Body, 32<<20, &catalog); err != nil {
		return fmt.Errorf("catalog: %w", err)
	}
	catalog = normalizeCatalog(catalog)
	if catalog.Version == "" {
		catalog.Version = catalogVersion(catalog)
	}
	report, err := s.local.ApplyCatalog(ctx, catalog)
	if err != nil {
		return fmt.Errorf("apply catalog: %w", err)
	}
	if report.Version == "" {
		report.Version = catalog.Version
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen != gen {
		return nil
	}
	s.catalog = catalog
	s.report = report
	s.appliedCatalog = catalog.Version
	s.publishRouteLocked()
	return nil
}

func (s *Service) memberPollFailed(ctx context.Context, gen uint64, cause error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.mu.Lock()
	if s.gen != gen {
		s.mu.Unlock()
		return cause
	}
	s.failures++
	failures := s.failures
	var offline []NodeState
	if failures >= unreachableAfter && !s.peersOffline {
		s.peersOffline = true
		offline = make([]NodeState, len(s.peers))
		for i, node := range s.peers {
			node.Online = false
			node.Offers = cloneOffers(node.Offers)
			offline[i] = node
		}
		s.publishRouteLocked()
	}
	s.mu.Unlock()
	if failures == 1 || failures == unreachableAfter {
		s.logf("[cluster] hub poll failed (%d in a row): %v", failures, cause)
	}
	if offline != nil {
		if err := s.local.ApplyPeers(ctx, offline); err != nil {
			s.logf("[cluster] apply offline peers: %v", err)
		} else {
			s.mu.Lock()
			if s.gen == gen {
				s.appliedPeers = peersKey(offline)
			}
			s.mu.Unlock()
		}
	}
	return newError(http.StatusBadGateway, CodeHubUnreachable, cause.Error())
}

// handleRevoked turns this member into a revoked one: loops stop with no
// redial, and the persisted state is updated in the background. gen is the
// membership generation the evidence was gathered under.
func (s *Service) handleRevoked(run *roleRun, gen uint64, status int, reason string) {
	s.mu.Lock()
	if s.gen != gen || s.membership.Role != RoleMember || s.membership.Revoked {
		s.mu.Unlock()
		return
	}
	m := s.membership
	m.Revoked = true
	m.NodeToken = "" // the hub no longer accepts it; keep no dead secret
	s.membership = m
	s.gen++
	revokedGen := s.gen
	current := s.run
	s.cleanupRun = current
	s.run = nil
	s.resetClusterStateLocked()
	s.link = linkStatus{State: LinkRevoked, LastError: "the hub no longer accepts this node"}
	s.publishRouteLocked()
	s.mu.Unlock()
	if current != nil {
		current.cancel()
	} else if run != nil {
		run.cancel()
	}
	s.logf("[cluster] membership of cluster %s revoked (%s)", m.ClusterID, reason)
	s.goBackground(func() { s.finishRevocation(current, m, revokedGen, status, reason) })
}

// finishRevocation persists a revocation once the old loops are gone, so a
// late catalog apply cannot land after ClearClusterData. It skips the work if
// an admin operation changed the membership first.
func (s *Service) finishRevocation(run *roleRun, m Membership, gen uint64, status int, reason string) {
	if run != nil {
		run.wg.Wait()
	}
	s.retryRevokedCleanup(m, gen)
	s.mu.Lock()
	current := s.gen == gen
	s.mu.Unlock()
	if current {
		ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
		defer cancel()
		s.local.Audit(ctx, PrincipalPrefix+m.HubNodeID, "cluster.revoked", m.ClusterID, status, map[string]any{"reason": reason})
	}
}

// Retry both persistence and cleanup; the generation guard prevents cleanup
// from deleting data belonging to a later membership.
func (s *Service) retryRevokedCleanup(m Membership, gen uint64) {
	backoff := s.minBackoff
	for {
		s.opMu.Lock()
		s.mu.Lock()
		current := s.gen == gen && s.membership.Role == RoleMember && s.membership.Revoked
		s.mu.Unlock()
		if !current {
			s.opMu.Unlock()
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
		saveErr := s.settings.SaveMembership(ctx, m)
		clearErr := s.local.ClearClusterData(ctx)
		cancel()
		s.opMu.Unlock()
		if saveErr == nil && clearErr == nil {
			return
		}
		s.logf("[cluster] retry revoked cleanup: save=%v clear=%v", saveErr, clearErr)
		timer := time.NewTimer(backoff)
		select {
		case <-s.stopCh:
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff = min(2*backoff, s.maxBackoff)
	}
}
