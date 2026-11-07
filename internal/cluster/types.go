// Package cluster lets several tproxy nodes borrow each other's account
// capacity without copying credentials. One hub has a reachable URL; members
// dial out to it over a WebSocket that carries HTTP/2, and the hub relays
// member-to-member traffic.
package cluster

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/tproxy/tproxy/internal/config"
)

type Role string

const (
	RoleStandalone Role = "standalone"
	RoleHub        Role = "hub"
	RoleMember     Role = "member"
)

const (
	ProtocolVersion  = "1"
	HeaderProtocol   = "X-TProxy-Cluster-Protocol"
	HeaderNode       = "X-TProxy-Cluster-Node"
	HeaderOrigin     = "X-TProxy-Cluster-Origin"
	HeaderOriginName = "X-TProxy-Cluster-Origin-Name"
	HeaderModel      = "X-TProxy-Cluster-Model"
	HubPathPrefix    = "/api/cluster/v1/"
	LinkPathPrefix   = "/_cluster/v1/"
	ProviderID       = "tproxy-cluster"
	CredentialPrefix = "cluster-node-"
	PrincipalPrefix  = "cluster:"
	JoinTokenPrefix  = "tpc1_"
	ModeFallback     = "fallback"
	ModeShared       = "shared"
	NodeActive       = "active"
	NodeRevoked      = "revoked"
	NodeLeft         = "left"
)

// Link states reported in StatusView.Link.State.
const (
	LinkConnecting = "connecting"
	LinkConnected  = "connected"
	LinkRevoked    = "revoked"
	LinkError      = "error"
	LinkOffline    = "offline"
)

// Warnings reported in StatusView.Warnings.
const (
	WarningInsecureHubURL = "insecure_hub_url"
	WarningHubUnreachable = "hub_unreachable"
)

// Error codes carried by *Error and by hub error responses.
const (
	CodeNotConfigured     = "cluster_not_configured"
	CodeAlreadyConfigured = "cluster_already_configured"
	CodeInvalidJoinToken  = "invalid_join_token"
	CodeJoinTokenExpired  = "join_token_expired"
	CodeHubUnreachable    = "hub_unreachable"
	CodeInsecureHubURL    = "insecure_hub_url"
	CodeMasterKeyRequired = "master_key_required"
	CodeNotFound          = "not_found"
	CodeInvalidRequest    = "invalid_request"
	CodeUnauthorized      = "cluster_unauthorized"
	CodeNodeRevoked       = "cluster_node_revoked"
	CodeProtocolMismatch  = "cluster_protocol_mismatch"
	CodeRelayLoop         = "cluster_relay_loop"
	CodeNodeUnknown       = "cluster_node_unknown"
	CodeNodeOffline       = "cluster_node_offline"
	CodeNodeBusy          = "cluster_node_busy"
	CodeNodeExists        = "cluster_node_exists"
	CodeRelayFailed       = "cluster_relay_failed"
	CodeLendingDisabled   = "cluster_lending_disabled"
	CodeModelNotLent      = "cluster_model_not_lent"
	CodeRequestTooLarge   = "request_too_large"
	CodeMethodNotAllowed  = "method_not_allowed"
	CodeUnavailable       = "cluster_unavailable"
	CodeInternal          = "internal_error"
)

type NodeIdentity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Membership struct {
	Role          Role      `json:"role"`
	ClusterID     string    `json:"cluster_id,omitempty"`
	ClusterName   string    `json:"cluster_name,omitempty"`
	HubURL        string    `json:"hub_url,omitempty"`
	HubNodeID     string    `json:"hub_node_id,omitempty"`
	AllowInsecure bool      `json:"allow_insecure,omitempty"`
	Revoked       bool      `json:"revoked,omitempty"`
	JoinedAt      time.Time `json:"joined_at,omitzero"`
	NodeToken     string    `json:"-"`
}

// LendingPolicy controls what this node lends. Empty Models lends every
// catalog model.
type LendingPolicy struct {
	Enabled bool               `json:"enabled"`
	Models  []string           `json:"models"`
	Limits  config.LimitPolicy `json:"limits"`
}

type BorrowingPolicy struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`
}

func DefaultLendingPolicy() LendingPolicy {
	return LendingPolicy{Enabled: true, Models: []string{}, Limits: config.LimitPolicy{RequestsPerMinute: 20, ConcurrentStreams: 2}}
}

func DefaultBorrowingPolicy() BorrowingPolicy {
	return BorrowingPolicy{Enabled: true, Mode: ModeFallback}
}

type SharedSelection struct {
	Models  []string                        `json:"models"`
	Combos  []string                        `json:"combos"`
	APIKeys []string                        `json:"api_keys"`
	Prices  map[string]config.PricingConfig `json:"prices,omitempty"`
}

type Offer struct {
	ModelID          string `json:"model_id"`
	EligibleAccounts int    `json:"eligible_accounts"`
	Healthy          bool   `json:"healthy"`
}

type LedgerRow struct {
	Day          string  `json:"day"`
	Lender       string  `json:"lender"`
	Borrower     string  `json:"borrower"` // "node:<id>" or "key:<id>"
	Requests     int64   `json:"requests"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

