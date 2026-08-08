package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/tproxy/tproxy/internal/auth"
)

func (s *Server) adminCursorOAuthAutoImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	result := auth.AutoImportCursor()
	if !result.Found {
		errMsg := "could not auto-detect cursor tokens"
		if result.Err != nil {
			errMsg = result.Err.Error()
		}
		payload := map[string]any{
			"found":   false,
			"error":   errMsg,
			"db_path": result.Tokens.DBPath,
		}
		if result.WindowsManual {
			payload["windows_manual"] = true
		}
		writeJSON(w, http.StatusOK, payload)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"found":        true,
		"access_token": result.Tokens.AccessToken,
		"machine_id":   result.Tokens.MachineID,
		"db_path":      result.Tokens.DBPath,
	})
}

func (s *Server) adminCursorOAuthImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	var payload struct {
		ProviderID   string `json:"provider_id"`
		CredentialID string `json:"credential_id"`
		Label        string `json:"label"`
		AccessToken  string `json:"access_token"`
		MachineID    string `json:"machine_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	providerID := strings.TrimSpace(payload.ProviderID)
	if providerID == "" {
		providerID = "cursor"
	}
	accessToken := strings.TrimSpace(payload.AccessToken)
	machineID := strings.TrimSpace(payload.MachineID)
	if accessToken == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "access_token is required", useClientRequestID(r))
		return
	}
	if machineID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "machine_id is required", useClientRequestID(r))
		return
	}
	if err := auth.ValidateCursorImportToken(accessToken, machineID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	if _, err := s.store.Provider(r.Context(), providerID); err != nil {
		writeError(w, http.StatusBadRequest, "provider_not_found", "cursor provider must exist before importing a token", useClientRequestID(r))
		return
	}
	credentialID := strings.TrimSpace(payload.CredentialID)
	if credentialID == "" {
		credentialID = "cursor-" + uuid.NewString()[:8]
	}
	label := strings.TrimSpace(payload.Label)
	if label == "" {
		label = "Cursor IDE"
	}
	token := auth.BuildCursorOAuthToken(accessToken, machineID)
	if err := s.store.SaveOAuthCredential(r.Context(), providerID, credentialID, label, "", token); err != nil {
		writeError(w, http.StatusBadRequest, "credential_save_failed", err.Error(), useClientRequestID(r))
		return
	}
	_ = s.router.SyncProviderHealth(r.Context(), providerID)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"provider_id":   providerID,
		"credential_id": credentialID,
	})
}
