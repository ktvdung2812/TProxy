package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/tproxy/tproxy/internal/auth"
)

func (s *Server) adminKiroOAuthAutoImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	result := auth.AutoImportKiro()
	if !result.Found {
		errMsg := "could not auto-detect Kiro token"
		if result.Err != nil {
			errMsg = result.Err.Error()
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"found": false,
			"error": errMsg,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"found":         true,
		"refresh_token": result.RefreshToken,
		"client_id":     result.ClientID,
		"client_secret": result.ClientSecret,
		"region":        result.Region,
		"auth_method":   result.AuthMethod,
		"profile_arn":   result.ProfileArn,
		"source":        result.Source,
	})
}

func (s *Server) adminKiroOAuthImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	var payload struct {
		ProviderID   string `json:"provider_id"`
		CredentialID string `json:"credential_id"`
		Label        string `json:"label"`
		RefreshToken string `json:"refresh_token"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		Region       string `json:"region"`
		AuthMethod   string `json:"auth_method"`
		ProfileArn   string `json:"profile_arn"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	providerID := strings.TrimSpace(payload.ProviderID)
	if providerID == "" {
		providerID = "kiro"
	}
	if _, err := s.store.Provider(r.Context(), providerID); err != nil {
		writeError(w, http.StatusBadRequest, "provider_not_found", "kiro provider must exist before importing a token", useClientRequestID(r))
		return
	}
	token, email, err := s.auth.ImportKiroRefreshToken(r.Context(), auth.KiroImportInput{
		RefreshToken: payload.RefreshToken,
		ClientID:     payload.ClientID,
		ClientSecret: payload.ClientSecret,
		Region:       payload.Region,
		AuthMethod:   payload.AuthMethod,
		ProfileArn:   payload.ProfileArn,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, auth.Code(err), err.Error(), useClientRequestID(r))
		return
	}
	credentialID := strings.TrimSpace(payload.CredentialID)
	if credentialID == "" {
		credentialID = "kiro-" + uuid.NewString()[:8]
	}
	label := strings.TrimSpace(payload.Label)
	if label == "" {
		label = "Kiro"
	}
	if err := s.store.SaveOAuthCredential(r.Context(), providerID, credentialID, label, email, token); err != nil {
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

func (s *Server) adminKiroOAuthImportCLIProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	var payload struct {
		ProviderID   string `json:"provider_id"`
		CredentialID string `json:"credential_id"`
		Label        string `json:"label"`
		JSON         string `json:"json"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	if strings.TrimSpace(payload.JSON) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "json is required", useClientRequestID(r))
		return
	}
	providerID := strings.TrimSpace(payload.ProviderID)
	if providerID == "" {
		providerID = "kiro"
	}
	if _, err := s.store.Provider(r.Context(), providerID); err != nil {
		writeError(w, http.StatusBadRequest, "provider_not_found", "kiro provider must exist before importing a token", useClientRequestID(r))
		return
	}
	token, email, err := auth.ImportKiroExternalIdpJSON(payload.JSON)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	credentialID := strings.TrimSpace(payload.CredentialID)
	if credentialID == "" {
		credentialID = "kiro-" + uuid.NewString()[:8]
	}
	label := strings.TrimSpace(payload.Label)
	if label == "" {
		label = "Kiro CLIProxyAPI"
	}
	if err := s.store.SaveOAuthCredential(r.Context(), providerID, credentialID, label, email, token); err != nil {
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

func (s *Server) adminKiroOAuthAPIKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	var payload struct {
		ProviderID   string `json:"provider_id"`
		CredentialID string `json:"credential_id"`
		Label        string `json:"label"`
		APIKey       string `json:"api_key"`
		Region       string `json:"region"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	providerID := strings.TrimSpace(payload.ProviderID)
	if providerID == "" {
		providerID = "kiro"
	}
	if _, err := s.store.Provider(r.Context(), providerID); err != nil {
		writeError(w, http.StatusBadRequest, "provider_not_found", "kiro provider must exist before importing a token", useClientRequestID(r))
		return
	}
	credentialCfg, err := s.auth.ImportKiroAPIKey(r.Context(), payload.APIKey, payload.Region)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "API key validation failed", useClientRequestID(r))
		return
	}
	credentialID := strings.TrimSpace(payload.CredentialID)
	if credentialID == "" {
		credentialID = "kiro-" + uuid.NewString()[:8]
	}
	credentialCfg.ID = credentialID
	if label := strings.TrimSpace(payload.Label); label != "" {
		credentialCfg.Label = label
	} else {
		credentialCfg.Label = "Kiro API Key"
	}
	if err := s.store.SaveCredential(r.Context(), providerID, credentialCfg); err != nil {
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
