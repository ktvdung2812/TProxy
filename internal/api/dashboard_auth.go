package api

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/security"
)

func (s *Server) loadManagementSecret(ctx context.Context) {
	// Always bootstrap the local dashboard verifier, even when an environment
	// secret overrides it for remote management. This keeps a fresh install
	// deterministic without ever logging or returning the default password.
	_, generated, err := s.store.EnsureDashboardPassword(ctx)
	passwordAuth := err == nil
	if err != nil {
		log.Printf("warning: dashboard password unavailable: %v", err)
	} else if generated {
		log.Printf("tproxy dashboard password initialized; change it from Settings before enabling remote access")
	}
	env := config.Env(s.currentConfig().Security.ManagementSecretEnv)

	s.managementMu.Lock()
	defer s.managementMu.Unlock()
	s.dashboardPasswordAuth = passwordAuth
	s.managementSecret = env
	s.managementSecretFromEnv = env != ""
}

// managementCredentials snapshots the credential state under one read lock so
// the auth decision cannot observe a half-applied reload.
func (s *Server) managementCredentials() (secret string, fromEnv bool, passwordAuth bool) {
	s.managementMu.RLock()
	defer s.managementMu.RUnlock()
	return s.managementSecret, s.managementSecretFromEnv, s.dashboardPasswordAuth
}

func (s *Server) setRemoteManagement(allowed bool) {
	s.managementMu.Lock()
	defer s.managementMu.Unlock()
	s.allowRemoteMgmt = allowed
}

func (s *Server) setLanManagement(allowed bool) {
	s.managementMu.Lock()
	defer s.managementMu.Unlock()
	s.allowLanMgmt = allowed
}

func (s *Server) managementScopes() (remote bool, lan bool) {
	s.managementMu.RLock()
	defer s.managementMu.RUnlock()
	return s.allowRemoteMgmt, s.allowLanMgmt
}

func (s *Server) adminDashboardPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPatch {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "PUT or PATCH required", useClientRequestID(r))
		return
	}

	var payload struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error(), useClientRequestID(r))
		return
	}

	current := strings.TrimSpace(payload.CurrentPassword)
	next := strings.TrimSpace(payload.NewPassword)
	if current == "" || next == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "current_password and new_password are required", useClientRequestID(r))
		return
	}
	if len(next) < 6 {
		writeError(w, http.StatusBadRequest, "invalid_request", "new_password must be at least 6 characters", useClientRequestID(r))
		return
	}
	secret, fromEnv, passwordAuth := s.managementCredentials()
	if fromEnv {
		writeError(w, http.StatusConflict, "management_secret_env_configured", "management password is configured by environment", useClientRequestID(r))
		return
	}
	matched := secret != "" && security.ConstantTimeEqual(current, secret)
	if !matched && passwordAuth && s.isLocalManagementRequest(r) {
		matched, _ = s.store.VerifyDashboardPassword(r.Context(), current)
	}
	if !matched {
		writeError(w, http.StatusUnauthorized, "invalid_management_secret", "current password is incorrect", useClientRequestID(r))
		return
	}
	if security.ConstantTimeEqual(current, next) {
		writeError(w, http.StatusBadRequest, "invalid_request", "new password must differ from current password", useClientRequestID(r))
		return
	}

	if err := s.store.SaveDashboardPassword(r.Context(), next); err != nil {
		writeError(w, http.StatusInternalServerError, "dashboard_password_save_failed", err.Error(), useClientRequestID(r))
		return
	}
	s.managementMu.Lock()
	if !s.managementSecretFromEnv {
		s.managementSecret = next
	}
	s.dashboardPasswordAuth = true
	s.managementMu.Unlock()
	// The stored verifier changed, so any cached decision for the old password
	// must not keep granting access.
	s.dashboardPasswordCache.reset()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
