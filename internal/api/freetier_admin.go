package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/tproxy/tproxy/internal/ninerouter"
	"github.com/tproxy/tproxy/internal/router"
)

func (s *Server) adminFreeTiers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	catalog := ninerouter.FreeTierCatalog()
	sort.Slice(catalog, func(i, j int) bool {
		return strings.ToLower(catalog[i].Name) < strings.ToLower(catalog[j].Name)
	})
	providers, err := s.store.Providers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "providers_failed", err.Error(), useClientRequestID(r))
		return
	}
	configured := map[string]bool{}
	for _, provider := range providers {
		configured[provider.ID] = provider.Enabled
	}
	items := make([]map[string]any, 0, len(catalog))
	for _, entry := range catalog {
		items = append(items, map[string]any{
			"provider_id":  entry.ProviderID,
			"name":         entry.Name,
			"category":     entry.Category,
			"models":       entry.Models,
			"daily_limit":  entry.DailyLimit,
			"reset_window": entry.ResetWindow,
			"auth_type":    entry.AuthType,
			"api_key_url":  entry.ApiKeyURL,
			"has_oauth":    entry.HasOAuth,
			"notes":        entry.Notes,
			"configured":   configured[entry.ProviderID],
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":      items,
		"strategies": router.AllStrategies,
	})
}

func (s *Server) adminRoutingStrategies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"strategies": router.AllStrategies})
}
