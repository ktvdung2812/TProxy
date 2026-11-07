package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tproxy/tproxy/internal/config"
)

// ClusterProviderID is both the id and the type of the provider whose
// credentials are the cluster peers. SaveProvider and config.Validate reject the
// type, so only SyncClusterPeers creates it.
const ClusterProviderID = "tproxy-cluster"

const (
	AppSettingClusterNode       = "cluster.node"
	AppSettingClusterMembership = "cluster.membership"
	AppSettingClusterLending    = "cluster.lending"
	AppSettingClusterBorrowing  = "cluster.borrowing"
	AppSettingClusterShared     = "cluster.shared"
)

var (
	ErrClusterMasterKeyRequired = errors.New("store: TPROXY_MASTER_KEY is required to store the cluster node token")
	ErrClusterJoinTokenInvalid  = errors.New("store: invalid cluster join token")
	ErrClusterJoinTokenExpired  = errors.New("store: cluster join token expired")
)

type ClusterIdentity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ClusterMembership struct {
	Role          string    `json:"role"`
	ClusterID     string    `json:"cluster_id"`
	ClusterName   string    `json:"cluster_name"`
	HubURL        string    `json:"hub_url"`
	HubNodeID     string    `json:"hub_node_id"`
	AllowInsecure bool      `json:"allow_insecure"`
	Revoked       bool      `json:"revoked"`
	JoinedAt      time.Time `json:"joined_at"`
	NodeToken     string    `json:"-"`
}

type ClusterLending struct {
	Enabled bool               `json:"enabled"`
	Models  []string           `json:"models"`
	Limits  config.LimitPolicy `json:"limits"`
}

type ClusterBorrowing struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`
}

type ClusterShared struct {
	Models  []string                        `json:"models"`
	Combos  []string                        `json:"combos"`
	APIKeys []string                        `json:"api_keys"`
	Prices  map[string]config.PricingConfig `json:"prices,omitempty"`
}

// clusterMembershipRecord is the stored membership: the node token reaches
// app_settings only as ciphertext.
type clusterMembershipRecord struct {
	ClusterMembership
	NodeTokenCiphertext string `json:"node_token_ciphertext,omitempty"`
}

// ClusterIdentity returns this node's cluster identity, creating and persisting
// it on first use.
func (s *Store) ClusterIdentity(ctx context.Context) (ClusterIdentity, error) {
	var identity ClusterIdentity
	if _, err := s.loadClusterSetting(ctx, AppSettingClusterNode, &identity); err != nil {
		return ClusterIdentity{}, err
	}
	if identity.ID != "" {
		return identity, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Read again under the write lock so concurrent first calls agree on one id.
	identity = ClusterIdentity{}
	if _, err := s.readClusterSetting(ctx, AppSettingClusterNode, &identity); err != nil {
		return ClusterIdentity{}, err
	}
	if identity.ID != "" {
		return identity, nil
	}
	identity.ID = uuid.NewString()
	if strings.TrimSpace(identity.Name) == "" {
		identity.Name = defaultClusterNodeName()
	}
	if err := s.writeClusterSetting(ctx, AppSettingClusterNode, identity); err != nil {
		return ClusterIdentity{}, err
	}
	return identity, nil
}

func (s *Store) SaveClusterIdentity(ctx context.Context, id ClusterIdentity) error {
	id.ID, id.Name = strings.TrimSpace(id.ID), strings.TrimSpace(id.Name)
	if id.ID == "" {
		return errors.New("cluster node id is required")
	}
	if id.Name == "" {
		id.Name = defaultClusterNodeName()
	}
	return s.saveClusterSetting(ctx, AppSettingClusterNode, id)
}

func defaultClusterNodeName() string {
	if name, err := os.Hostname(); err == nil && strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	return "tproxy"
}

// ClusterMembership returns the zero value when the node never joined. When the
// node token cannot be decrypted the other fields are still returned alongside
// the error, so callers can tell which cluster the broken token belonged to.
func (s *Store) ClusterMembership(ctx context.Context) (ClusterMembership, error) {
	var record clusterMembershipRecord
	if _, err := s.loadClusterSetting(ctx, AppSettingClusterMembership, &record); err != nil {
		return ClusterMembership{}, err
	}
	membership := record.ClusterMembership
	if record.NodeTokenCiphertext != "" {
		token, err := s.encryptor.Decrypt(record.NodeTokenCiphertext)
		if err != nil {
			return membership, fmt.Errorf("decrypt cluster node token: %w", err)
		}
		membership.NodeToken = token
	}
	return membership, nil
}

func (s *Store) SaveClusterMembership(ctx context.Context, m ClusterMembership) error {
	record := clusterMembershipRecord{ClusterMembership: m}
	if m.NodeToken != "" {
		ciphertext, err := s.encryptor.Encrypt(m.NodeToken)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrClusterMasterKeyRequired, err)
		}
		record.NodeTokenCiphertext = ciphertext
	}
	return s.saveClusterSetting(ctx, AppSettingClusterMembership, record)
}

// CanEncrypt reports whether secrets such as the cluster node token can be
// stored, which needs a master key.
func (s *Store) CanEncrypt() bool {
	_, err := s.encryptor.Encrypt("probe")
	return err == nil
}

func (s *Store) ClearClusterMembership(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM app_settings WHERE key=?`, AppSettingClusterMembership)
	return err
}

