package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/security"
)

func openClusterTestStore(t *testing.T, withMasterKey bool) (*Store, string) {
	t.Helper()
	var encryptor *security.Encryptor
	if withMasterKey {
		key, err := security.GenerateMasterKey()
		if err != nil {
			t.Fatal(err)
		}
		if encryptor, err = security.NewEncryptor(key); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "cluster.db")
	dataStore, err := OpenSQLite(path, encryptor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	return dataStore, path
}

// totalChanges counts rows written on the store's only connection.
func totalChanges(t *testing.T, dataStore *Store) int64 {
	t.Helper()
	var changes int64
	if err := dataStore.db.QueryRow(`SELECT total_changes()`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	return changes
}

func TestClusterMembershipEncryptsNodeTokenAtRest(t *testing.T) {
	ctx := context.Background()
	dataStore, path := openClusterTestStore(t, true)
	if empty, err := dataStore.ClusterMembership(ctx); err != nil || !reflect.DeepEqual(empty, ClusterMembership{}) {
		t.Fatalf("absent membership = %+v, %v; want zero value", empty, err)
	}
	membership := ClusterMembership{Role: "member", ClusterID: "cluster-1", ClusterName: "Team", HubURL: "https://hub.example.test", HubNodeID: "hub-node", AllowInsecure: true, JoinedAt: time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC), NodeToken: "tpn_node-token-plaintext"}
	if err := dataStore.SaveClusterMembership(ctx, membership); err != nil {
		t.Fatal(err)
	}
	loaded, err := dataStore.ClusterMembership(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.JoinedAt.Equal(membership.JoinedAt) {
		t.Fatalf("joined_at = %v want %v", loaded.JoinedAt, membership.JoinedAt)
	}
	loaded.JoinedAt = membership.JoinedAt
	if !reflect.DeepEqual(loaded, membership) {
		t.Fatalf("membership = %+v want %+v", loaded, membership)
	}

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var stored string
	if err = raw.QueryRow(`SELECT value_json FROM app_settings WHERE key=?`, AppSettingClusterMembership).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, membership.NodeToken) || !strings.Contains(stored, `"node_token_ciphertext":"`) || !strings.Contains(stored, `"hub_url":"https://hub.example.test"`) {
		t.Fatalf("stored membership must carry the node token only as ciphertext: %s", stored)
	}

	if err = dataStore.SetAppSettingJSON(ctx, AppSettingClusterMembership, map[string]any{"role": "member", "hub_url": "https://hub.example.test", "node_token_ciphertext": "not-a-ciphertext"}); err != nil {
		t.Fatal(err)
	}
	broken, err := dataStore.ClusterMembership(ctx)
	if err == nil || broken.Role != "member" || broken.HubURL != "https://hub.example.test" || broken.NodeToken != "" {
		t.Fatalf("corrupt token = %+v, %v; want the other fields and an error", broken, err)
	}

	if err = dataStore.ClearClusterMembership(ctx); err != nil {
		t.Fatal(err)
	}
	if cleared, err := dataStore.ClusterMembership(ctx); err != nil || !reflect.DeepEqual(cleared, ClusterMembership{}) {
		t.Fatalf("cleared membership = %+v, %v", cleared, err)
	}

	keyless, _ := openClusterTestStore(t, false)
	if err = keyless.SaveClusterMembership(ctx, membership); !errors.Is(err, ErrClusterMasterKeyRequired) {
		t.Fatalf("save without master key = %v; want ErrClusterMasterKeyRequired", err)
	}
	// A hub holds no node token, so it needs no master key.
	if err = keyless.SaveClusterMembership(ctx, ClusterMembership{Role: "hub", ClusterID: "cluster-1"}); err != nil {
		t.Fatal(err)
	}
	if hub, err := keyless.ClusterMembership(ctx); err != nil || hub.Role != "hub" || hub.NodeToken != "" {
		t.Fatalf("hub membership = %+v, %v", hub, err)
	}
}

func TestClusterIdentityIsCreatedOnceAndPersisted(t *testing.T) {
	ctx := context.Background()
	dataStore, path := openClusterTestStore(t, false)
	var stored int
	if err := dataStore.db.QueryRow(`SELECT COUNT(*) FROM app_settings WHERE key=?`, AppSettingClusterNode).Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("identity rows before first use = %d, %v", stored, err)
	}
	identities := make([]ClusterIdentity, 8)
	var wait sync.WaitGroup
	for index := range identities {
		wait.Add(1)
		go func() {
			defer wait.Done()
			identity, err := dataStore.ClusterIdentity(ctx)
			if err != nil {
				t.Error(err)
			}
			identities[index] = identity
		}()
	}
	wait.Wait()
	first := identities[0]
	if _, err := uuid.Parse(first.ID); err != nil || first.Name == "" {
		t.Fatalf("identity = %+v, %v", first, err)
	}
	for _, identity := range identities {
		if identity != first {
			t.Fatalf("concurrent first calls disagree: %+v vs %+v", identity, first)
		}
	}
	if err := dataStore.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSQLite(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if again, err := reopened.ClusterIdentity(ctx); err != nil || again != first {
		t.Fatalf("identity after reopen = %+v, %v; want %+v", again, err, first)
	}
	renamed := ClusterIdentity{ID: first.ID, Name: "edge-1"}
	if err = reopened.SaveClusterIdentity(ctx, renamed); err != nil {
		t.Fatal(err)
	}
	if again, err := reopened.ClusterIdentity(ctx); err != nil || again != renamed {
		t.Fatalf("renamed identity = %+v, %v", again, err)
	}
	if err = reopened.SaveClusterIdentity(ctx, ClusterIdentity{Name: "no-id"}); err == nil {
		t.Fatal("identity without an id must be rejected")
	}
}

func TestClusterPolicySettingsRoundTrip(t *testing.T) {
	ctx := context.Background()
	dataStore, _ := openClusterTestStore(t, false)
	if _, found, err := dataStore.ClusterLending(ctx); err != nil || found {
		t.Fatalf("lending before save: found=%v err=%v", found, err)
	}
	if _, found, err := dataStore.ClusterBorrowing(ctx); err != nil || found {
		t.Fatalf("borrowing before save: found=%v err=%v", found, err)
	}
	if shared, err := dataStore.ClusterShared(ctx); err != nil || !reflect.DeepEqual(shared, ClusterShared{}) {
		t.Fatalf("shared before save = %+v, %v", shared, err)
	}
	lending := ClusterLending{Enabled: true, Models: []string{"gpt-shared"}, Limits: config.LimitPolicy{RequestsPerMinute: 30, BudgetUSDPerDay: 2.5}}
	borrowing := ClusterBorrowing{Enabled: true, Mode: "fallback"}
	shared := ClusterShared{Models: []string{"gpt-shared"}, Combos: []string{"combo"}, APIKeys: []string{"key"}, Prices: map[string]config.PricingConfig{"gpt-shared": {InputPerMillion: 1, OutputPerMillion: 2}}}
	if err := dataStore.SaveClusterLending(ctx, lending); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.SaveClusterBorrowing(ctx, borrowing); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.SaveClusterShared(ctx, shared); err != nil {
		t.Fatal(err)
	}
	if loaded, found, err := dataStore.ClusterLending(ctx); err != nil || !found || !reflect.DeepEqual(loaded, lending) {
		t.Fatalf("lending = %+v found=%v err=%v", loaded, found, err)
	}
	if loaded, found, err := dataStore.ClusterBorrowing(ctx); err != nil || !found || loaded != borrowing {
		t.Fatalf("borrowing = %+v found=%v err=%v", loaded, found, err)
	}
	if loaded, err := dataStore.ClusterShared(ctx); err != nil || !reflect.DeepEqual(loaded, shared) {
		t.Fatalf("shared = %+v, %v", loaded, err)
	}
}

func TestClusterJoinTokensConsumeExpireAndRevoke(t *testing.T) {
	ctx := context.Background()
	dataStore, _ := openClusterTestStore(t, false)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	create := func(id string, maxUses int, expires, created time.Time) {
		t.Helper()
		if err := dataStore.CreateClusterJoinToken(ctx, ClusterJoinToken{ID: id, Label: "label " + id, MaxUses: maxUses, ExpiresAt: expires, CreatedAt: created}, "hash-"+id); err != nil {
			t.Fatal(err)
		}
	}
	create("once", 1, now.Add(time.Hour), now.Add(-3*time.Minute))
	create("twice", 2, now.Add(time.Hour), now.Add(-2*time.Minute))
	create("stale", 5, now.Add(-time.Second), now.Add(-time.Hour))
	create("revoked", 5, now.Add(time.Hour), now.Add(-time.Minute))

	tok, err := dataStore.ConsumeClusterJoinToken(ctx, "hash-once", now)
	if err != nil || tok.ID != "once" || tok.Label != "label once" || tok.Uses != 1 || tok.MaxUses != 1 || !tok.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("consume once = %+v, %v", tok, err)
	}
	if _, err = dataStore.ConsumeClusterJoinToken(ctx, "hash-once", now); !errors.Is(err, ErrClusterJoinTokenExpired) {
		t.Fatalf("second use of a single-use token = %v; want expired", err)
	}
	for use := 1; use <= 2; use++ {
		if tok, err = dataStore.ConsumeClusterJoinToken(ctx, "hash-twice", now); err != nil || tok.Uses != use {
			t.Fatalf("use %d = %+v, %v", use, tok, err)
		}
	}
	if _, err = dataStore.ConsumeClusterJoinToken(ctx, "hash-twice", now); !errors.Is(err, ErrClusterJoinTokenExpired) {
		t.Fatalf("use past max_uses = %v; want expired", err)
	}
	if _, err = dataStore.ConsumeClusterJoinToken(ctx, "hash-stale", now); !errors.Is(err, ErrClusterJoinTokenExpired) {
		t.Fatalf("expired token = %v; want expired", err)
	}
	if err = dataStore.RevokeClusterJoinToken(ctx, "revoked", now); err != nil {
		t.Fatal(err)
	}
	if err = dataStore.RevokeClusterJoinToken(ctx, "revoked", now.Add(time.Hour)); err != nil {
		t.Fatalf("revoking twice = %v", err)
	}
	for _, hash := range []string{"hash-revoked", "hash-unknown", ""} {
		if _, err = dataStore.ConsumeClusterJoinToken(ctx, hash, now); !errors.Is(err, ErrClusterJoinTokenInvalid) {
			t.Fatalf("consume %q = %v; want invalid", hash, err)
		}
	}
	if err = dataStore.RevokeClusterJoinToken(ctx, "missing", now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("revoke missing token = %v; want sql.ErrNoRows", err)
	}

	tokens, err := dataStore.ClusterJoinTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, item := range tokens {
		ids = append(ids, item.ID)
	}
	if !reflect.DeepEqual(ids, []string{"revoked", "twice", "once", "stale"}) {
		t.Fatalf("tokens newest first = %v", ids)
	}
	if !tokens[0].RevokedAt.Equal(now) || tokens[2].Uses != 1 || tokens[3].Uses != 0 {
		t.Fatalf("token state after refused uses = %+v", tokens)
	}

	if err = dataStore.CreateClusterJoinToken(ctx, ClusterJoinToken{ID: "forever"}, "hash-forever"); err == nil {
		t.Fatal("a join token without expiry must be rejected")
	}
	if err = dataStore.CreateClusterJoinToken(ctx, ClusterJoinToken{ID: "copy", ExpiresAt: now.Add(time.Hour)}, "hash-once"); err == nil {
		t.Fatal("a duplicate token hash must be rejected")
	}
}

