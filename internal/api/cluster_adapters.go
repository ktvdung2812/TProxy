package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/tproxy/tproxy/internal/cluster"
	"github.com/tproxy/tproxy/internal/store"
)

// clusterStoreError maps store errors onto the sentinels the cluster service
// checks for.
func clusterStoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w: %v", cluster.ErrNotFound, err)
	case errors.Is(err, store.ErrClusterJoinTokenInvalid):
		return cluster.ErrJoinTokenInvalid
	case errors.Is(err, store.ErrClusterJoinTokenExpired):
		return cluster.ErrJoinTokenExpired
	case errors.Is(err, store.ErrClusterMasterKeyRequired):
		return fmt.Errorf("%w: %v", cluster.ErrMasterKeyRequired, err)
	}
	return err
}

// clusterSettingsAdapter implements cluster.SettingsStore over app settings.
type clusterSettingsAdapter struct{ store *store.Store }

func (a clusterSettingsAdapter) EncryptionAvailable() bool { return a.store.CanEncrypt() }

func (a clusterSettingsAdapter) NodeIdentity(ctx context.Context) (cluster.NodeIdentity, error) {
	identity, err := a.store.ClusterIdentity(ctx)
	if err != nil {
		return cluster.NodeIdentity{}, clusterStoreError(err)
	}
	return cluster.NodeIdentity{ID: identity.ID, Name: identity.Name}, nil
}

func (a clusterSettingsAdapter) SaveNodeIdentity(ctx context.Context, id cluster.NodeIdentity) error {
	return clusterStoreError(a.store.SaveClusterIdentity(ctx, store.ClusterIdentity{ID: id.ID, Name: id.Name}))
}

// Membership treats a node token that no longer decrypts (the master key
// changed) as a revocation it does not persist: the node can leave or join
// again, and restoring the old key restores the membership.
func (a clusterSettingsAdapter) Membership(ctx context.Context) (cluster.Membership, error) {
	stored, err := a.store.ClusterMembership(ctx)
	if err != nil {
		if stored.Role != string(cluster.RoleMember) {
			return cluster.Membership{}, clusterStoreError(err)
		}
		log.Printf("[cluster] the stored node token cannot be read (%v); treating membership of cluster %s as revoked until TPROXY_MASTER_KEY matches the one used to join", err, stored.ClusterID)
		stored.Revoked, stored.NodeToken = true, ""
	}
	return cluster.Membership{
		Role: cluster.Role(stored.Role), ClusterID: stored.ClusterID, ClusterName: stored.ClusterName, HubURL: stored.HubURL,
		HubNodeID: stored.HubNodeID, AllowInsecure: stored.AllowInsecure, Revoked: stored.Revoked, JoinedAt: stored.JoinedAt,
		NodeToken: stored.NodeToken,
	}, nil
}

func (a clusterSettingsAdapter) SaveMembership(ctx context.Context, m cluster.Membership) error {
	return clusterStoreError(a.store.SaveClusterMembership(ctx, store.ClusterMembership{
		Role: string(m.Role), ClusterID: m.ClusterID, ClusterName: m.ClusterName, HubURL: m.HubURL, HubNodeID: m.HubNodeID,
		AllowInsecure: m.AllowInsecure, Revoked: m.Revoked, JoinedAt: m.JoinedAt, NodeToken: m.NodeToken,
	}))
}

func (a clusterSettingsAdapter) ClearMembership(ctx context.Context) error {
	return clusterStoreError(a.store.ClearClusterMembership(ctx))
}

func (a clusterSettingsAdapter) Lending(ctx context.Context) (cluster.LendingPolicy, bool, error) {
	lending, saved, err := a.store.ClusterLending(ctx)
	if err != nil {
		return cluster.LendingPolicy{}, false, clusterStoreError(err)
	}
	return cluster.LendingPolicy{Enabled: lending.Enabled, Models: nonNilStrings(lending.Models), Limits: lending.Limits}, saved, nil
}