func (s *Store) ClusterLending(ctx context.Context) (ClusterLending, bool, error) {
	var lending ClusterLending
	found, err := s.loadClusterSetting(ctx, AppSettingClusterLending, &lending)
	if err != nil {
		return ClusterLending{}, false, err
	}
	return lending, found, nil
}

func (s *Store) SaveClusterLending(ctx context.Context, p ClusterLending) error {
	return s.saveClusterSetting(ctx, AppSettingClusterLending, p)
}

func (s *Store) ClusterBorrowing(ctx context.Context) (ClusterBorrowing, bool, error) {
	var borrowing ClusterBorrowing
	found, err := s.loadClusterSetting(ctx, AppSettingClusterBorrowing, &borrowing)
	if err != nil {
		return ClusterBorrowing{}, false, err
	}
	return borrowing, found, nil
}

func (s *Store) SaveClusterBorrowing(ctx context.Context, p ClusterBorrowing) error {
	return s.saveClusterSetting(ctx, AppSettingClusterBorrowing, p)
}

func (s *Store) ClusterShared(ctx context.Context) (ClusterShared, error) {
	var shared ClusterShared
	if _, err := s.loadClusterSetting(ctx, AppSettingClusterShared, &shared); err != nil {
		return ClusterShared{}, err
	}
	return shared, nil
}

func (s *Store) SaveClusterShared(ctx context.Context, sh ClusterShared) error {
	return s.saveClusterSetting(ctx, AppSettingClusterShared, sh)
}

// loadClusterSetting decodes one app setting into target and reports whether it
// was ever saved.
func (s *Store) loadClusterSetting(ctx context.Context, key string, target any) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readClusterSetting(ctx, key, target)
}

func (s *Store) saveClusterSetting(ctx context.Context, key string, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeClusterSetting(ctx, key, value)
}

// readClusterSetting and writeClusterSetting expect the caller to hold s.mu.
func (s *Store) readClusterSetting(ctx context.Context, key string, target any) (bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value_json FROM app_settings WHERE key=?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = json.Unmarshal([]byte(raw), target); err != nil {
		return true, fmt.Errorf("decode %s setting: %w", key, err)
	}
	return true, nil
}

func (s *Store) writeClusterSetting(ctx context.Context, key string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO app_settings(key,value_json) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json`, key, string(encoded))
	return err
}

// clusterIDs trims, de-duplicates and sorts ids so repeated syncs compare and
// store them identically.
func clusterIDs(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func clusterTimeText(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseClusterTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func validateClusterDay(day string) error {
	if day == "" {
		return nil
	}
	if _, err := time.Parse(time.DateOnly, day); err != nil {
		return fmt.Errorf("invalid cluster day %q: want YYYY-MM-DD", day)
	}
	return nil
}
