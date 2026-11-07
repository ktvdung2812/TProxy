package cluster

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tproxy/tproxy/internal/security"
)

const (
	defaultJoinTokenTTL = 24 * time.Hour
	maxJoinTokenTTL     = 720 * time.Hour
	maxJoinTokenUses    = 100
)

func errNotConfigured() *Error {
	return newError(http.StatusConflict, CodeNotConfigured, "this node is not part of a cluster")
}

func errNotHub() *Error {
	return newError(http.StatusConflict, CodeNotConfigured, "this node is not the cluster hub")
}

func errAlreadyConfigured() *Error {
	return newError(http.StatusConflict, CodeAlreadyConfigured, "this node already belongs to a cluster")
}

func invalidRequest(message string) *Error {
	return newError(http.StatusBadRequest, CodeInvalidRequest, message)
}

func saveMembershipError(err error) error {
	if errors.Is(err, ErrMasterKeyRequired) {
		return newError(http.StatusPreconditionFailed, CodeMasterKeyRequired, "set TPROXY_MASTER_KEY so the node token can be stored encrypted")
	}
	return err
}

func (s *Service) snapshot() (Membership, NodeIdentity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.membership, s.identity
}

// CreateCluster makes this standalone node the hub of a new cluster.
func (s *Service) CreateCluster(ctx context.Context, in CreateInput) error {
	if err := s.opMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.opMu.Unlock()
	if err := s.ensureLoaded(ctx); err != nil {
		return err
	}
	m, self := s.snapshot()
	if m.Role != RoleStandalone {
		return errAlreadyConfigured()
	}
	if strings.TrimSpace(in.HubURL) == "" {
		return invalidRequest("hub_url is required")
	}
	hubURL, err := NormalizeHubURL(in.HubURL, in.AllowInsecure)
	if err != nil {
		return err
	}
	name, ok := cleanText(in.Name)
	if !ok {
		return invalidRequest("cluster name must be at most 100 printable characters")
	}
	if name == "" {
		name = defaultClusterName
	}
	lending := DefaultLendingPolicy()
	if in.Lending != nil {
		lending = *in.Lending
	}
	if lending, err = validateLending(lending); err != nil {
		return err
	}
	borrowing, saved, err := s.settings.Borrowing(ctx)
	if err != nil {
		return err
	}
	if !saved {
		borrowing = DefaultBorrowingPolicy()
		if err := s.settings.SaveBorrowing(ctx, borrowing); err != nil {
			return err
		}
	}
	if err := s.settings.SaveLending(ctx, lending); err != nil {
		return err
	}
	membership := Membership{
		Role: RoleHub, ClusterID: uuid.NewString(), ClusterName: name, HubURL: hubURL, HubNodeID: self.ID,
		AllowInsecure: in.AllowInsecure, JoinedAt: s.now().UTC(),
	}
	if err := s.settings.SaveMembership(ctx, membership); err != nil {
		return saveMembershipError(err)
	}
	s.mu.Lock()
	s.membership = membership
	s.gen++
	s.lending = lending
	s.borrowing = normalizeBorrowing(borrowing)
	s.resetClusterStateLocked()
	s.startRunLocked()
	s.publishRouteLocked()
	s.mu.Unlock()
	s.logf("[cluster] created cluster %q (%s); this node is the hub", name, membership.ClusterID)
	return nil
}

