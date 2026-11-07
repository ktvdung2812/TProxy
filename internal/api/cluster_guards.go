package api

import (
	"net/http"
	"reflect"
	"slices"
	"strings"

	"github.com/tproxy/tproxy/internal/config"
)

func (s *Server) clusterManaged(w http.ResponseWriter, r *http.Request, kind, id string) bool {
	managed, err := s.store.IsClusterManaged(r.Context(), kind, strings.TrimSpace(id))
	if err != nil {
		writeError(w, 500, "store_error", err.Error(), useClientRequestID(r))
		return true
	}
	if managed {
		s.clusterManagedError(w, r)
		return true
	}
	return false
}
func (s *Server) clusterManagedError(w http.ResponseWriter, r *http.Request) {
	writeError(w, 409, "cluster_managed", "managed by the cluster hub", useClientRequestID(r))
}
func sameStringSet(a, b []string) bool {
	a = slices.Clone(a)
	b = slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	a = slices.Compact(a)
	b = slices.Compact(b)
	return slices.Equal(a, b)
}
func (s *Server) guardClusterModel(w http.ResponseWriter, r *http.Request, model *config.PublicModelConfig, policy map[string]any) bool {
	managed, err := s.store.IsClusterManaged(r.Context(), "model", model.ID)
	if err != nil {
		writeError(w, 500, "store_error", err.Error(), useClientRequestID(r))
		return true
	}
	if !managed {
		return false
	}
	current, err := s.store.PublicModel(r.Context(), model.ID)
	if err != nil {
		writeError(w, 500, "store_error", err.Error(), useClientRequestID(r))
		return true
	}
	if model.DisplayName != current.DisplayName || model.Enabled != current.Enabled || model.ExposeUpstreamName != current.ExposeUpstreamName || model.RewriteResponseModel != current.RewriteResponseModel || !sameStringSet(model.Aliases, current.Aliases) || !sameStringSet(model.Capabilities, current.Capabilities) || !sameClusterMap(model.Limits, current.Limits) || !sameClusterMap(policy, current.Policy) {
		s.clusterManagedError(w, r)
		return true
	}
	model.Aliases = current.Aliases
	return false
}

func sameClusterMap(a, b map[string]any) bool {
	return len(a) == 0 && len(b) == 0 || reflect.DeepEqual(a, b)
}