type Inventory struct {
	NodeID  string        `json:"node_id"`
	Name    string        `json:"name"`
	Version string        `json:"version"`
	Lending LendingPolicy `json:"lending"`
	Offers  []Offer       `json:"offers"`
	Ledger  []LedgerRow   `json:"ledger"`
}

type NodeState struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Owner          string    `json:"owner,omitempty"`
	Role           Role      `json:"role"`
	Status         string    `json:"status"`
	Online         bool      `json:"online"`
	Version        string    `json:"version,omitempty"`
	LastSeenAt     time.Time `json:"last_seen_at,omitzero"`
	RTTMS          int64     `json:"rtt_ms"`
	LendingEnabled bool      `json:"lending_enabled"`
	Offers         []Offer   `json:"offers"`
}

type State struct {
	Version        string      `json:"version"`
	ClusterID      string      `json:"cluster_id"`
	ClusterName    string      `json:"cluster_name"`
	HubNodeID      string      `json:"hub_node_id"`
	CatalogVersion string      `json:"catalog_version"`
	Nodes          []NodeState `json:"nodes"`
	GeneratedAt    time.Time   `json:"generated_at"`
}

type CatalogModel struct {
	ID                   string                `json:"id"`
	DisplayName          string                `json:"display_name"`
	Aliases              []string              `json:"aliases,omitempty"`
	Enabled              bool                  `json:"enabled"`
	ExposeUpstreamName   bool                  `json:"expose_upstream_name,omitempty"`
	RewriteResponseModel bool                  `json:"rewrite_response_model,omitempty"`
	Capabilities         []string              `json:"capabilities,omitempty"`
	Limits               map[string]any        `json:"limits,omitempty"`
	Policy               map[string]any        `json:"policy,omitempty"`
	Pricing              *config.PricingConfig `json:"pricing,omitempty"`
}

type CatalogComboItem struct {
	Position      int    `json:"position"`
	PublicModelID string `json:"public_model_id"`
}

type CatalogCombo struct {
	ID                   string             `json:"id"`
	DisplayName          string             `json:"display_name"`
	Enabled              bool               `json:"enabled"`
	RewriteResponseModel bool               `json:"rewrite_response_model,omitempty"`
	Capabilities         []string           `json:"capabilities,omitempty"`
	Limits               map[string]any     `json:"limits,omitempty"`
	Policy               map[string]any     `json:"policy,omitempty"`
	Items                []CatalogComboItem `json:"items"`
}

type CatalogAPIKey struct {
	ID      string                 `json:"id"`
	Name    string                 `json:"name"`
	KeyHash string                 `json:"key_hash"`
	Enabled bool                   `json:"enabled"`
	Models  []string               `json:"models"`
	Policy  config.ClientKeyPolicy `json:"policy"`
}

type Catalog struct {
	Version string          `json:"version"`
	Models  []CatalogModel  `json:"models"`
	Combos  []CatalogCombo  `json:"combos"`
	APIKeys []CatalogAPIKey `json:"api_keys"`
}

// CatalogConflict reports a catalog entry a member could not apply. Kind is
// one of model, combo, api_key or alias.
type CatalogConflict struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

type ApplyReport struct {
	Version   string
	Models    []string
	Combos    []string
	APIKeys   []string
	Conflicts []CatalogConflict
}