// CreateJoinToken issues a token for new members. Zero ttl and maxUses take
// the defaults of 24h and 1.
func (s *Service) CreateJoinToken(ctx context.Context, label string, ttl time.Duration, maxUses int) (string, JoinTokenRecord, error) {
	if err := s.ensureLoaded(ctx); err != nil {
		return "", JoinTokenRecord{}, err
	}
	m, _ := s.snapshot()
	if m.Role != RoleHub {
		return "", JoinTokenRecord{}, errNotHub()
	}
	label, ok := cleanText(label)
	if !ok {
		return "", JoinTokenRecord{}, invalidRequest("label must be at most 100 printable characters")
	}
	if ttl == 0 {
		ttl = defaultJoinTokenTTL
	}
	if ttl < 0 || ttl > maxJoinTokenTTL {
		return "", JoinTokenRecord{}, invalidRequest("ttl must be between 1s and 720h")
	}
	if maxUses == 0 {
		maxUses = 1
	}
	if maxUses < 0 || maxUses > maxJoinTokenUses {
		return "", JoinTokenRecord{}, invalidRequest("max_uses must be between 1 and 100")
	}
	secret := NewSecret()
	token, err := EncodeJoinToken(JoinToken{Version: joinTokenVersion, HubURL: m.HubURL, ClusterID: m.ClusterID, Secret: secret})
	if err != nil {
		return "", JoinTokenRecord{}, err
	}
	now := s.now().UTC()
	record := JoinTokenRecord{ID: uuid.NewString(), Label: label, MaxUses: maxUses, ExpiresAt: now.Add(ttl), CreatedAt: now}
	if err := s.hubStore.CreateJoinToken(ctx, record, HashSecret(secret)); err != nil {
		return "", JoinTokenRecord{}, err
	}
	return token, record, nil
}

func (s *Service) JoinTokens(ctx context.Context) ([]JoinTokenRecord, error) {
	if err := s.ensureLoaded(ctx); err != nil {
		return nil, err
	}
	if m, _ := s.snapshot(); m.Role != RoleHub {
		return nil, errNotHub()
	}
	tokens, err := s.hubStore.JoinTokens(ctx)
	if err != nil {
		return nil, err
	}
	if tokens == nil {
		tokens = []JoinTokenRecord{}
	}
	return tokens, nil
}

func (s *Service) RevokeJoinToken(ctx context.Context, id string) error {
	if err := s.ensureLoaded(ctx); err != nil {
		return err
	}
	if m, _ := s.snapshot(); m.Role != RoleHub {
		return errNotHub()
	}
	if err := s.hubStore.RevokeJoinToken(ctx, strings.TrimSpace(id), s.now().UTC()); err != nil {
		if errors.Is(err, ErrNotFound) {
			return newError(http.StatusNotFound, CodeNotFound, "join token not found")
		}
		return err
	}
	return nil
}

// Join makes this node a member of the cluster named by in.Token. A revoked
// member may join again.
type encryptionChecker interface{ EncryptionAvailable() bool }

func (s *Service) Join(ctx context.Context, in JoinInput) error {
	if err := s.opMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.opMu.Unlock()
	if err := s.ensureLoaded(ctx); err != nil {
		return err
	}
	m, self := s.snapshot()
	if m.Role != RoleStandalone && (m.Role != RoleMember || !m.Revoked) {
		return errAlreadyConfigured()
	}
	token, err := DecodeJoinToken(in.Token)
	if err != nil {
		return newError(http.StatusBadRequest, CodeInvalidJoinToken, "the join token is malformed")
	}
	hubURL, err := NormalizeHubURL(token.HubURL, in.AllowInsecure)
	if err != nil {
		var clusterErr *Error
		if errors.As(err, &clusterErr) && clusterErr.Code == CodeInsecureHubURL {
			return err
		}
		return newError(http.StatusBadRequest, CodeInvalidJoinToken, "the join token carries an invalid hub URL")
	}
	name, nameOK := cleanText(in.NodeName)
	owner, ownerOK := cleanText(in.Owner)
	if !nameOK || !ownerOK {
		return invalidRequest("node name and owner must be at most 100 printable characters")
	}
	lending := DefaultLendingPolicy()
	if in.Lending != nil {
		lending = *in.Lending
	}
	if lending, err = validateLending(lending); err != nil {
		return err
	}
	if checker, ok := s.settings.(encryptionChecker); ok && !checker.EncryptionAvailable() {
		return saveMembershipError(ErrMasterKeyRequired)
	}
	if m.Revoked {
		s.waitRevokedRun()
		if err := s.local.ClearClusterData(ctx); err != nil {
			return fmt.Errorf("clear cluster data: %w", err)
		}
	}
	joined, err := s.postJoin(ctx, hubURL, joinRequest{
		Protocol: ProtocolVersion, ClusterID: token.ClusterID, Secret: token.Secret,
		NodeID: self.ID, NodeName: cmp.Or(name, self.Name), Owner: owner, Version: s.version,
	})
	if err != nil {
		return err
	}
	membership := Membership{
		Role: RoleMember, ClusterID: joined.ClusterID, ClusterName: joined.ClusterName, HubURL: hubURL,
		HubNodeID: joined.HubNodeID, AllowInsecure: in.AllowInsecure, JoinedAt: s.now().UTC(), NodeToken: joined.NodeToken,
	}
	if err := s.settings.SaveMembership(ctx, membership); err != nil {
		// The hub already counts this node; leaving undoes that, since the
		// token cannot be kept to come back with.
		s.postLeave(membership, self.ID)
		return saveMembershipError(err)
	}
	if err := s.settings.SaveLending(ctx, lending); err != nil {
		s.logf("[cluster] save lending policy: %v", err)
	}
	borrowing, saved, err := s.settings.Borrowing(ctx)
	switch {
	case err != nil:
		s.logf("[cluster] load borrowing policy: %v", err)
		borrowing = DefaultBorrowingPolicy()
	case !saved:
		borrowing = DefaultBorrowingPolicy()
		if err := s.settings.SaveBorrowing(ctx, borrowing); err != nil {
			s.logf("[cluster] save borrowing policy: %v", err)
		}
	}
	if name != "" && name != self.Name {
		self.Name = name
		if err := s.settings.SaveNodeIdentity(ctx, self); err != nil {
			s.logf("[cluster] rename node: %v", err)
		}
	}
	s.mu.Lock()
	s.identity = self
	s.membership = membership
	s.gen++
	s.lending = lending
	s.borrowing = normalizeBorrowing(borrowing)
	s.resetClusterStateLocked()
	s.link = linkStatus{State: LinkConnecting}
	s.startRunLocked()
	s.publishRouteLocked()
	s.mu.Unlock()
	s.logf("[cluster] joined cluster %q (%s) via %s", membership.ClusterName, membership.ClusterID, hubURL)
	return nil
}

