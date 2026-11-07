package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type ClusterNode struct {
	ID, Name, Owner, Status, Version, LastAddr string
	JoinedAt, LastSeenAt                       time.Time
}

type ClusterJoinToken struct {
	ID, Label                       string
	MaxUses, Uses                   int
	ExpiresAt, CreatedAt, RevokedAt time.Time
}

type ClusterLedgerRow struct {
	Day, Lender, Borrower               string
	Requests, InputTokens, OutputTokens int64
	CostUSD                             float64
}

const clusterJoinTokenColumns = `id,label,max_uses,uses,expires_at,created_at,revoked_at`

const clusterNodeColumns = `id,name,owner,status,version,last_addr,joined_at,last_seen_at`

// CreateClusterJoinToken stores a fresh, unused token. MaxUses below one means
// a single use; the expiry is required because a join token must not live
// forever.
func (s *Store) CreateClusterJoinToken(ctx context.Context, tok ClusterJoinToken, tokenHash string) error {
	if strings.TrimSpace(tok.ID) == "" || strings.TrimSpace(tokenHash) == "" {
		return errors.New("cluster join token id and hash are required")
	}
	if tok.ExpiresAt.IsZero() {
		return errors.New("cluster join token expiry is required")
	}
	if tok.MaxUses <= 0 {
		tok.MaxUses = 1
	}
	if tok.CreatedAt.IsZero() {
		tok.CreatedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `INSERT INTO cluster_join_tokens(id,token_hash,label,max_uses,uses,expires_at,created_at,revoked_at) VALUES(?,?,?,?,0,?,?,'')`,
		tok.ID, tokenHash, tok.Label, tok.MaxUses, clusterTimeText(tok.ExpiresAt), clusterTimeText(tok.CreatedAt))
	return err
}

// ConsumeClusterJoinToken spends one use of a token. Unknown and revoked tokens
// are ErrClusterJoinTokenInvalid; expired and used-up tokens are
// ErrClusterJoinTokenExpired. The returned token carries the new use count.
func (s *Store) ConsumeClusterJoinToken(ctx context.Context, tokenHash string, now time.Time) (ClusterJoinToken, error) {
	if tokenHash == "" {
		return ClusterJoinToken{}, ErrClusterJoinTokenInvalid
	}
	if now.IsZero() {
		now = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ClusterJoinToken{}, err
	}
	defer func() { _ = tx.Rollback() }()
	tok, err := scanClusterJoinToken(tx.QueryRowContext(ctx, `SELECT `+clusterJoinTokenColumns+` FROM cluster_join_tokens WHERE token_hash=?`, tokenHash).Scan)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ClusterJoinToken{}, ErrClusterJoinTokenInvalid
	case err != nil:
		return ClusterJoinToken{}, err
	case !tok.RevokedAt.IsZero():
		return ClusterJoinToken{}, ErrClusterJoinTokenInvalid
	case !now.Before(tok.ExpiresAt) || tok.Uses >= tok.MaxUses:
		return ClusterJoinToken{}, ErrClusterJoinTokenExpired
	}
	if _, err = tx.ExecContext(ctx, `UPDATE cluster_join_tokens SET uses=uses+1 WHERE id=?`, tok.ID); err != nil {
		return ClusterJoinToken{}, err
	}
	if err = tx.Commit(); err != nil {
		return ClusterJoinToken{}, err
	}
	tok.Uses++
	return tok, nil
}

