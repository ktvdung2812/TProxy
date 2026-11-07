package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"reflect"
	"sort"
	"strings"
	"time"
)

type ClusterPeer struct {
	NodeID, Name string
	Enabled      bool
	Models       []string
}

const clusterPeerCredentialPrefix = "cluster-node-"

type clusterPeerMetadata struct {
	ClusterNodeID string   `json:"cluster_node_id"`
	ServesModels  []string `json:"serves_models"`
}

// SyncClusterPeers makes the credentials of the cluster provider match peers,
// one credential per peer node, and creates the provider when it is missing. A
// credential is rewritten only when its label, enabled flag or metadata
// changed, so the router's cooldown and rotation state survives frequent syncs.
func (s *Store) SyncClusterPeers(ctx context.Context, peers []ClusterPeer) error {
	type peerRow struct {
		label, metadata string
		enabled         bool
	}
	desired := make(map[string]peerRow, len(peers))
	for _, peer := range peers {
		nodeID := strings.TrimSpace(peer.NodeID)
		if nodeID == "" {
			return errors.New("cluster peer node id is required")
		}
		metadata, err := json.Marshal(clusterPeerMetadata{ClusterNodeID: nodeID, ServesModels: clusterIDs(peer.Models)})
		if err != nil {
			return err
		}
		label := strings.TrimSpace(peer.Name)
		if label == "" {
			label = nodeID
		}
		desired[clusterPeerCredentialPrefix+nodeID] = peerRow{label: label, metadata: string(metadata), enabled: peer.Enabled}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = ensureClusterProviderTx(ctx, tx, now); err != nil {
		return err
	}
	type currentRow struct {
		authType, label, status, metadata string
		enabled                           bool
	}
	current := map[string]currentRow{}
	rows, err := tx.QueryContext(ctx, `SELECT id,auth_type,label,enabled,status,metadata_json FROM credentials WHERE provider_id=?`, ClusterProviderID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var row currentRow
		var enabled int
		if err = rows.Scan(&id, &row.authType, &row.label, &enabled, &row.status, &row.metadata); err != nil {
			_ = rows.Close()
			return err
		}
		row.enabled = enabled != 0
		current[id] = row
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if err = rows.Err(); err != nil {
		return err
	}
	var skipped []string
	for id, want := range desired {
		have, exists := current[id]
		if !exists {
			var providerID string
			err = tx.QueryRowContext(ctx, `SELECT provider_id FROM credentials WHERE id=?`, id).Scan(&providerID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil && providerID != ClusterProviderID {
				skipped = append(skipped, id)
				continue
			}
		}
		// Peer authentication belongs to the cluster link, so a peer marked
		// auth_required or disabled by the router is put back in rotation.
		if exists && have.authType == "none" && have.label == want.label && have.enabled == want.enabled && sameClusterJSON(have.metadata, want.metadata) && have.status != "auth_required" && have.status != "disabled" {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO credentials(id,provider_id,auth_type,status,label,email,secret_ciphertext,metadata_json,priority,weight,enabled,created_at) VALUES(?,?,'none','healthy',?,'','',?,0,1,?,?)
ON CONFLICT(id) DO UPDATE SET provider_id=excluded.provider_id,auth_type='none',secret_ciphertext='',label=excluded.label,enabled=excluded.enabled,metadata_json=excluded.metadata_json,
status=CASE WHEN credentials.status IN ('auth_required','disabled') THEN 'healthy' ELSE credentials.status END`,
			id, ClusterProviderID, want.label, want.metadata, boolInt(want.enabled), now); err != nil {
			return fmt.Errorf("sync cluster peer %s: %w", id, err)
		}
	}
	for id := range current {
		if _, keep := desired[id]; keep {
			continue
		}
		if err = deleteClusterCredentialTx(ctx, tx, id); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if len(skipped) > 0 {
		sort.Strings(skipped)
		log.Printf("cluster peer credentials skipped because IDs belong to another provider: %s", strings.Join(skipped, ", "))
	}
	return nil
}

// ClearClusterPeers removes the cluster provider and every peer credential. A
// local provider that merely reuses the id is left alone.
func (s *Store) ClearClusterPeers(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	ids, err := queryClusterStrings(ctx, tx, `SELECT c.id FROM credentials c JOIN providers p ON p.id=c.provider_id WHERE p.id=? AND p.type=?`, ClusterProviderID, ClusterProviderID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = deleteClusterCredentialTx(ctx, tx, id); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM route_targets WHERE provider_id IN (SELECT id FROM providers WHERE id=? AND type=?)`, ClusterProviderID, ClusterProviderID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM providers WHERE id=? AND type=?`, ClusterProviderID, ClusterProviderID); err != nil {
		return err
	}
	return tx.Commit()
}

func ensureClusterProviderTx(ctx context.Context, tx *sql.Tx, now string) error {
	var providerType string
	err := tx.QueryRowContext(ctx, `SELECT type FROM providers WHERE id=?`, ClusterProviderID).Scan(&providerType)
	if err == nil {
		if providerType != ClusterProviderID {
			return fmt.Errorf("provider id %q is already used by a %q provider", ClusterProviderID, providerType)
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// The marker sits at the top level for SQL readers and under "config" so it
	// also shows up in Provider.Config.
	providerConfig, err := json.Marshal(map[string]any{"managed_by": "cluster", "config": map[string]any{"managed_by": "cluster"}})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO providers(id,type,name,base_url,enabled,status,last_error,last_checked_at,headers_json,config_json,created_at,updated_at) VALUES(?,?,'Cluster','',1,'unknown','','','{}',?,?,?)`,
		ClusterProviderID, ClusterProviderID, string(providerConfig), now, now)
	return err
}

// deleteClusterCredentialTx removes a peer credential and its model cooldowns,
// which have no foreign key to cascade from.
func deleteClusterCredentialTx(ctx context.Context, tx *sql.Tx, id string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM credential_model_cooldowns WHERE credential_id=?`, id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM credentials WHERE id=?`, id)
	return err
}

func sameClusterJSON(a, b string) bool {
	var left, right any
	if json.Unmarshal([]byte(a), &left) != nil || json.Unmarshal([]byte(b), &right) != nil {
		return a == b
	}
	return reflect.DeepEqual(left, right)
}

type clusterQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func queryClusterStrings(ctx context.Context, q clusterQueryer, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