func (s *Service) postJoin(ctx context.Context, hubURL string, body joinRequest) (joinResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, joinTimeout)
	defer cancel()
	data, err := json.Marshal(body)
	if err != nil {
		return joinResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hubURL+HubPathPrefix+"join", bytes.NewReader(data))
	if err != nil {
		return joinResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderProtocol, ProtocolVersion)
	req.Header.Set("User-Agent", "tproxy-cluster/"+s.version)
	resp, err := s.client.Do(req)
	if err != nil {
		return joinResponse{}, newError(http.StatusBadGateway, CodeHubUnreachable, "could not reach the cluster hub: "+security.RedactText(err.Error()))
	}
	defer drain(resp.Body)
	if resp.StatusCode != http.StatusOK {
		code, message := readHubError(resp)
		switch code {
		case CodeNodeExists:
			return joinResponse{}, newError(http.StatusConflict, code, message)
		case CodeInvalidJoinToken, CodeJoinTokenExpired, CodeInvalidRequest:
			return joinResponse{}, newError(http.StatusBadRequest, code, message)
		}
		return joinResponse{}, newError(http.StatusBadGateway, CodeHubUnreachable, fmt.Sprintf("the cluster hub answered %d: %s", resp.StatusCode, message))
	}
	var joined joinResponse
	if err := decodeBody(resp.Body, 64<<10, &joined); err != nil {
		return joinResponse{}, newError(http.StatusBadGateway, CodeHubUnreachable, "the cluster hub sent an invalid join response")
	}
	if joined.ClusterID != body.ClusterID || joined.NodeToken == "" || !validNodeID(joined.HubNodeID) {
		return joinResponse{}, newError(http.StatusBadGateway, CodeHubUnreachable, "the cluster hub sent an invalid join response")
	}
	return joined, nil
}

// postLeave tells the hub this node is leaving. It is best effort: a node
// that cannot reach its hub must still be able to leave.
func (s *Service) postLeave(m Membership, selfID string) {
	ctx, cancel := context.WithTimeout(context.Background(), leaveTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.HubURL+HubPathPrefix+"leave", nil)
	if err != nil {
		return
	}
	s.authorizeHubRequest(req, m, selfID)
	resp, err := s.client.Do(req)
	if err != nil {
		s.logf("[cluster] leave notice to hub: %v", err)
		return
	}
	drain(resp.Body)
}