func TestClusterNodesUpsertLookupStatusAndTouch(t *testing.T) {
	ctx := context.Background()
	dataStore, _ := openClusterTestStore(t, false)
	joinedA := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	joinedB := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if err := dataStore.SaveClusterNode(ctx, ClusterNode{ID: "node-a", Name: "Alpha", Owner: "alice", Version: "1.0.0", LastAddr: "10.0.0.1:443", JoinedAt: joinedA}, "hash-a"); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.SaveClusterNode(ctx, ClusterNode{ID: "node-b", Name: "Beta", JoinedAt: joinedB}, "hash-b"); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.SaveClusterNode(ctx, ClusterNode{ID: "node-c"}, ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("new node without token hash = %v; want sql.ErrNoRows", err)
	}
	nodes, err := dataStore.ClusterNodes(ctx)
	if err != nil || len(nodes) != 2 || nodes[0].ID != "node-b" || nodes[1].ID != "node-a" {
		t.Fatalf("nodes in join order = %+v, %v", nodes, err)
	}
	if nodes[1].Status != "active" || nodes[1].Owner != "alice" || !nodes[1].JoinedAt.Equal(joinedA) || !nodes[1].LastSeenAt.IsZero() {
		t.Fatalf("node-a = %+v", nodes[1])
	}
	if node, err := dataStore.ClusterNodeByTokenHash(ctx, "hash-a"); err != nil || node.ID != "node-a" {
		t.Fatalf("lookup by hash = %+v, %v", node, err)
	}
	for _, hash := range []string{"missing", ""} {
		if _, err = dataStore.ClusterNodeByTokenHash(ctx, hash); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("lookup %q = %v; want sql.ErrNoRows", hash, err)
		}
	}

	if err = dataStore.SetClusterNodeStatus(ctx, "node-a", "suspended"); err != nil {
		t.Fatal(err)
	}
	// A re-save without hash, status or join time keeps all three.
	if err = dataStore.SaveClusterNode(ctx, ClusterNode{ID: "node-a", Name: "Alpha", Owner: "alice", Version: "1.1.0", LastAddr: "10.0.0.2:443"}, ""); err != nil {
		t.Fatal(err)
	}
	node, err := dataStore.ClusterNodeByTokenHash(ctx, "hash-a")
	if err != nil || node.Status != "suspended" || node.Version != "1.1.0" || node.LastAddr != "10.0.0.2:443" || !node.JoinedAt.Equal(joinedA) {
		t.Fatalf("re-saved node = %+v, %v", node, err)
	}
	if err = dataStore.SaveClusterNode(ctx, ClusterNode{ID: "node-a", Name: "Alpha", Owner: "alice", Status: "active", Version: "1.1.0", LastAddr: "10.0.0.2:443"}, "hash-a2"); err != nil {
		t.Fatal(err)
	}
	if _, err = dataStore.ClusterNodeByTokenHash(ctx, "hash-a"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old hash after rotation = %v; want sql.ErrNoRows", err)
	}
	if node, err = dataStore.ClusterNodeByTokenHash(ctx, "hash-a2"); err != nil || node.Status != "active" {
		t.Fatalf("rotated node = %+v, %v", node, err)
	}

	name, owner := "Alpha Prime", "bob"
	if err = dataStore.UpdateClusterNode(ctx, "node-a", &name, nil); err != nil {
		t.Fatal(err)
	}
	if node, _ = dataStore.ClusterNodeByTokenHash(ctx, "hash-a2"); node.Name != name || node.Owner != "alice" {
		t.Fatalf("rename = %+v", node)
	}
	if err = dataStore.UpdateClusterNode(ctx, "node-a", nil, &owner); err != nil {
		t.Fatal(err)
	}
	if node, _ = dataStore.ClusterNodeByTokenHash(ctx, "hash-a2"); node.Name != name || node.Owner != owner {
		t.Fatalf("owner change = %+v", node)
	}
	if err = dataStore.UpdateClusterNode(ctx, "missing", &name, nil); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("update missing node = %v", err)
	}
	if err = dataStore.SetClusterNodeStatus(ctx, "missing", "active"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("status of missing node = %v", err)
	}

	seen := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	if err = dataStore.TouchClusterNode(ctx, "node-a", "", "", seen); err != nil {
		t.Fatal(err)
	}
	if node, _ = dataStore.ClusterNodeByTokenHash(ctx, "hash-a2"); !node.LastSeenAt.Equal(seen) || node.LastAddr != "10.0.0.2:443" || node.Version != "1.1.0" {
		t.Fatalf("touch without addr/version = %+v", node)
	}
	if err = dataStore.TouchClusterNode(ctx, "node-a", "192.0.2.1:443", "1.2.0", seen.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if node, _ = dataStore.ClusterNodeByTokenHash(ctx, "hash-a2"); !node.LastSeenAt.Equal(seen.Add(time.Minute)) || node.LastAddr != "192.0.2.1:443" || node.Version != "1.2.0" {
		t.Fatalf("touch = %+v", node)
	}
	if err = dataStore.TouchClusterNode(ctx, "missing", "", "", seen); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("touch missing node = %v", err)
	}
}

