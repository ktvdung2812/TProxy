package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/import9router"
	"github.com/tproxy/tproxy/internal/importcliproxy"
	"github.com/tproxy/tproxy/internal/importsub2api"
	"github.com/tproxy/tproxy/internal/ninerouter"
	"github.com/tproxy/tproxy/internal/store"
)

type clusterImportItems map[string][]string

func (items clusterImportItems) add(kind, id string) {
	if trimmed := strings.TrimSpace(id); trimmed != "" {
		items[kind] = append(items[kind], id)
		if trimmed != id {
			items[kind] = append(items[kind], trimmed)
		}
	}
}
func (items clusterImportItems) provider(id string) {
	if strings.TrimSpace(id) == store.ClusterProviderID {
		items.add("provider", id)
	}
}

func configImportItems(cfg *config.Config) clusterImportItems {
	items := clusterImportItems{}
	for _, p := range cfg.Providers {
		items.provider(p.ID)
		for _, c := range p.Credentials {
			items.add("credential", c.ID)
		}
	}
	for _, m := range cfg.Models {
		items.add("model", m.ID)
		items.add("alias", m.ID)
		for _, a := range m.Aliases {
			items.add("alias", a)
		}
	}
	for _, c := range cfg.Combos {
		items.add("combo", c.ID)
	}
	for _, k := range cfg.ClientAPIKeys {
		items.add("api_key", k.ID)
	}
	return items
}

var clusterImportSlug = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)
var clusterImportCredentialSlug = regexp.MustCompile(`[^a-z0-9]+`)

func clusterImport9Slug(s string) string {
	s = strings.Trim(strings.ToLower(strings.TrimSpace(s)), "-._")
	if s == "" {
		return "imported-item"
	}
	return clusterImportSlug.ReplaceAllString(s, "-")
}

func (s *Server) clusterImportItems(ctx context.Context, source string, data []byte) (clusterImportItems, error) {
	items := clusterImportItems{}
	switch source {
	case "9router":
		backup, err := import9router.ParseBackup(data)
		if err != nil {
			return items, nil
		}
		for _, c := range backup.ProviderConnections {
			items.provider(c.Provider)
			items.add("credential", c.ID)
		}
		for _, k := range backup.APIKeys {
			id := strings.TrimSpace(k.ID)
			if id == "" {
				id = clusterImport9Slug(k.Name)
			}
			items.add("api_key", id)
		}
		for _, c := range backup.Combos {
			items.add("combo", clusterImport9Slug(c.Name))
			for _, ref := range c.Models {
				ref = strings.TrimSpace(ref)
				parts := strings.SplitN(ref, "/", 2)
				provider, model := strings.TrimSpace(parts[0]), ref
				if len(parts) == 2 {
					model = strings.TrimSpace(parts[1])
				}
				items.provider(provider)
				if preset, ok := ninerouter.Lookup(provider); ok {
					provider = preset.ID
				} else if strings.HasPrefix(provider, "openai-compatible") {
					continue
				}
				provider = ninerouter.ResolveProviderID(provider)
				if _, ok := ninerouter.Lookup(provider); !ok || model == "" {
					continue
				}
				id := clusterImport9Slug(provider + "-" + model)
				items.add("model", id)
				items.add("alias", id)
			}
		}
	case "cliproxy":
		files, err := importcliproxy.ParseAuthFiles(data)
		if err != nil {
			return items, nil
		}
		for _, f := range files {
			items.provider(f.Type)
			id := strings.TrimSpace(f.AccountID)
			if id == "" {
				id = strings.TrimSpace(f.Email)
			}
			if id == "" {
				id = "imported"
			}
			id = strings.Trim(clusterImportCredentialSlug.ReplaceAllString(strings.ToLower("cliproxy-"+f.Type+"-"+id), "-"), "-")
			items.add("credential", id)
		}
	case "sub2api", "chatgpt":
		result, err := importsub2api.Import(ctx, s.store, data, importsub2api.Options{DryRun: true})
		if err != nil {
			return items, nil
		}
		if len(result.Errors) > 0 {
			return nil, fmt.Errorf("cannot check import: %s", strings.Join(result.Errors, "; "))
		}
		for _, p := range result.Accounts {
			items.provider(p.ProviderID)
			items.add("credential", p.CredentialID)
		}
	case "auth-bundle":
		var bundle struct {
			Providers []struct {
				ID string `json:"id"`
			} `json:"providers"`
			Credentials []struct {
				ID         string `json:"id"`
				ProviderID string `json:"provider_id"`
			} `json:"credentials"`
		}
		if json.Unmarshal(data, &bundle) != nil {
			return items, nil
		}
		for _, p := range bundle.Providers {
			items.provider(p.ID)
		}
		for _, c := range bundle.Credentials {
			items.provider(c.ProviderID)
			items.add("credential", c.ID)
		}
	}
	return items, nil
}

func (s *Server) guardClusterImport(w http.ResponseWriter, r *http.Request, items clusterImportItems) bool {
	conflicts := []string{}
	for kind, ids := range items {
		for _, id := range ids {
			managed := kind == "provider" && id == store.ClusterProviderID
			if !managed {
				var err error
				managed, err = s.store.IsClusterManaged(r.Context(), kind, id)
				if err != nil {
					writeError(w, 500, "store_error", err.Error(), useClientRequestID(r))
					return true
				}
			}
			if managed {
				if kind == "api_key" {
					conflicts = append(conflicts, fmt.Sprintf("client key ID %q", id))
				} else {
					conflicts = append(conflicts, fmt.Sprintf("%s:%s", kind, id))
				}
			}
		}
	}
	if len(conflicts) == 0 {
		return false
	}
	slices.Sort(conflicts)
	conflicts = slices.Compact(conflicts)
	writeError(w, 409, "cluster_managed", "cluster-managed import conflicts: "+strings.Join(conflicts, ", "), useClientRequestID(r))
	return true
}