// Leave takes a member, revoked or not, back to standalone.
func (s *Service) Leave(ctx context.Context) error {
	if err := s.opMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.opMu.Unlock()
	if err := s.ensureLoaded(ctx); err != nil {
		return err
	}
	m, self := s.snapshot()
	switch m.Role {
	case RoleMember:
	case RoleHub:
		return invalidRequest("the hub cannot leave its own cluster; dissolve it instead")
	default:
		return errNotConfigured()
	}
	// Loops stop first so the hub closing the link is not taken for a
	// revocation.
	s.stopRun()
	s.waitRevokedRun()
	if !m.Revoked {
		s.postLeave(m, self.ID)
		m.Revoked = true
		m.NodeToken = ""
		s.mu.Lock()
		s.membership = m
		s.gen++
		s.resetClusterStateLocked()
		s.link = linkStatus{State: LinkRevoked}
		s.publishRouteLocked()
		s.mu.Unlock()
	}
	s.mu.Lock()
	gen := s.gen
	s.mu.Unlock()
	if err := s.settings.SaveMembership(ctx, m); err != nil {
		s.goBackground(func() { s.retryRevokedCleanup(m, gen) })
		return fmt.Errorf("save revoked membership: %w", err)
	}
	if err := s.local.ClearClusterData(ctx); err != nil {
		s.goBackground(func() { s.retryRevokedCleanup(m, gen) })
		return fmt.Errorf("clear cluster data: %w", err)
	}
	if err := s.settings.ClearMembership(ctx); err != nil {
		return fmt.Errorf("clear membership: %w", err)
	}
	s.mu.Lock()
	s.membership = Membership{Role: RoleStandalone}
	s.gen++
	s.resetClusterStateLocked()
	s.link = linkStatus{State: LinkOffline}
	s.publishRouteLocked()
	s.mu.Unlock()
	s.logf("[cluster] left cluster %s", m.ClusterID)
	return nil
}

// Dissolve ends the cluster this node is the hub of. Members are told they
// were revoked before their links close.
func (s *Service) Dissolve(ctx context.Context) error {
	if err := s.opMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.opMu.Unlock()
	if err := s.ensureLoaded(ctx); err != nil {
		return err
	}
	m, _ := s.snapshot()
	if m.Role != RoleHub {
		return errNotHub()
	}
	// Gate new work while retaining the persisted role for retries.
	s.mu.Lock()
	s.dissolving = true
	s.mu.Unlock()
	s.links.mu.Lock()
	s.links.mu.Unlock() // wait for any registration already in progress
	s.notifyAndWait(s.links.all(), "revoked")
	s.stopRun()
	s.closeLinks()
	s.linkWG.Wait()
	if err := s.hubStore.ResetHub(ctx); err != nil {
		return fmt.Errorf("reset hub: %w", err)
	}
	if err := s.local.ClearClusterData(ctx); err != nil {
		return fmt.Errorf("clear cluster data: %w", err)
	}
	empty := normalizeShared(SharedSelection{})
	if err := s.settings.SaveShared(ctx, empty); err != nil {
		return fmt.Errorf("clear shared selection: %w", err)
	}
	if err := s.settings.ClearMembership(ctx); err != nil {
		return fmt.Errorf("clear membership: %w", err)
	}

	s.mu.Lock()
	s.membership = Membership{Role: RoleStandalone}
	s.dissolving = false
	s.gen++
	s.resetClusterStateLocked()
	s.shared = empty
	s.publishRouteLocked()
	s.mu.Unlock()
	s.logf("[cluster] dissolved cluster %s", m.ClusterID)
	return nil
}

// RevokeNode removes a member from the cluster. Its link closes at once and
// its token stops working, which the member notices on its next poll.
func (s *Service) RevokeNode(ctx context.Context, id string) error {
	if err := s.opMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.opMu.Unlock()
	if err := s.ensureLoaded(ctx); err != nil {
		return err
	}
	m, self := s.snapshot()
	if m.Role != RoleHub {
		return errNotHub()
	}
	id = strings.TrimSpace(id)
	if id == "" || id == self.ID {
		return invalidRequest("choose a member node to revoke")
	}
	s.links.mu.Lock()
	if err := s.hubStore.SetNodeStatus(ctx, id, NodeRevoked); err != nil {
		s.links.mu.Unlock()
		if errors.Is(err, ErrNotFound) {
			return newError(http.StatusNotFound, CodeNotFound, "node not found")
		}
		return err
	}
	l := s.links.byNode[id]
	delete(s.links.byNode, id)
	s.links.mu.Unlock()
	if l != nil {
		l.close()
	}
	s.mu.Lock()
	delete(s.inventories, id)
	s.mu.Unlock()
	s.refreshAndNotify(ctx)
	s.logf("[cluster] revoked node %s", id)
	return nil
}

