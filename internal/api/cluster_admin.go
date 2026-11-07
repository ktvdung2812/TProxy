package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/cluster"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/netutil"
)

func (s *Server) clusterStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.cluster.Status(r.Context())
	if err != nil {
		s.clusterError(w, r, err)
		return
	}
	gateway, err := s.store.GatewaySettings(r.Context())
	if err != nil {
		s.clusterError(w, r, err)
		return
	}
	tunnel, err := s.store.TunnelSettings(r.Context())
	if err != nil {
		s.clusterError(w, r, err)
		return
	}
	if status.Role == cluster.RoleStandalone {
		seen := map[string]bool{}
		add := func(u string) {
			u = strings.TrimSpace(u)
			if u != "" && !seen[u] {
				seen[u] = true
				status.SuggestedHubURLs = append(status.SuggestedHubURLs, u)
			}
		}
		add(gateway.PublicBaseURL)
		add(tunnel.TunnelURL)
		add(tunnel.TailscaleURL)
		for _, ip := range netutil.LANIPv4Addresses() {
			add("http://" + net.JoinHostPort(ip, strconv.Itoa(s.currentConfig().Server.Port)))
		}
		add("http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(s.currentConfig().Server.Port)))
	}
	host := s.currentConfig().Server.Host
	if (status.Role == cluster.RoleStandalone || status.Role == cluster.RoleHub) && (host == "127.0.0.1" || host == "::1" || host == "localhost") && gateway.PublicBaseURL == "" && tunnel.TunnelURL == "" && tunnel.TailscaleURL == "" {
		status.Warnings = append(status.Warnings, "hub_listens_on_loopback")
	}
	env := s.currentConfig().Security.MasterKeyEnv
	if env == "" {
		env = "TPROXY_MASTER_KEY"
	}
	if config.Env(env) == "" {
		status.Warnings = append(status.Warnings, "master_key_missing")
	}
	writeJSON(w, 200, status)
}
func (s *Server) clusterError(w http.ResponseWriter, r *http.Request, err error) {
	var ce *cluster.Error
	if errors.As(err, &ce) {
		writeError(w, ce.Status, ce.Code, ce.Message, useClientRequestID(r))
		return
	}
	writeError(w, 500, "internal_error", err.Error(), useClientRequestID(r))
}
func (s *Server) adminCluster(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/admin/cluster")
	ctx := r.Context()
	method := func(want string) bool {
		if r.Method != want {
			writeError(w, 405, "method_not_allowed", want+" required", useClientRequestID(r))
			return false
		}
		return true
	}
	decode := func(v any) bool {
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v); err != nil {
			writeError(w, 400, "invalid_request", err.Error(), useClientRequestID(r))
			return false
		}
		return true
	}
	var err error
	switch {
	case path == "":
		if method(http.MethodGet) {
			s.clusterStatus(w, r)
		}
		return
	case path == "/create":
		if !method("POST") {
			return
		}
		var v struct {
			Name          string                 `json:"name"`
			HubURL        string                 `json:"hub_url"`
			AllowInsecure bool                   `json:"allow_insecure"`
			Lending       *cluster.LendingPolicy `json:"lending"`
		}
		if !decode(&v) {
			return
		}
		err = s.cluster.CreateCluster(ctx, cluster.CreateInput{Name: v.Name, HubURL: v.HubURL, AllowInsecure: v.AllowInsecure, Lending: v.Lending})
	case path == "/join":
		if !method("POST") {
			return
		}
		var v struct {
			Token         string                 `json:"token"`
			NodeName      string                 `json:"node_name"`
			Owner         string                 `json:"owner"`
			AllowInsecure bool                   `json:"allow_insecure"`
			Lending       *cluster.LendingPolicy `json:"lending"`
		}
		if !decode(&v) {
			return
		}
		err = s.cluster.Join(ctx, cluster.JoinInput{Token: v.Token, NodeName: v.NodeName, Owner: v.Owner, AllowInsecure: v.AllowInsecure, Lending: v.Lending})
	case path == "/leave":
		if !method("POST") {
			return
		}
		err = s.cluster.Leave(ctx)
	case path == "/dissolve":
		if !method("POST") {
			return
		}
		err = s.cluster.Dissolve(ctx)
	case path == "/sync":
		if !method("POST") {
			return
		}
		err = s.cluster.Sync(ctx)
	case path == "/node":
		if !method("PUT") {
			return
		}
		var v struct {
			Name string `json:"name"`
		}
		if !decode(&v) {
			return
		}
		err = s.cluster.RenameLocalNode(ctx, v.Name)
	case path == "/lending":
		if !method("PUT") {
			return
		}
		var v cluster.LendingPolicy
		if !decode(&v) {
			return
		}
		err = s.cluster.SetLending(ctx, v)
	case path == "/borrowing":
		if !method("PUT") {
			return
		}
		var v cluster.BorrowingPolicy
		if !decode(&v) {
			return
		}
		err = s.cluster.SetBorrowing(ctx, v)
	case path == "/shared":
		if !method("PUT") {
			return
		}
		var v cluster.SharedSelection
		if !decode(&v) {
			return
		}
		err = s.cluster.SetShared(ctx, v)
	case path == "/tokens":
		switch r.Method {
		case "GET":
			var tokens []cluster.JoinTokenRecord
			tokens, err = s.cluster.JoinTokens(ctx)
			if err == nil {
				if tokens == nil {
					tokens = []cluster.JoinTokenRecord{}
				}
				writeJSON(w, 200, map[string]any{"tokens": tokens})
				return
			}
		case "POST":
			var v struct {
				Label    string `json:"label"`
				TTLHours int    `json:"ttl_hours"`
				MaxUses  int    `json:"max_uses"`
			}
			if !decode(&v) {
				return
			}
			var token string
			var rec cluster.JoinTokenRecord
			token, rec, err = s.cluster.CreateJoinToken(ctx, v.Label, time.Duration(v.TTLHours)*time.Hour, v.MaxUses)
			if err == nil {
				writeJSON(w, 200, map[string]any{"token": token, "id": rec.ID, "expires_at": rec.ExpiresAt, "command": "tproxy cluster join " + token})
				return
			}
		default:
			writeError(w, 405, "method_not_allowed", "GET or POST required", useClientRequestID(r))
			return
		}
	case strings.HasPrefix(path, "/tokens/"):
		if !method("DELETE") {
			return
		}
		err = s.cluster.RevokeJoinToken(ctx, strings.TrimPrefix(path, "/tokens/"))
		if err == nil {
			writeJSON(w, 200, map[string]any{"ok": true})
			return
		}
	case path == "/ledger":
		if !method("GET") {
			return
		}
		days := 0
		if raw := r.URL.Query().Get("days"); raw != "" {
			days, err = strconv.Atoi(raw)
			if err != nil {
				writeError(w, 400, "invalid_request", "invalid days", useClientRequestID(r))
				return
			}
		}
		var rows []cluster.LedgerView
		rows, err = s.cluster.Ledger(ctx, days)
		if err == nil {
			if rows == nil {
				rows = []cluster.LedgerView{}
			}
			writeJSON(w, 200, map[string]any{"rows": rows})
			return
		}
	case strings.HasPrefix(path, "/nodes/"):
		id := strings.TrimPrefix(path, "/nodes/")
		switch r.Method {
		case "PATCH":
			var v struct {
				Name  *string `json:"name"`
				Owner *string `json:"owner"`
			}
			if !decode(&v) {
				return
			}
			err = s.cluster.UpdateNode(ctx, id, v.Name, v.Owner)
		case "DELETE":
			err = s.cluster.RevokeNode(ctx, id)
		default:
			writeError(w, 405, "method_not_allowed", "PATCH or DELETE required", useClientRequestID(r))
			return
		}
	default:
		writeError(w, 404, "not_found", fmt.Sprintf("unknown cluster endpoint %s", path), useClientRequestID(r))
		return
	}
	if err != nil {
		s.clusterError(w, r, err)
		return
	}
	s.clusterStatus(w, r)
}