type JoinTokenRecord struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	MaxUses   int       `json:"max_uses"`
	Uses      int       `json:"uses"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
	RevokedAt time.Time `json:"revoked_at,omitzero"`
}

type NodeRecord struct {
	ID, Name, Owner, Status, Version, LastAddr string
	JoinedAt, LastSeenAt                       time.Time
}

// LedgerView is a ledger row with display names resolved for the dashboard.
type LedgerView struct {
	LedgerRow
	LenderName   string `json:"lender_name"`
	BorrowerName string `json:"borrower_name"`
}

var (
	ErrNotFound          = errors.New("cluster: not found")
	ErrJoinTokenInvalid  = errors.New("cluster: invalid join token")
	ErrJoinTokenExpired  = errors.New("cluster: join token expired")
	ErrMasterKeyRequired = errors.New("cluster: master key required")
)

// Error is returned by admin operations; Status is the HTTP status the API
// layer should answer with.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

func newError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

type SettingsStore interface {
	// NodeIdentity creates and persists {uuid, hostname} on first call.
	NodeIdentity(ctx context.Context) (NodeIdentity, error)
	SaveNodeIdentity(ctx context.Context, id NodeIdentity) error
	// Membership returns the zero value when absent, which means standalone.
	Membership(ctx context.Context) (Membership, error)
	// SaveMembership wraps ErrMasterKeyRequired when the node token cannot be
	// encrypted.
	SaveMembership(ctx context.Context, m Membership) error
	ClearMembership(ctx context.Context) error
	// Lending reports false when nothing was ever saved.
	Lending(ctx context.Context) (LendingPolicy, bool, error)
	SaveLending(ctx context.Context, p LendingPolicy) error
	Borrowing(ctx context.Context) (BorrowingPolicy, bool, error)
	SaveBorrowing(ctx context.Context, p BorrowingPolicy) error
	Shared(ctx context.Context) (SharedSelection, error)
	SaveShared(ctx context.Context, s SharedSelection) error
}

type HubStore interface {
	CreateJoinToken(ctx context.Context, rec JoinTokenRecord, secretHash string) error
	// ConsumeJoinToken atomically increments uses; it fails with
	// ErrJoinTokenInvalid or ErrJoinTokenExpired.
	ConsumeJoinToken(ctx context.Context, secretHash string, now time.Time) (JoinTokenRecord, error)
	JoinTokens(ctx context.Context) ([]JoinTokenRecord, error)
	RevokeJoinToken(ctx context.Context, id string, at time.Time) error // ErrNotFound
	// SaveNode upserts by id; an empty tokenHash keeps the stored hash.
	SaveNode(ctx context.Context, rec NodeRecord, tokenHash string) error
	NodeByTokenHash(ctx context.Context, tokenHash string) (NodeRecord, error) // ErrNotFound
	Nodes(ctx context.Context) ([]NodeRecord, error)
	UpdateNode(ctx context.Context, id string, name, owner *string) error // ErrNotFound
	SetNodeStatus(ctx context.Context, id, status string) error           // ErrNotFound
	TouchNode(ctx context.Context, id, addr, version string, at time.Time) error
	UpsertLedger(ctx context.Context, rows []LedgerRow) error
	Ledger(ctx context.Context, sinceDay string) ([]LedgerRow, error)
	ResetHub(ctx context.Context) error
}

type Local interface {
	// LocalCapacity counts eligible local accounts for a public model.
	LocalCapacity(ctx context.Context, modelID string) (int, error)
	// LendingLedger leaves Lender empty; the service fills it.
	LendingLedger(ctx context.Context, sinceDay string) ([]LedgerRow, error)
	// BuildCatalog runs on the hub; the service computes Version.
	BuildCatalog(ctx context.Context, shared SharedSelection) (Catalog, error)
	// ApplyCatalog runs on members.
	ApplyCatalog(ctx context.Context, catalog Catalog) (ApplyReport, error)
	// ApplyPeers receives every node except this one.
	ApplyPeers(ctx context.Context, peers []NodeState) error
	// ServeRelayed serves "/v1/..." for another node; the origin is only
	// available through OriginFromContext.
	ServeRelayed() http.Handler
	ClearClusterData(ctx context.Context) error
	Audit(ctx context.Context, actor, action, resourceID string, status int, metadata map[string]any)
}

type Options struct {
	Settings SettingsStore
	Hub      HubStore
	Local    Local
	Version  string
	// HTTPClient carries member→hub calls. Nil builds one with 10s dial and
	// TLS handshake timeouts, no overall timeout and the environment proxy.
	HTTPClient *http.Client
	Logf       func(string, ...any)
	Now        func() time.Time
	// Defaults: 15s, 20s and 10s.
	PollInterval, PingInterval, PingTimeout time.Duration
	// MaxRelayBodyBytes caps bodies relayed through the hub; 0 is unlimited.
	MaxRelayBodyBytes func() int64
}

type CreateInput struct {
	Name, HubURL  string
	AllowInsecure bool
	Lending       *LendingPolicy
}

type JoinInput struct {
	Token, NodeName, Owner string
	AllowInsecure          bool
	Lending                *LendingPolicy
}

type originContextKey struct{}

type origin struct{ id, name string }

// WithOrigin marks ctx as serving a request relayed from another node.
func WithOrigin(ctx context.Context, nodeID, name string) context.Context {
	return context.WithValue(ctx, originContextKey{}, origin{id: nodeID, name: name})
}

func OriginFromContext(ctx context.Context) (nodeID, name string, ok bool) {
	value, ok := ctx.Value(originContextKey{}).(origin)
	if !ok || value.id == "" {
		return "", "", false
	}
	return value.id, value.name, true
}
