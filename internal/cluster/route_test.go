package cluster

import (
	"testing"

	"github.com/tproxy/tproxy/internal/config"
)

func TestClusterRouteTruthTable(t *testing.T) {
	price := &config.PricingConfig{InputPerMillion: 2, OutputPerMillion: 8}
	catalog := Catalog{Models: []CatalogModel{
		{ID: "gpt-test", Enabled: true, Aliases: []string{"gpt-alias"}, Pricing: price},
		{ID: "disabled-model", Enabled: false},
		{ID: "conflicted", Enabled: true},
		{ID: "free-model", Enabled: true},
	}}
	peer := func(change func(*NodeState)) NodeState {
		node := NodeState{ID: "peer", Role: RoleMember, Status: NodeActive, Online: true, LendingEnabled: true, Offers: []Offer{
			{ModelID: "gpt-test", EligibleAccounts: 2, Healthy: true},
			{ModelID: "disabled-model", EligibleAccounts: 1, Healthy: true},
			{ModelID: "conflicted", EligibleAccounts: 1, Healthy: true},
			{ModelID: "free-model", EligibleAccounts: 1, Healthy: true},
		}}
		if change != nil {
			change(&node)
		}
		return node
	}
	type setup struct {
		role      Role
		revoked   bool
		borrowing BorrowingPolicy
		peers     []NodeState
		offline   bool
	}
	for _, tc := range []struct {
		name   string
		change func(*setup)
		model  string
		mode   string // empty: no route
	}{
		{name: "member borrows an offered model", model: "gpt-test", mode: ModeFallback},
		{name: "alias resolves to its model", model: "gpt-alias", mode: ModeFallback},
		{name: "model without pricing", model: "free-model", mode: ModeFallback},
		{name: "hub borrows too", change: func(s *setup) { s.role = RoleHub }, model: "gpt-test", mode: ModeFallback},
		{name: "shared mode", change: func(s *setup) { s.borrowing.Mode = ModeShared }, model: "gpt-test", mode: ModeShared},
		{name: "standalone", change: func(s *setup) { s.role = RoleStandalone }, model: "gpt-test"},
		{name: "revoked member", change: func(s *setup) { s.revoked = true }, model: "gpt-test"},
		{name: "borrowing disabled", change: func(s *setup) { s.borrowing.Enabled = false }, model: "gpt-test"},
		{name: "model not in catalog", model: "other-model"},
		{name: "disabled catalog model", model: "disabled-model"},
		{name: "model that conflicted locally", model: "conflicted"},
		{name: "no peers", change: func(s *setup) { s.peers = nil }, model: "gpt-test"},
		{name: "only this node offers", change: func(s *setup) { s.peers = []NodeState{peer(func(n *NodeState) { n.ID = "self" })} }, model: "gpt-test"},
		{name: "peer offline", change: func(s *setup) { s.peers = []NodeState{peer(func(n *NodeState) { n.Online = false })} }, model: "gpt-test"},
		{name: "peer revoked", change: func(s *setup) { s.peers = []NodeState{peer(func(n *NodeState) { n.Status = NodeRevoked })} }, model: "gpt-test"},
		{name: "peer left", change: func(s *setup) { s.peers = []NodeState{peer(func(n *NodeState) { n.Status = NodeLeft })} }, model: "gpt-test"},
		{name: "peer not lending", change: func(s *setup) { s.peers = []NodeState{peer(func(n *NodeState) { n.LendingEnabled = false })} }, model: "gpt-test"},
		{name: "no eligible accounts", change: func(s *setup) {
			s.peers = []NodeState{peer(func(n *NodeState) { n.Offers = []Offer{{ModelID: "gpt-test"}} })}
		}, model: "gpt-test"},
		{name: "peer offers other models only", change: func(s *setup) {
			s.peers = []NodeState{peer(func(n *NodeState) { n.Offers = []Offer{{ModelID: "free-model", EligibleAccounts: 3}} })}
		}, model: "gpt-test"},
		{name: "hub unreachable", change: func(s *setup) { s.offline = true }, model: "gpt-test"},
		{name: "one usable peer is enough", change: func(s *setup) {
			s.peers = []NodeState{peer(func(n *NodeState) { n.ID, n.Online = "down", false }), peer(nil)}
		}, model: "gpt-test", mode: ModeFallback},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := setup{role: RoleMember, borrowing: BorrowingPolicy{Enabled: true, Mode: ModeFallback}, peers: []NodeState{peer(nil)}}
			if tc.change != nil {
				tc.change(&st)
			}
			s := New(Options{Settings: &memSettings{}, Local: newFakeLocal()})
			s.mu.Lock()
			s.identity = NodeIdentity{ID: "self", Name: "Self"}
			s.membership = Membership{Role: st.role, Revoked: st.revoked}
			s.borrowing = st.borrowing
			s.catalog = catalog
			s.report = ApplyReport{Conflicts: []CatalogConflict{{Kind: "model", ID: "conflicted", Reason: "exists locally"}}}
			s.peers = st.peers
			s.peersOffline = st.offline
			s.publishRouteLocked()
			s.mu.Unlock()

			mode, pricing, ok := s.ClusterRoute(tc.model)
			if ok != (tc.mode != "") || mode != tc.mode {
				t.Fatalf("ClusterRoute(%q) = %q, %v; want mode %q", tc.model, mode, ok, tc.mode)
			}
			if !ok {
				return
			}
			switch tc.model {
			case "gpt-test", "gpt-alias":
				if pricing == nil || *pricing != *price {
					t.Fatalf("pricing = %v", pricing)
				}
				if pricing == price {
					t.Fatal("pricing must be a copy")
				}
			default:
				if pricing != nil {
					t.Fatalf("pricing = %v, want nil", pricing)
				}
			}
		})
	}
}