func TestClusterLedgerUpsertOverwritesAndResetClearsHub(t *testing.T) {
	ctx := context.Background()
	dataStore, _ := openClusterTestStore(t, false)
	newer := ClusterLedgerRow{Day: "2026-11-01", Lender: "node-a", Borrower: "key:hub-key", Requests: 1, InputTokens: 5, OutputTokens: 2, CostUSD: 0.05}
	if err := dataStore.UpsertClusterLedger(ctx, []ClusterLedgerRow{
		{Day: "2026-10-31", Lender: "node-a", Borrower: "node:node-b", Requests: 3, InputTokens: 30, OutputTokens: 10, CostUSD: 0.3},
		newer,
	}); err != nil {
		t.Fatal(err)
	}
	// A later snapshot of the same day replaces the earlier one.
	replaced := ClusterLedgerRow{Day: "2026-10-31", Lender: "node-a", Borrower: "node:node-b", Requests: 4, InputTokens: 40, OutputTokens: 12, CostUSD: 0.4}
	if err := dataStore.UpsertClusterLedger(ctx, []ClusterLedgerRow{replaced}); err != nil {
		t.Fatal(err)
	}
	if ledger, err := dataStore.ClusterLedger(ctx, "2026-10-01"); err != nil || !reflect.DeepEqual(ledger, []ClusterLedgerRow{newer, replaced}) {
		t.Fatalf("ledger = %+v, %v", ledger, err)
	}
	if ledger, err := dataStore.ClusterLedger(ctx, "2026-11-01"); err != nil || !reflect.DeepEqual(ledger, []ClusterLedgerRow{newer}) {
		t.Fatalf("ledger since 2026-11-01 = %+v, %v", ledger, err)
	}
	if err := dataStore.UpsertClusterLedger(ctx, []ClusterLedgerRow{{Day: "yesterday", Lender: "node-a", Borrower: "node:node-b"}}); err == nil {
		t.Fatal("a ledger row with an invalid day must be rejected")
	}
	if _, err := dataStore.ClusterLedger(ctx, "11/01/2026"); err == nil {
		t.Fatal("an invalid since day must be rejected")
	}

	if err := dataStore.SaveClusterNode(ctx, ClusterNode{ID: "node-a"}, "hash-a"); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.CreateClusterJoinToken(ctx, ClusterJoinToken{ID: "tok", ExpiresAt: time.Now().Add(time.Hour)}, "hash-tok"); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.ResetClusterHub(ctx); err != nil {
		t.Fatal(err)
	}
	nodes, nodesErr := dataStore.ClusterNodes(ctx)
	tokens, tokensErr := dataStore.ClusterJoinTokens(ctx)
	ledger, ledgerErr := dataStore.ClusterLedger(ctx, "")
	if nodesErr != nil || tokensErr != nil || ledgerErr != nil || len(nodes)+len(tokens)+len(ledger) != 0 {
		t.Fatalf("after reset: nodes=%v tokens=%v ledger=%v errs=%v %v %v", nodes, tokens, ledger, nodesErr, tokensErr, ledgerErr)
	}
}