// ClusterJoinTokens lists tokens newest first.
func (s *Store) ClusterJoinTokens(ctx context.Context) ([]ClusterJoinToken, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+clusterJoinTokenColumns+` FROM cluster_join_tokens ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tokens := []ClusterJoinToken{}
	for rows.Next() {
		tok, err := scanClusterJoinToken(rows.Scan)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, tok)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	// RFC3339Nano drops trailing zeros, so the stored text does not sort
	// chronologically; sort on the parsed times instead.
	sort.SliceStable(tokens, func(i, j int) bool { return tokens[i].CreatedAt.After(tokens[j].CreatedAt) })
	return tokens, nil
}

// RevokeClusterJoinToken keeps the first revocation time when called twice.
func (s *Store) RevokeClusterJoinToken(ctx context.Context, id string, at time.Time) error {
	if at.IsZero() {
		at = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.ExecContext(ctx, `UPDATE cluster_join_tokens SET revoked_at=CASE WHEN revoked_at='' THEN ? ELSE revoked_at END WHERE id=?`, clusterTimeText(at), id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func scanClusterJoinToken(scan func(dest ...any) error) (ClusterJoinToken, error) {
	var tok ClusterJoinToken
	var expires, created, revoked string
	if err := scan(&tok.ID, &tok.Label, &tok.MaxUses, &tok.Uses, &expires, &created, &revoked); err != nil {
		return ClusterJoinToken{}, err
	}
	tok.ExpiresAt, tok.CreatedAt, tok.RevokedAt = parseClusterTime(expires), parseClusterTime(created), parseClusterTime(revoked)
	return tok, nil
}

// SaveClusterNode upserts a node by id. An empty tokenHash keeps the stored
// hash, so it can only update a node that already exists. An empty Status and
// zero JoinedAt/LastSeenAt also keep the stored values; a new node defaults to
// status "active" and joined now.
func (s *Store) SaveClusterNode(ctx context.Context, node ClusterNode, tokenHash string) error {
	node.ID = strings.TrimSpace(node.ID)
	if node.ID == "" {
		return errors.New("cluster node id is required")
	}
	status, joinedAt := node.Status, node.JoinedAt
	if status == "" {
		status = "active"
	}
	if joinedAt.IsZero() {
		joinedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if tokenHash == "" {
		var count int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cluster_nodes WHERE id=?`, node.ID).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("cluster node %q needs a token hash: %w", node.ID, sql.ErrNoRows)
		}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO cluster_nodes(id,name,owner,token_hash,status,version,last_addr,joined_at,last_seen_at) VALUES(?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name,owner=excluded.owner,token_hash=CASE WHEN excluded.token_hash<>'' THEN excluded.token_hash ELSE cluster_nodes.token_hash END,
status=CASE WHEN ?<>'' THEN excluded.status ELSE cluster_nodes.status END,version=excluded.version,last_addr=excluded.last_addr,
joined_at=CASE WHEN ?<>'' THEN excluded.joined_at ELSE cluster_nodes.joined_at END,
last_seen_at=CASE WHEN excluded.last_seen_at<>'' THEN excluded.last_seen_at ELSE cluster_nodes.last_seen_at END`,
		node.ID, node.Name, node.Owner, tokenHash, status, node.Version, node.LastAddr, clusterTimeText(joinedAt), clusterTimeText(node.LastSeenAt),
		node.Status, clusterTimeText(node.JoinedAt))
	return err
}

func (s *Store) ClusterNodeByTokenHash(ctx context.Context, tokenHash string) (ClusterNode, error) {
	if tokenHash == "" {
		return ClusterNode{}, sql.ErrNoRows
	}
	return scanClusterNode(s.db.QueryRowContext(ctx, `SELECT `+clusterNodeColumns+` FROM cluster_nodes WHERE token_hash=?`, tokenHash).Scan)
}

// ClusterNodes lists nodes in join order.
func (s *Store) ClusterNodes(ctx context.Context) ([]ClusterNode, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+clusterNodeColumns+` FROM cluster_nodes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := []ClusterNode{}
	for rows.Next() {
		node, err := scanClusterNode(rows.Scan)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].JoinedAt.Before(nodes[j].JoinedAt) })
	return nodes, nil
}