// UpdateNode renames a node or changes its owner. On the hub's own id only
// the name can change.
func (s *Service) UpdateNode(ctx context.Context, id string, name, owner *string) error {
	if err := s.opMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.opMu.Unlock()
	if err := s.ensureLoaded(ctx); err != nil {
		return err
	}
	m, self := s.snapshot()
	if m.Role != RoleHub {
		return errNotHub()
	}
	id = strings.TrimSpace(id)
	if name != nil {
		cleaned, ok := cleanText(*name)
		if !ok || cleaned == "" {
			return invalidRequest("name must be 1 to 100 printable characters")
		}
		name = &cleaned
	}
	if owner != nil {
		cleaned, ok := cleanText(*owner)
		if !ok {
			return invalidRequest("owner must be at most 100 printable characters")
		}
		owner = &cleaned
	}
	if id == self.ID {
		if owner != nil {
			return invalidRequest("the hub node has no owner field")
		}
		if name != nil {
			if err := s.renameLocal(ctx, *name); err != nil {
				return err
			}
		}
	} else if err := s.hubStore.UpdateNode(ctx, id, name, owner); err != nil {
		if errors.Is(err, ErrNotFound) {
			return newError(http.StatusNotFound, CodeNotFound, "node not found")
		}
		return err
	}
	s.refreshAndNotify(ctx)
	return nil
}

func (s *Service) RenameLocalNode(ctx context.Context, name string) error {
	if err := s.opMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.opMu.Unlock()
	if err := s.ensureLoaded(ctx); err != nil {
		return err
	}
	name, ok := cleanText(name)
	if !ok || name == "" {
		return invalidRequest("name must be 1 to 100 printable characters")
	}
	if err := s.renameLocal(ctx, name); err != nil {
		return err
	}
	if m, _ := s.snapshot(); m.Role == RoleHub {
		s.refreshAndNotify(ctx)
	}
	return nil
}

func (s *Service) renameLocal(ctx context.Context, name string) error {
	_, self := s.snapshot()
	self.Name = name
	if err := s.settings.SaveNodeIdentity(ctx, self); err != nil {
		return err
	}
	s.mu.Lock()
	s.identity = self
	s.mu.Unlock()
	return nil
}

func (s *Service) SetLending(ctx context.Context, p LendingPolicy) error {
	if err := s.opMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.opMu.Unlock()
	if err := s.ensureLoaded(ctx); err != nil {
		return err
	}
	p, err := validateLending(p)
	if err != nil {
		return err
	}
	if err := s.settings.SaveLending(ctx, p); err != nil {
		return err
	}
	s.mu.Lock()
	s.lending = p
	s.mu.Unlock()
	if m, _ := s.snapshot(); m.Role == RoleHub {
		// The hub's own offers are part of the state members poll.
		if err := s.pollHub(ctx); err != nil {
			s.logf("[cluster] refresh after lending change: %v", err)
		}
	}
	return nil
}

func (s *Service) SetBorrowing(ctx context.Context, p BorrowingPolicy) error {
	if err := s.opMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.opMu.Unlock()
	if err := s.ensureLoaded(ctx); err != nil {
		return err
	}
	p.Mode = strings.ToLower(strings.TrimSpace(p.Mode))
	if p.Mode == "" {
		p.Mode = ModeFallback
	}
	if p.Mode != ModeFallback && p.Mode != ModeShared {
		return invalidRequest("borrowing mode must be fallback or shared")
	}
	if err := s.settings.SaveBorrowing(ctx, p); err != nil {
		return err
	}
	s.mu.Lock()
	s.borrowing = p
	s.publishRouteLocked()
	s.mu.Unlock()
	return nil
}