func (a clusterSettingsAdapter) SaveLending(ctx context.Context, p cluster.LendingPolicy) error {
	return clusterStoreError(a.store.SaveClusterLending(ctx, store.ClusterLending{Enabled: p.Enabled, Models: nonNilStrings(p.Models), Limits: p.Limits}))
}

func (a clusterSettingsAdapter) Borrowing(ctx context.Context) (cluster.BorrowingPolicy, bool, error) {
	borrowing, saved, err := a.store.ClusterBorrowing(ctx)
	if err != nil {
		return cluster.BorrowingPolicy{}, false, clusterStoreError(err)
	}
	return cluster.BorrowingPolicy{Enabled: borrowing.Enabled, Mode: borrowing.Mode}, saved, nil
}

func (a clusterSettingsAdapter) SaveBorrowing(ctx context.Context, p cluster.BorrowingPolicy) error {
	return clusterStoreError(a.store.SaveClusterBorrowing(ctx, store.ClusterBorrowing{Enabled: p.Enabled, Mode: p.Mode}))
}

func (a clusterSettingsAdapter) Shared(ctx context.Context) (cluster.SharedSelection, error) {
	shared, err := a.store.ClusterShared(ctx)
	if err != nil {
		return cluster.SharedSelection{}, clusterStoreError(err)
	}
	return cluster.SharedSelection{Models: nonNilStrings(shared.Models), Combos: nonNilStrings(shared.Combos), APIKeys: nonNilStrings(shared.APIKeys), Prices: shared.Prices}, nil
}

func (a clusterSettingsAdapter) SaveShared(ctx context.Context, sel cluster.SharedSelection) error {
	return clusterStoreError(a.store.SaveClusterShared(ctx, store.ClusterShared{Models: nonNilStrings(sel.Models), Combos: nonNilStrings(sel.Combos), APIKeys: nonNilStrings(sel.APIKeys), Prices: sel.Prices}))
}

// clusterHubAdapter implements cluster.HubStore over the hub tables.
type clusterHubAdapter struct{ store *store.Store }

func (a clusterHubAdapter) CreateJoinToken(ctx context.Context, rec cluster.JoinTokenRecord, secretHash string) error {
	return clusterStoreError(a.store.CreateClusterJoinToken(ctx, store.ClusterJoinToken{
		ID: rec.ID, Label: rec.Label, MaxUses: rec.MaxUses, Uses: rec.Uses, ExpiresAt: rec.ExpiresAt, CreatedAt: rec.CreatedAt, RevokedAt: rec.RevokedAt,
	}, secretHash))
}

func (a clusterHubAdapter) ConsumeJoinToken(ctx context.Context, secretHash string, now time.Time) (cluster.JoinTokenRecord, error) {
	token, err := a.store.ConsumeClusterJoinToken(ctx, secretHash, now)
	if err != nil {
		return cluster.JoinTokenRecord{}, clusterStoreError(err)
	}
	return clusterJoinTokenRecord(token), nil
}

func (a clusterHubAdapter) JoinTokens(ctx context.Context) ([]cluster.JoinTokenRecord, error) {
	tokens, err := a.store.ClusterJoinTokens(ctx)
	if err != nil {
		return nil, clusterStoreError(err)
	}
	records := make([]cluster.JoinTokenRecord, 0, len(tokens))
	for _, token := range tokens {
		records = append(records, clusterJoinTokenRecord(token))
	}
	return records, nil
}

func (a clusterHubAdapter) RevokeJoinToken(ctx context.Context, id string, at time.Time) error {
	return clusterStoreError(a.store.RevokeClusterJoinToken(ctx, id, at))
}

