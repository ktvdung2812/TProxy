package api

import (
	"net/http"

	"github.com/tproxy/tproxy/internal/version"
)

func (s *Server) adminVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, version.Check(r.Context(), true))
}