func TestSyncClusterPeersKeepsOneCredentialPerPeer(t *testing.T) {
	ctx := context.Background()
	dataStore, _ := openClusterTestStore(t, false)
	peers := []ClusterPeer{
		{NodeID: "node-a", Name: "Alpha", Enabled: true, Models: []string{"gpt-b", "gpt-a", "gpt-a", " "}},
		{NodeID: "node-b", Name: "Beta"},
	}
	if err := dataStore.SyncClusterPeers(ctx, peers); err != nil {
		t.Fatal(err)
	}
	provider, err := dataStore.Provider(ctx, ClusterProviderID)
	if err != nil || provider.Type != ClusterProviderID || provider.Name != "Cluster" || !provider.Enabled || provider.Config["managed_by"] != "cluster" {
		t.Fatalf("cluster provider = %+v, %v", provider, err)
	}
	credentials, err := dataStore.Credentials(ctx, ClusterProviderID)
	if err != nil || len(credentials) != 2 {
		t.Fatalf("peer credentials = %+v, %v", credentials, err)
	}
	alpha, err := dataStore.CredentialByID(ctx, "cluster-node-node-a")
	if err != nil || alpha.ProviderID != ClusterProviderID || alpha.AuthType != "none" || alpha.Label != "Alpha" || !alpha.Enabled || alpha.Status != "healthy" || alpha.Secret != "" || alpha.Priority != 0 || alpha.Weight != 1 {
		t.Fatalf("alpha = %+v, %v", alpha, err)
	}
	if alpha.Metadata["cluster_node_id"] != "node-a" || !reflect.DeepEqual(alpha.Metadata["serves_models"], []any{"gpt-a", "gpt-b"}) {
		t.Fatalf("alpha metadata = %#v", alpha.Metadata)
	}
	beta, err := dataStore.CredentialByID(ctx, "cluster-node-node-b")
	if err != nil || beta.Enabled || !reflect.DeepEqual(beta.Metadata["serves_models"], []any{}) {
		t.Fatalf("beta = %+v, %v", beta, err)
	}
	if eligible := EligibleCredentials(credentials, time.Now()); len(eligible) != 1 || eligible[0].ID != alpha.ID {
		t.Fatalf("eligible peers = %+v; want only the enabled peer", eligible)
	}

	// Router state on peer credentials must survive syncs that change nothing.
	if err = dataStore.TouchCredentialRotation(ctx, alpha.ID, 4, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err = dataStore.SetModelCooldown(ctx, beta.ID, "gpt-a", "upstream_rate_limited", "busy", time.Now().Add(time.Hour), 429, 1); err != nil {
		t.Fatal(err)
	}
	before := totalChanges(t, dataStore)
	reordered := []ClusterPeer{{NodeID: "node-b", Name: "Beta"}, {NodeID: "node-a", Name: "Alpha", Enabled: true, Models: []string{"gpt-a", "gpt-b"}}}
	if err = dataStore.SyncClusterPeers(ctx, reordered); err != nil {
		t.Fatal(err)
	}
	if after := totalChanges(t, dataStore); after != before {
		t.Fatalf("an unchanged sync wrote %d rows", after-before)
	}

	// The cluster link authenticates peers, so a peer the router marked
	// auth_required goes back into rotation on the next sync.
	if err = dataStore.MarkCredentialAuthRequired(ctx, alpha.ID, "upstream_auth"); err != nil {
		t.Fatal(err)
	}
	if err = dataStore.SyncClusterPeers(ctx, []ClusterPeer{{NodeID: "node-a", Name: "Alpha 2", Enabled: true, Models: []string{"gpt-c"}}}); err != nil {
		t.Fatal(err)
	}
	alpha, err = dataStore.CredentialByID(ctx, alpha.ID)
	if err != nil || alpha.Label != "Alpha 2" || alpha.Status != "healthy" || alpha.ConsecutiveUseCount != 4 || alpha.LastUsedAt.IsZero() || !reflect.DeepEqual(alpha.Metadata["serves_models"], []any{"gpt-c"}) {
		t.Fatalf("updated alpha = %+v, %v", alpha, err)
	}
	if _, err = dataStore.CredentialByID(ctx, beta.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("removed peer credential = %v; want sql.ErrNoRows", err)
	}
	var cooldowns int
	if err = dataStore.db.QueryRow(`SELECT COUNT(*) FROM credential_model_cooldowns WHERE credential_id=?`, beta.ID).Scan(&cooldowns); err != nil || cooldowns != 0 {
		t.Fatalf("removed peer kept %d model cooldowns, err=%v", cooldowns, err)
	}

	for _, check := range []struct {
		kind, id string
		want     bool
	}{{"provider", ClusterProviderID, true}, {"credential", alpha.ID, true}, {"credential", beta.ID, false}, {"provider", "other", false}} {
		if got, err := dataStore.IsClusterManaged(ctx, check.kind, check.id); err != nil || got != check.want {
			t.Fatalf("IsClusterManaged(%s, %s) = %v, %v; want %v", check.kind, check.id, got, err, check.want)
		}
	}
	if _, err = dataStore.IsClusterManaged(ctx, "route", "x"); err == nil {
		t.Fatal("an unknown kind must be an error")
	}

	if err = dataStore.ClearClusterPeers(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = dataStore.Provider(ctx, ClusterProviderID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("provider after clear = %v", err)
	}
	var remaining int
	if err = dataStore.db.QueryRow(`SELECT COUNT(*) FROM credentials WHERE provider_id=?`, ClusterProviderID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("credentials after clear = %d, %v", remaining, err)
	}
}

func TestSyncClusterPeersLeavesLocalProviderWithSameIDAlone(t *testing.T) {
	ctx := context.Background()
	dataStore, _ := openClusterTestStore(t, false)
	if err := dataStore.Seed(ctx, &config.Config{Providers: []config.ProviderConfig{{ID: ClusterProviderID, Type: "openai-compatible", Name: "Mine", BaseURL: "https://example.test", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.SyncClusterPeers(ctx, []ClusterPeer{{NodeID: "node-a", Enabled: true}}); err == nil {
		t.Fatal("sync must refuse to take over a local provider")
	}
	if err := dataStore.ClearClusterPeers(ctx); err != nil {
		t.Fatal(err)
	}
	provider, err := dataStore.Provider(ctx, ClusterProviderID)
	if err != nil || provider.Type != "openai-compatible" {
		t.Fatalf("local provider = %+v, %v", provider, err)
	}
	if managed, err := dataStore.IsClusterManaged(ctx, "provider", ClusterProviderID); err != nil || managed {
		t.Fatalf("local provider reported as cluster managed: %v, %v", managed, err)
	}
}

func TestSyncClusterPeersSkipsLocalCredentialID(t *testing.T) {
	ctx := context.Background()
	s, _ := openClusterTestStore(t, true)
	if err := s.Seed(ctx, &config.Config{Providers: []config.ProviderConfig{{ID: "local", Type: "openai-compatible", Name: "Local", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCredential(ctx, "local", config.CredentialConfig{ID: "cluster-node-clash", AuthType: "api_key", Secret: "local-secret"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncClusterPeers(ctx, []ClusterPeer{{NodeID: "clash"}, {NodeID: "other"}}); err != nil {
		t.Fatal(err)
	}
	credential, err := s.CredentialByID(ctx, "cluster-node-clash")
	if err != nil || credential.ProviderID != "local" || credential.Secret != "local-secret" {
		t.Fatalf("local credential = %+v, %v", credential, err)
	}
	peer, err := s.CredentialByID(ctx, "cluster-node-other")
	if err != nil || peer.ProviderID != ClusterProviderID {
		t.Fatalf("other peer = %+v, %v", peer, err)
	}
}

func TestClusterCatalogMovesAliasFromRetainedModel(t *testing.T) {
	ctx := context.Background()
	s, _ := openClusterTestStore(t, false)
	if err := s.Seed(ctx, &config.Config{Providers: []config.ProviderConfig{{ID: "local", Type: "openai-compatible", Name: "Local", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	_, err := s.ApplyClusterCatalog(ctx, ClusterCatalog{Models: []ClusterCatalogModel{{ID: "old", Enabled: true, Aliases: []string{"moving", "staying"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO route_targets(id,public_model_id,provider_id,upstream_model,priority,weight,enabled,conditions_json) VALUES('route','old','local','upstream',0,1,1,'{}')`); err != nil {
		t.Fatal(err)
	}
	_, err = s.ApplyClusterCatalog(ctx, ClusterCatalog{Models: []ClusterCatalogModel{{ID: "new", Enabled: true, Aliases: []string{"moving"}}}})
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.PublicModel(ctx, "old")
	if err != nil || !reflect.DeepEqual(old.Aliases, []string{"staying"}) {
		t.Fatalf("retained model = %+v, %v", old, err)
	}
	resolved, err := s.ResolveModel(ctx, "moving", "")
	if err != nil || resolved.ID != "new" {
		t.Fatalf("moved alias = %+v, %v", resolved, err)
	}
	exported, err := s.ExportConfig(ctx, &config.Config{})
	if err != nil || len(exported.Models) != 1 || exported.Models[0].ID != "old" || !reflect.DeepEqual(exported.Models[0].Aliases, []string{"staying"}) {
		t.Fatalf("exported models = %+v, %v", exported.Models, err)
	}
}

func TestClusterCatalogBuildApplyAndRemove(t *testing.T) {
	ctx := context.Background()
	hub, _ := openClusterTestStore(t, true)
	route := func(provider, upstream string) []config.RouteTargetConfig {
		return []config.RouteTargetConfig{{Provider: provider, UpstreamModel: upstream}}
	}
	if err := hub.Seed(ctx, &config.Config{
		Providers: []config.ProviderConfig{{ID: "hub-openai", Type: "openai-compatible", Name: "Hub", BaseURL: "https://hub.example.test/v1", Enabled: true}},
		Models: []config.PublicModelConfig{
			{ID: "gpt-shared", DisplayName: "GPT Shared", Aliases: []string{"gpt-alias", "taken-alias"}, Enabled: true, RewriteResponseModel: true, Capabilities: []string{"chat"}, Limits: map[string]any{"max_tokens": 4096}, Routes: route("hub-openai", "gpt-5")},
			{ID: "clash", DisplayName: "Hub Clash", Enabled: true, Routes: route("hub-openai", "gpt-clash")},
			{ID: "routed-later", Enabled: true, Routes: route("hub-openai", "gpt-routed")},
			{ID: "dropped-later", Enabled: true, Routes: route("hub-openai", "gpt-dropped")},
			{ID: "private", Enabled: true, Routes: route("hub-openai", "gpt-private")},
		},
		Combos: []config.ComboConfig{
			{ID: "combo-shared", Enabled: true, Items: []config.ComboItemConfig{{PublicModelID: "gpt-alias"}, {PublicModelID: "routed-later"}}},
			{ID: "combo-private", Enabled: true, Items: []config.ComboItemConfig{{PublicModelID: "gpt-shared"}, {PublicModelID: "private"}}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	_, plaintextAll, err := hub.CreateAPIKey(ctx, "key-all", "All models", nil, config.ClientKeyPolicy{Endpoints: []string{"/v1/chat/completions"}, CredentialIDs: []string{"hub-credential"}, Team: "hub-team", Tags: map[string]string{"env": "hub"}, Limits: config.LimitPolicy{RequestsPerMinute: 7}})
	if err != nil {
		t.Fatal(err)
	}
	_, plaintextList, err := hub.CreateAPIKey(ctx, "key-list", "Listed", []string{"gpt-shared", "private"})
	if err != nil {
		t.Fatal(err)
	}
	if err = hub.SaveModelAlias(ctx, ModelAlias{Alias: "scoped-alias", PublicModelID: "gpt-shared", APIKeyID: "key-all", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err = hub.SaveModelAlias(ctx, ModelAlias{Alias: "disabled-alias", PublicModelID: "gpt-shared"}); err != nil {
		t.Fatal(err)
	}
	shared := ClusterShared{
		Models:  []string{"routed-later", "gpt-shared", "clash", "dropped-later", "missing", "gpt-shared"},
		Combos:  []string{"combo-shared", "combo-private", "missing-combo"},
		APIKeys: []string{"key-list", "key-all", "missing-key"},
		Prices:  map[string]config.PricingConfig{"gpt-shared": {InputPerMillion: 1.5, OutputPerMillion: 6}},
	}
	catalog, err := hub.BuildClusterCatalog(ctx, shared)
	if err != nil {
		t.Fatal(err)
	}
	if ids := catalogModelIDs(catalog); !reflect.DeepEqual(ids, []string{"clash", "dropped-later", "gpt-shared", "routed-later"}) {
		t.Fatalf("catalog models = %v", ids)
	}
	gpt := catalog.Models[2]
	if gpt.DisplayName != "GPT Shared" || !gpt.Enabled || !gpt.RewriteResponseModel || !reflect.DeepEqual(gpt.Aliases, []string{"gpt-alias", "taken-alias"}) || !reflect.DeepEqual(gpt.Capabilities, []string{"chat"}) || gpt.Limits["max_tokens"] != float64(4096) {
		t.Fatalf("gpt-shared = %+v", gpt)
	}
	if gpt.Pricing == nil || *gpt.Pricing != (config.PricingConfig{InputPerMillion: 1.5, OutputPerMillion: 6}) || catalog.Models[0].Pricing != nil {
		t.Fatalf("pricing = %+v / %+v", gpt.Pricing, catalog.Models[0].Pricing)
	}
	// The alias item is shared under its model's id.
	wantItems := []ClusterCatalogComboItem{{Position: 0, PublicModelID: "gpt-shared"}, {Position: 1, PublicModelID: "routed-later"}}
	if len(catalog.Combos) != 1 || catalog.Combos[0].ID != "combo-shared" || !catalog.Combos[0].Enabled || !reflect.DeepEqual(catalog.Combos[0].Items, wantItems) {
		t.Fatalf("catalog combos = %+v", catalog.Combos)
	}
	if len(catalog.APIKeys) != 2 || catalog.APIKeys[0].ID != "key-all" || catalog.APIKeys[1].ID != "key-list" {
		t.Fatalf("catalog keys = %+v", catalog.APIKeys)
	}
	keyAll, keyList := catalog.APIKeys[0], catalog.APIKeys[1]
	if keyAll.KeyHash != security.HashAPIKey(plaintextAll) || !keyAll.Enabled || !reflect.DeepEqual(keyAll.Models, []string{"clash", "combo-shared", "dropped-later", "gpt-shared", "routed-later"}) {
		t.Fatalf("key-all = %+v", keyAll)
	}
	if keyAll.Policy.CredentialIDs != nil || keyAll.Policy.Team != "" || keyAll.Policy.Tags != nil || !reflect.DeepEqual(keyAll.Policy.Endpoints, []string{"/v1/chat/completions"}) || keyAll.Policy.Limits.RequestsPerMinute != 7 {
		t.Fatalf("key-all policy = %+v", keyAll.Policy)
	}
	if !reflect.DeepEqual(keyList.Models, []string{"gpt-shared"}) {
		t.Fatalf("key-list models = %v", keyList.Models)
	}

	member, _ := openClusterTestStore(t, true)
	if err = member.Seed(ctx, &config.Config{
		Providers: []config.ProviderConfig{{ID: "member-openai", Type: "openai-compatible", Name: "Member", BaseURL: "https://member.example.test/v1", Enabled: true}},
		Models: []config.PublicModelConfig{
			{ID: "clash", DisplayName: "Member Clash", Enabled: true, Routes: route("member-openai", "local-clash")},
			{ID: "member-model", Aliases: []string{"taken-alias"}, Enabled: true, Routes: route("member-openai", "local-model")},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = member.CreateAPIKey(ctx, "key-list", "Member key", nil); err != nil {
		t.Fatal(err)
	}
	report, err := member.ApplyClusterCatalog(ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	wantReport := ClusterApplyReport{
		Models:  []string{"dropped-later", "gpt-shared", "routed-later"},
		Combos:  []string{"combo-shared"},
		APIKeys: []string{"key-all"},
		Conflicts: []ClusterCatalogConflict{
			{Kind: "model", ID: "clash", Reason: "local_model_exists"},
			{Kind: "alias", ID: "taken-alias", Reason: "alias_in_use"},
			{Kind: "api_key", ID: "key-list", Reason: "local_key_exists"},
		},
	}
	if !reflect.DeepEqual(report, wantReport) {
		t.Fatalf("apply report = %+v\nwant %+v", report, wantReport)
	}
	if again, err := member.ApplyClusterCatalog(ctx, catalog); err != nil || !reflect.DeepEqual(again, wantReport) {
		t.Fatalf("re-applying the same catalog = %+v, %v", again, err)
	}

	clash, err := member.PublicModel(ctx, "clash")
	if err != nil || clash.DisplayName != "Member Clash" {
		t.Fatalf("local model after conflict = %+v, %v", clash, err)
	}
	if routes, err := member.Routes(ctx, "clash"); err != nil || len(routes) != 1 || routes[0].ProviderID != "member-openai" {
		t.Fatalf("local routes after conflict = %+v, %v", routes, err)
	}
	if resolved, err := member.ResolveModel(ctx, "taken-alias", ""); err != nil || resolved.ID != "member-model" {
		t.Fatalf("local alias after conflict = %+v, %v", resolved, err)
	}
	if resolved, err := member.ResolveModel(ctx, "gpt-alias", ""); err != nil || resolved.ID != "gpt-shared" {
		t.Fatalf("hub alias on member = %+v, %v", resolved, err)
	}
	if combo, err := member.ResolveCombo(ctx, "combo-shared"); err != nil || len(combo.ComboItems) != 2 || combo.ComboItems[0].PublicModelID != "gpt-shared" {
		t.Fatalf("hub combo on member = %+v, %v", combo, err)
	}
	authenticated, err := member.AuthenticateAPIKey(ctx, plaintextAll)
	if err != nil || authenticated.ID != "key-all" || authenticated.Policy.Team != "" {
		t.Fatalf("hub key on member = %+v, %v", authenticated, err)
	}
	// "clash" is the member's own model here, so the hub key must not reach it.
	if !reflect.DeepEqual(authenticated.Models, []string{"combo-shared", "dropped-later", "gpt-shared", "routed-later"}) {
		t.Fatalf("hub key models on member = %v", authenticated.Models)
	}
	if _, err = member.AuthenticateAPIKey(ctx, plaintextList); err == nil {
		t.Fatal("a hub key that conflicted must not be installed")
	}
	managed, err := member.ClusterManagedItems(ctx)
	wantManaged := ClusterManaged{Models: []string{"dropped-later", "gpt-shared", "routed-later"}, Aliases: []string{"gpt-alias"}, Combos: []string{"combo-shared"}, APIKeys: []string{"key-all"}}
	if err != nil || !reflect.DeepEqual(managed, wantManaged) {
		t.Fatalf("managed = %+v, %v", managed, err)
	}
	for _, check := range []struct {
		kind, id string
		want     bool
	}{{"model", "gpt-shared", true}, {"model", "clash", false}, {"alias", "gpt-alias", true}, {"alias", "taken-alias", false}, {"combo", "combo-shared", true}, {"api_key", "key-all", true}, {"api_key", "key-list", false}} {
		if got, err := member.IsClusterManaged(ctx, check.kind, check.id); err != nil || got != check.want {
			t.Fatalf("IsClusterManaged(%s, %s) = %v, %v; want %v", check.kind, check.id, got, err, check.want)
		}
	}

	// The member attaches a local route to one hub model, then the hub stops
	// sharing it and another model.
	if _, err = member.db.ExecContext(ctx, `INSERT INTO route_targets(id,public_model_id,provider_id,upstream_model,priority,weight,enabled,conditions_json) VALUES('member-route','routed-later','member-openai','local-routed',0,1,1,'{}')`); err != nil {
		t.Fatal(err)
	}
	shared.Models = []string{"gpt-shared", "clash"}
	narrowed, err := hub.BuildClusterCatalog(ctx, shared)
	if err != nil || len(narrowed.Combos) != 0 {
		t.Fatalf("narrowed catalog = %+v, %v; want the combo dropped with its model", narrowed, err)
	}
	report, err = member.ApplyClusterCatalog(ctx, narrowed)
	if err != nil || !reflect.DeepEqual(report.Models, []string{"gpt-shared"}) || len(report.Combos) != 0 || !reflect.DeepEqual(report.APIKeys, []string{"key-all"}) {
		t.Fatalf("narrowed report = %+v, %v", report, err)
	}
	if kept, err := member.PublicModel(ctx, "routed-later"); err != nil || kept.ID != "routed-later" {
		t.Fatalf("model with a local route = %+v, %v; want it kept", kept, err)
	}
	if routes, err := member.Routes(ctx, "routed-later"); err != nil || len(routes) != 1 {
		t.Fatalf("local route on kept model = %+v, %v", routes, err)
	}
	if _, err = member.PublicModel(ctx, "dropped-later"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("model without local routes = %v; want deleted", err)
	}
	if _, err = member.ResolveCombo(ctx, "combo-shared"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale combo = %v; want deleted", err)
	}
	if authenticated, err = member.AuthenticateAPIKey(ctx, plaintextAll); err != nil || !reflect.DeepEqual(authenticated.Models, []string{"gpt-shared"}) {
		t.Fatalf("hub key after narrowing = %+v, %v", authenticated, err)
	}
	managed, err = member.ClusterManagedItems(ctx)
	wantManaged = ClusterManaged{Models: []string{"gpt-shared"}, Aliases: []string{"gpt-alias"}, Combos: []string{}, APIKeys: []string{"key-all"}}
	if err != nil || !reflect.DeepEqual(managed, wantManaged) {
		t.Fatalf("managed after narrowing = %+v, %v", managed, err)
	}

	if err = member.ClearClusterManaged(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = member.PublicModel(ctx, "gpt-shared"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("hub model after clear = %v", err)
	}
	if _, err = member.ResolveModel(ctx, "gpt-alias", ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("hub alias after clear = %v", err)
	}
	if _, err = member.AuthenticateAPIKey(ctx, plaintextAll); err == nil {
		t.Fatal("hub key must be removed by clear")
	}
	for _, id := range []string{"clash", "member-model", "routed-later"} {
		if _, err = member.PublicModel(ctx, id); err != nil {
			t.Fatalf("local model %s after clear: %v", id, err)
		}
	}
	if managed, err = member.ClusterManagedItems(ctx); err != nil || len(managed.Models)+len(managed.Aliases)+len(managed.Combos)+len(managed.APIKeys) != 0 {
		t.Fatalf("managed after clear = %+v, %v", managed, err)
	}
}

func catalogModelIDs(catalog ClusterCatalog) []string {
	ids := []string{}
	for _, model := range catalog.Models {
		ids = append(ids, model.ID)
	}
	return ids
}

func TestClusterLendingSummaryGroupsBorrowersByDay(t *testing.T) {
	ctx := context.Background()
	dataStore, _ := openClusterTestStore(t, false)
	if _, err := dataStore.db.ExecContext(ctx, `INSERT INTO cluster_managed_items(kind,item_id,updated_at) VALUES('api_key','hub-key',?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	at := func(value string) time.Time {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	for _, event := range []UsageEvent{
		{RequestID: "r1", ClientAPIKeyID: "cluster:node-a", Attempt: 1, Status: 429, InputTokens: 10, EstimatedCostUSD: 0.1, CreatedAt: at("2026-10-31T10:00:00Z")},
		{RequestID: "r1", ClientAPIKeyID: "cluster:node-a", Attempt: 2, Status: 200, InputTokens: 20, OutputTokens: 12, EstimatedCostUSD: 0.2, CreatedAt: at("2026-10-31T10:00:01Z")},
		{RequestID: "r2", ClientAPIKeyID: "cluster:node-a", Attempt: 1, Status: 200, InputTokens: 1, OutputTokens: 1, EstimatedCostUSD: 0.01, CreatedAt: at("2026-11-01T00:00:00.5Z")},
		{RequestID: "r3", ClientAPIKeyID: "hub-key", Attempt: 1, Status: 200, InputTokens: 3, OutputTokens: 4, EstimatedCostUSD: 0.03, CreatedAt: at("2026-11-01T08:00:00Z")},
		{RequestID: "r4", ClientAPIKeyID: "local-key", Attempt: 1, Status: 200, InputTokens: 99, CreatedAt: at("2026-11-01T09:00:00Z")},
		{RequestID: "r5", ClientAPIKeyID: "cluster:node-b", Attempt: 1, Status: 200, InputTokens: 50, CreatedAt: at("2026-10-29T23:59:59.999Z")},
	} {
		if err := dataStore.AddUsage(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := dataStore.ClusterLendingSummary(ctx, "2026-10-30")
	if err != nil {
		t.Fatal(err)
	}
	want := []ClusterLedgerRow{
		{Day: "2026-11-01", Borrower: "key:hub-key", Requests: 1, InputTokens: 3, OutputTokens: 4, CostUSD: 0.03},
		{Day: "2026-11-01", Borrower: "node:node-a", Requests: 1, InputTokens: 1, OutputTokens: 1, CostUSD: 0.01},
		{Day: "2026-10-31", Borrower: "node:node-a", Requests: 1, InputTokens: 30, OutputTokens: 12, CostUSD: 0.3},
	}
	if len(summary) != len(want) {
		t.Fatalf("summary = %+v want %+v", summary, want)
	}
	for index := range want {
		got := summary[index]
		if math.Abs(got.CostUSD-want[index].CostUSD) > 1e-9 {
			t.Fatalf("row %d cost = %v want %v", index, got.CostUSD, want[index].CostUSD)
		}
		got.CostUSD = want[index].CostUSD
		if got != want[index] {
			t.Fatalf("row %d = %+v want %+v", index, got, want[index])
		}
	}
	// The whole first day counts, including its first fractional second.
	if summary, err = dataStore.ClusterLendingSummary(ctx, "2026-11-01"); err != nil || len(summary) != 2 {
		t.Fatalf("summary since 2026-11-01 = %+v, %v", summary, err)
	}
	if _, err = dataStore.ClusterLendingSummary(ctx, "2026-11-01T00:00:00Z"); err == nil {
		t.Fatal("since must be a YYYY-MM-DD day")
	}
}

func TestClusterLendingSummaryPersistsBorrowers(t *testing.T) {
	ctx := context.Background()
	dataStore, _ := openClusterTestStore(t, false)
	if _, err := dataStore.db.ExecContext(ctx, `INSERT INTO cluster_managed_items(kind,item_id,updated_at) VALUES('api_key','hub-key',?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, event := range []UsageEvent{
		{RequestID: "key-request", ClientAPIKeyID: "hub-key", ClusterBorrower: "key:hub-key", InputTokens: 2, CreatedAt: now},
		{RequestID: "key-request", ClientAPIKeyID: "hub-key", InputTokens: 3, CreatedAt: now},
		{RequestID: "relay-request", ClientAPIKeyID: "different-principal", ClusterBorrower: "node:origin", OutputTokens: 4, CreatedAt: now},
	} {
		if err := dataStore.AddUsage(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := dataStore.ClusterLendingSummary(ctx, "")
	if err != nil || len(summary) != 2 || summary[0].Borrower != "key:hub-key" || summary[0].Requests != 1 || summary[0].InputTokens != 5 || summary[1].Borrower != "node:origin" || summary[1].OutputTokens != 4 {
		t.Fatalf("mixed summary = %+v, %v", summary, err)
	}
	if err := dataStore.ClearClusterManaged(ctx); err != nil {
		t.Fatal(err)
	}
	summary, err = dataStore.ClusterLendingSummary(ctx, "")
	if err != nil || len(summary) != 2 || summary[0].Borrower != "key:hub-key" || summary[0].Requests != 1 || summary[0].InputTokens != 2 || summary[1].Borrower != "node:origin" || summary[1].Requests != 1 {
		t.Fatalf("summary after unsharing = %+v, %v", summary, err)
	}
	usage, _, err := dataStore.UsageEvents(ctx, UsageEventsQuery{})
	if err != nil || len(usage) != 3 || usage[0].ClusterBorrower != "node:origin" || usage[2].ClusterBorrower != "key:hub-key" {
		t.Fatalf("usage events = %+v, %v", usage, err)
	}
}

func TestExportConfigLeavesOutClusterRows(t *testing.T) {
	ctx := context.Background()
	dataStore, _ := openClusterTestStore(t, true)
	local := &config.Config{
		Providers: []config.ProviderConfig{{ID: "local-openai", Type: "openai-compatible", Name: "Local", BaseURL: "https://local.example.test/v1", Enabled: true}},
		Models:    []config.PublicModelConfig{{ID: "local-model", Enabled: true, Routes: []config.RouteTargetConfig{{Provider: "local-openai", UpstreamModel: "gpt-local"}}}},
		Combos:    []config.ComboConfig{{ID: "local-combo", Enabled: true, Items: []config.ComboItemConfig{{PublicModelID: "local-model"}}}},
	}
	if err := dataStore.Seed(ctx, local); err != nil {
		t.Fatal(err)
	}
	if _, _, err := dataStore.CreateAPIKey(ctx, "local-key", "Local", nil); err != nil {
		t.Fatal(err)
	}
	report, err := dataStore.ApplyClusterCatalog(ctx, ClusterCatalog{
		Models:  []ClusterCatalogModel{{ID: "hub-model", Enabled: true, Aliases: []string{"hub-alias"}}},
		Combos:  []ClusterCatalogCombo{{ID: "hub-combo", Enabled: true, Items: []ClusterCatalogComboItem{{Position: 0, PublicModelID: "hub-model"}}}},
		APIKeys: []ClusterCatalogAPIKey{{ID: "hub-key", Name: "Hub", KeyHash: security.HashAPIKey("tp_hub-secret"), Enabled: true, Models: []string{"hub-model"}}},
	})
	if err != nil || len(report.Conflicts) != 0 || len(report.Models) != 1 || len(report.Combos) != 1 || len(report.APIKeys) != 1 {
		t.Fatalf("apply = %+v, %v", report, err)
	}
	if err = dataStore.SyncClusterPeers(ctx, []ClusterPeer{{NodeID: "node-a", Name: "Alpha", Enabled: true, Models: []string{"hub-model"}}}); err != nil {
		t.Fatal(err)
	}
	for name, export := range map[string]func(context.Context, *config.Config) (*config.Config, error){
		"ExportConfig":                dataStore.ExportConfig,
		"ExportConfigWithOAuthTokens": dataStore.ExportConfigWithOAuthTokens,
	} {
		exported, err := export(ctx, local)
		if err != nil {
			t.Fatal(err)
		}
		var providers, models, combos, keys []string
		for _, provider := range exported.Providers {
			providers = append(providers, provider.ID)
		}
		for _, model := range exported.Models {
			models = append(models, model.ID)
		}
		for _, combo := range exported.Combos {
			combos = append(combos, combo.ID)
		}
		for _, key := range exported.ClientAPIKeys {
			keys = append(keys, key.ID)
		}
		got := [][]string{providers, models, combos, keys}
		want := [][]string{{"local-openai"}, {"local-model"}, {"local-combo"}, {"local-key"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s exported %v; want %v", name, got, want)
		}
	}
}
