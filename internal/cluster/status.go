package cluster

import (
	"context"
	"strings"
)

// StatusView is served as-is by GET /api/admin/cluster. Cluster and Catalog
// are null on a standalone node, Link is set only on members, and Shared only
// on the hub. SuggestedHubURLs is left for the API layer to fill.
type StatusView struct {
	Role             Role             `json:"role"`
	Node             StatusNode       `json:"node"`
	Cluster          *StatusCluster   `json:"cluster"`
	Link             *StatusLink      `json:"link"`
	Lending          LendingPolicy    `json:"lending"`
	Borrowing        BorrowingPolicy  `json:"borrowing"`
	Nodes            []StatusNodeView `json:"nodes"`
	Catalog          *StatusCatalog   `json:"catalog"`
	Shared           *SharedSelection `json:"shared,omitempty"`
	SuggestedHubURLs []string         `json:"suggested_hub_urls,omitempty"`
	Warnings         []string         `json:"warnings"`
}

type StatusNode struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type StatusCluster struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	HubURL        string `json:"hub_url"`
	HubNodeID     string `json:"hub_node_id"`
	JoinedAt      string `json:"joined_at"`
	AllowInsecure bool   `json:"allow_insecure"`
}

type StatusLink struct {
	State       string `json:"state"`
	ConnectedAt string `json:"connected_at"`
	LastError   string `json:"last_error"`
	RTTMS       int64  `json:"rtt_ms"`
}

type StatusNodeView struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Owner          string  `json:"owner"`
	Role           Role    `json:"role"`
	Status         string  `json:"status"`
	Online         bool    `json:"online"`
	Self           bool    `json:"self"`
	Version        string  `json:"version"`
	LastSeenAt     string  `json:"last_seen_at"`
	RTTMS          int64   `json:"rtt_ms"`
	LendingEnabled bool    `json:"lending_enabled"`
	Offers         []Offer `json:"offers"`
}

type StatusCatalog struct {
	Version   string               `json:"version"`
	Models    []StatusCatalogEntry `json:"models"`
	Combos    []StatusCatalogEntry `json:"combos"`
	APIKeys   []StatusCatalogKey   `json:"api_keys"`
	Conflicts []CatalogConflict    `json:"conflicts"`
}

type StatusCatalogEntry struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type StatusCatalogKey struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Service) Status(ctx context.Context) (StatusView, error) {
	if err := s.ensureLoaded(ctx); err != nil {
		return StatusView{}, err
	}
	s.mu.Lock()
	m, self := s.membership, s.identity
	lending, borrowing, shared := normalizeLending(s.lending), s.borrowing, normalizeShared(s.shared)
	catalog, conflicts := s.catalog, s.report.Conflicts
	state, peersOffline, failures, link := s.state, s.peersOffline, s.failures, s.link
	s.mu.Unlock()
	view := StatusView{
		Role:      m.Role,
		Node:      StatusNode{ID: self.ID, Name: self.Name, Version: s.version},
		Lending:   lending,
		Borrowing: borrowing,
		Nodes:     []StatusNodeView{},
		Warnings:  []string{},
	}
	if m.Role == RoleStandalone {
		return view, nil
	}
	view.Cluster = &StatusCluster{
		ID: m.ClusterID, Name: m.ClusterName, HubURL: m.HubURL, HubNodeID: m.HubNodeID,
		JoinedAt: formatTime(m.JoinedAt), AllowInsecure: m.AllowInsecure,
	}
	if strings.HasPrefix(m.HubURL, "http://") {
		view.Warnings = append(view.Warnings, WarningInsecureHubURL)
	}
	var nodes []NodeState
	if m.Role == RoleHub {
		hubState, err := s.hubState(ctx)
		if err != nil {
			return StatusView{}, err
		}
		nodes = hubState.Nodes
		s.mu.Lock()
		catalog = s.catalog
		s.mu.Unlock()
		conflicts = nil
		view.Shared = &shared
	} else {
		nodes = state.Nodes
		view.Link = &StatusLink{State: link.State, ConnectedAt: formatTime(link.ConnectedAt), LastError: link.LastError, RTTMS: link.RTTMS}
		if failures >= unreachableAfter {
			view.Warnings = append(view.Warnings, WarningHubUnreachable)
		}
	}
	for _, node := range nodes {
		isSelf := node.ID == self.ID
		online := node.Online && (isSelf || !peersOffline)
		view.Nodes = append(view.Nodes, StatusNodeView{
			ID: node.ID, Name: node.Name, Owner: node.Owner, Role: node.Role, Status: node.Status, Online: online,
			Self: isSelf, Version: node.Version, LastSeenAt: formatTime(node.LastSeenAt), RTTMS: node.RTTMS,
			LendingEnabled: node.LendingEnabled, Offers: cloneOffers(node.Offers),
		})
	}
	view.Catalog = catalogStatus(catalog, conflicts)
	return view, nil
}

func catalogStatus(catalog Catalog, conflicts []CatalogConflict) *StatusCatalog {
	view := &StatusCatalog{
		Version:   catalog.Version,
		Models:    make([]StatusCatalogEntry, 0, len(catalog.Models)),
		Combos:    make([]StatusCatalogEntry, 0, len(catalog.Combos)),
		APIKeys:   make([]StatusCatalogKey, 0, len(catalog.APIKeys)),
		Conflicts: append([]CatalogConflict{}, conflicts...),
	}
	for _, model := range catalog.Models {
		view.Models = append(view.Models, StatusCatalogEntry{ID: model.ID, DisplayName: model.DisplayName})
	}
	for _, combo := range catalog.Combos {
		view.Combos = append(view.Combos, StatusCatalogEntry{ID: combo.ID, DisplayName: combo.DisplayName})
	}
	for _, key := range catalog.APIKeys {
		view.APIKeys = append(view.APIKeys, StatusCatalogKey{ID: key.ID, Name: key.Name})
	}
	return view
}