// UpdateClusterNode changes the operator-facing fields; nil leaves a field as is.
func (s *Store) UpdateClusterNode(ctx context.Context, id string, name, owner *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.ExecContext(ctx, `UPDATE cluster_nodes SET name=COALESCE(?,name),owner=COALESCE(?,owner) WHERE id=?`, optionalClusterText(name), optionalClusterText(owner), id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) SetClusterNodeStatus(ctx context.Context, id, status string) error {
	if strings.TrimSpace(status) == "" {
		return errors.New("cluster node status is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.ExecContext(ctx, `UPDATE cluster_nodes SET status=? WHERE id=?`, status, id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// TouchClusterNode records a heartbeat; an empty addr or version keeps the
// stored value. It returns sql.ErrNoRows for an unknown node.
func (s *Store) TouchClusterNode(ctx context.Context, id, addr, version string, at time.Time) error {
	if at.IsZero() {
		at = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.ExecContext(ctx, `UPDATE cluster_nodes SET last_seen_at=?,last_addr=CASE WHEN ?<>'' THEN ? ELSE last_addr END,version=CASE WHEN ?<>'' THEN ? ELSE version END WHERE id=?`,
		clusterTimeText(at), addr, addr, version, version, id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func scanClusterNode(scan func(dest ...any) error) (ClusterNode, error) {
	var node ClusterNode
	var joined, seen string
	if err := scan(&node.ID, &node.Name, &node.Owner, &node.Status, &node.Version, &node.LastAddr, &joined, &seen); err != nil {
		return ClusterNode{}, err
	}
	node.JoinedAt, node.LastSeenAt = parseClusterTime(joined), parseClusterTime(seen)
	return node, nil
}

func optionalClusterText(value *string) any {
	if value == nil {
		return nil
	}
	return strings.TrimSpace(*value)
}

// UpsertClusterLedger stores lender-reported snapshots: a row replaces the
// previous one for the same (day, lender, borrower) rather than adding to it.
func (s *Store) UpsertClusterLedger(ctx context.Context, rows []ClusterLedgerRow) error {
	for _, row := range rows {
		if row.Day == "" || validateClusterDay(row.Day) != nil || row.Lender == "" || row.Borrower == "" {
			return fmt.Errorf("invalid cluster ledger row: day %q lender %q borrower %q", row.Day, row.Lender, row.Borrower)
		}
	}
	if len(rows) == 0 {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, row := range rows {
		if _, err = tx.ExecContext(ctx, `INSERT INTO cluster_ledger(day,lender_node_id,borrower,requests,input_tokens,output_tokens,cost_usd,updated_at) VALUES(?,?,?,?,?,?,?,?)
ON CONFLICT(day,lender_node_id,borrower) DO UPDATE SET requests=excluded.requests,input_tokens=excluded.input_tokens,output_tokens=excluded.output_tokens,cost_usd=excluded.cost_usd,updated_at=excluded.updated_at`,
			row.Day, row.Lender, row.Borrower, row.Requests, row.InputTokens, row.OutputTokens, row.CostUSD, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ClusterLedger returns rows with day >= sinceDay (YYYY-MM-DD; empty for all),
// newest day first.
func (s *Store) ClusterLedger(ctx context.Context, sinceDay string) ([]ClusterLedgerRow, error) {
	if err := validateClusterDay(sinceDay); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT day,lender_node_id,borrower,requests,input_tokens,output_tokens,cost_usd FROM cluster_ledger WHERE day>=? ORDER BY day DESC,lender_node_id,borrower`, sinceDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ledger := []ClusterLedgerRow{}
	for rows.Next() {
		var row ClusterLedgerRow
		if err := rows.Scan(&row.Day, &row.Lender, &row.Borrower, &row.Requests, &row.InputTokens, &row.OutputTokens, &row.CostUSD); err != nil {
			return nil, err
		}
		ledger = append(ledger, row)
	}
	return ledger, rows.Err()
}

func (s *Store) ResetClusterHub(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `DELETE FROM cluster_nodes; DELETE FROM cluster_join_tokens; DELETE FROM cluster_ledger;`); err != nil {
		return err
	}
	return tx.Commit()
}
