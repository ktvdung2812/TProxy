package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/tproxy/tproxy/internal/ninerouter"
)

func (s *Server) adminNinerouterPresets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	type presetView struct {
		ID             string   `json:"id"`
		Type           string   `json:"type"`
		Name           string   `json:"name"`
		BaseURL        string   `json:"base_url,omitempty"`
		AuthType       string   `json:"auth_type"`
		Category       string   `json:"category"`
		CredentialAuth string   `json:"credential_auth,omitempty"`
		AuthHint       string   `json:"auth_hint,omitempty"`
		ApiKeyURL      string   `json:"api_key_url,omitempty"`
		AuthModes      []string `json:"auth_modes,omitempty"`
		HasOAuth       bool     `json:"has_oauth"`
		SupportsQuota  bool     `json:"supports_quota"`
		NoAuth         bool     `json:"no_auth"`
	}
	items := make([]presetView, 0, len(ninerouter.Presets))
	for _, preset := range ninerouter.Presets {
		items = append(items, presetView{
			ID:             preset.ID,
			Type:           preset.Type,
			Name:           preset.Name,
			BaseURL:        preset.BaseURL,
			AuthType:       preset.AuthType,
			Category:       preset.Category,
			CredentialAuth: preset.CredentialAuth,
			AuthHint:       preset.AuthHint,
			ApiKeyURL:      preset.ApiKeyURL,
			AuthModes:      preset.AuthModes,
			HasOAuth:       preset.HasOAuth,
			SupportsQuota:  preset.SupportsQuota,
			NoAuth:         preset.NoAuth,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	writeJSON(w, http.StatusOK, map[string]any{"presets": items, "aliases": ninerouter.Aliases})
}