// SetShared chooses what the hub publishes in the catalog; members are told
// as soon as the rebuilt catalog differs.
func (s *Service) SetShared(ctx context.Context, sel SharedSelection) error {
	if err := s.opMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.opMu.Unlock()
	if err := s.ensureLoaded(ctx); err != nil {
		return err
	}
	if m, _ := s.snapshot(); m.Role != RoleHub {
		return errNotHub()
	}
	sel, err := validateShared(sel)
	if err != nil {
		return err
	}
	if err := s.settings.SaveShared(ctx, sel); err != nil {
		return err
	}
	s.mu.Lock()
	s.shared = sel
	s.mu.Unlock()
	return s.pollHub(ctx)
}

// Ledger returns lending activity for the last days (1-31, default 7) with
// node and key names filled in.
func (s *Service) Ledger(ctx context.Context, days int) ([]LedgerView, error) {
	if err := s.ensureLoaded(ctx); err != nil {
		return nil, err
	}
	days = clampDays(days)
	m, self := s.snapshot()
	names := map[string]string{}
	var rows []LedgerRow
	switch {
	case m.Role == RoleHub:
		var err error
		if rows, err = s.hubStore.Ledger(ctx, sinceDay(s.now(), days)); err != nil {
			return nil, err
		}
		nodes, err := s.hubStore.Nodes(ctx)
		if err != nil {
			return nil, err
		}
		for _, node := range nodes {
			names[node.ID] = node.Name
		}
	case m.Role == RoleMember && !m.Revoked:
		var err error
		if rows, err = s.fetchLedger(ctx, m, days); err != nil {
			return nil, err
		}
		s.mu.Lock()
		for _, node := range s.state.Nodes {
			names[node.ID] = node.Name
		}
		s.mu.Unlock()
	default:
		return nil, errNotConfigured()
	}
	names[self.ID] = self.Name
	keyNames := map[string]string{}
	s.mu.Lock()
	for _, key := range s.catalog.APIKeys {
		keyNames[key.ID] = key.Name
	}
	s.mu.Unlock()
	views := make([]LedgerView, 0, len(rows))
	for _, row := range rows {
		view := LedgerView{LedgerRow: row, LenderName: names[row.Lender]}
		if id, ok := strings.CutPrefix(row.Borrower, "node:"); ok {
			view.BorrowerName = names[id]
		} else if id, ok := strings.CutPrefix(row.Borrower, "key:"); ok {
			view.BorrowerName = keyNames[id]
		}
		views = append(views, view)
	}
	return views, nil
}

func (s *Service) fetchLedger(ctx context.Context, m Membership, days int) ([]LedgerRow, error) {
	ctx, cancel := context.WithTimeout(ctx, hubCallTimeout)
	defer cancel()
	req, err := s.newHubRequest(ctx, m, http.MethodGet, "ledger?"+url.Values{"days": {strconv.Itoa(days)}}.Encode())
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, newError(http.StatusBadGateway, CodeHubUnreachable, "could not reach the cluster hub: "+security.RedactText(err.Error()))
	}
	defer drain(resp.Body)
	if resp.StatusCode != http.StatusOK {
		if revokedResponse(resp.StatusCode, resp.Header) {
			s.kick()
		}
		code, message := readHubError(resp)
		return nil, newError(http.StatusBadGateway, CodeHubUnreachable, fmt.Sprintf("the cluster hub answered %d %s: %s", resp.StatusCode, code, message))
	}
	var body ledgerResponse
	if err := decodeBody(resp.Body, 16<<20, &body); err != nil {
		return nil, newError(http.StatusBadGateway, CodeHubUnreachable, "the cluster hub sent an invalid ledger")
	}
	return body.Rows, nil
}

// Sync runs one poll cycle now and reports its error.
func (s *Service) Sync(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()
	if err := s.opMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.opMu.Unlock()
	if err := s.ensureLoaded(ctx); err != nil {
		return err
	}
	m, _ := s.snapshot()
	switch {
	case m.Role == RoleHub:
		return s.pollHub(ctx)
	case m.Role == RoleMember && !m.Revoked:
		return s.pollMember(ctx, s.currentRun())
	default:
		return errNotConfigured()
	}
}