func (a clusterHubAdapter) SaveNode(ctx context.Context, rec cluster.NodeRecord, tokenHash string) error {
	return clusterStoreError(a.store.SaveClusterNode(ctx, store.ClusterNode{
		ID: rec.ID, Name: rec.Name, Owner: rec.Owner, Status: rec.Status, Version: rec.Version, LastAddr: rec.LastAddr, JoinedAt: rec.JoinedAt, LastSeenAt: rec.LastSeenAt,
	}, tokenHash))
}

func (a clusterHubAdapter) NodeByTokenHash(ctx context.Context, tokenHash string) (cluster.NodeRecord, error) {
	node, err := a.store.ClusterNodeByTokenHash(ctx, tokenHash)
	if err != nil {
		return cluster.NodeRecord{}, clusterStoreError(err)
	}
	return clusterNodeRecord(node), nil
}

func (a clusterHubAdapter) Nodes(ctx context.Context) ([]cluster.NodeRecord, error) {
	nodes, err := a.store.ClusterNodes(ctx)
	if err != nil {
		return nil, clusterStoreError(err)
	}
	records := make([]cluster.NodeRecord, 0, len(nodes))
	for _, node := range nodes {
		records = append(records, clusterNodeRecord(node))
	}
	return records, nil
}

func (a clusterHubAdapter) UpdateNode(ctx context.Context, id string, name, owner *string) error {
	return clusterStoreError(a.store.UpdateClusterNode(ctx, id, name, owner))
}

func (a clusterHubAdapter) SetNodeStatus(ctx context.Context, id, status string) error {
	return clusterStoreError(a.store.SetClusterNodeStatus(ctx, id, status))
}

func (a clusterHubAdapter) TouchNode(ctx context.Context, id, addr, version string, at time.Time) error {
	return clusterStoreError(a.store.TouchClusterNode(ctx, id, addr, version, at))
}

func (a clusterHubAdapter) UpsertLedger(ctx context.Context, rows []cluster.LedgerRow) error {
	converted := make([]store.ClusterLedgerRow, 0, len(rows))
	for _, row := range rows {
		converted = append(converted, store.ClusterLedgerRow{
			Day: row.Day, Lender: row.Lender, Borrower: row.Borrower, Requests: row.Requests,
			InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, CostUSD: row.CostUSD,
		})
	}
	return clusterStoreError(a.store.UpsertClusterLedger(ctx, converted))
}

func (a clusterHubAdapter) Ledger(ctx context.Context, sinceDay string) ([]cluster.LedgerRow, error) {
	rows, err := a.store.ClusterLedger(ctx, sinceDay)
	if err != nil {
		return nil, clusterStoreError(err)
	}
	return clusterLedgerRows(rows), nil
}

func (a clusterHubAdapter) ResetHub(ctx context.Context) error {
	return clusterStoreError(a.store.ResetClusterHub(ctx))
}

func clusterJoinTokenRecord(token store.ClusterJoinToken) cluster.JoinTokenRecord {
	return cluster.JoinTokenRecord{
		ID: token.ID, Label: token.Label, MaxUses: token.MaxUses, Uses: token.Uses,
		ExpiresAt: token.ExpiresAt, CreatedAt: token.CreatedAt, RevokedAt: token.RevokedAt,
	}
}

func clusterNodeRecord(node store.ClusterNode) cluster.NodeRecord {
	return cluster.NodeRecord{
		ID: node.ID, Name: node.Name, Owner: node.Owner, Status: node.Status, Version: node.Version, LastAddr: node.LastAddr,
		JoinedAt: node.JoinedAt, LastSeenAt: node.LastSeenAt,
	}
}

func clusterLedgerRows(rows []store.ClusterLedgerRow) []cluster.LedgerRow {
	converted := make([]cluster.LedgerRow, 0, len(rows))
	for _, row := range rows {
		converted = append(converted, cluster.LedgerRow{
			Day: row.Day, Lender: row.Lender, Borrower: row.Borrower, Requests: row.Requests,
			InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, CostUSD: row.CostUSD,
		})
	}
	return converted
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
