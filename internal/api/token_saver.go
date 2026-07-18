package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/rtk"
	"github.com/tproxy/tproxy/internal/store"
	"github.com/tproxy/tproxy/internal/tokenizer"
)

func (s *Server) adminTokenSaverSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := s.store.TokenSaverSettings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "token_saver_settings_failed", err.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"rtk_enabled":          settings.RTKEnabled,
			"per_request_opt_out":  settings.PerRequestOptOut,
			"cli_hook_recommended": settings.CLIHookRecommended,
			"upstream_project":     "https://github.com/rtk-ai/rtk",
		})
	case http.MethodPut, http.MethodPatch:
		var payload struct {
			RTKEnabled         *bool `json:"rtk_enabled"`
			CLIHookRecommended *bool `json:"cli_hook_recommended"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error(), useClientRequestID(r))
			return
		}
		settings, err := s.store.TokenSaverSettings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "token_saver_settings_failed", err.Error(), useClientRequestID(r))
			return
		}
		if payload.RTKEnabled != nil {
			settings.RTKEnabled = *payload.RTKEnabled
		}
		if payload.CLIHookRecommended != nil {
			settings.CLIHookRecommended = *payload.CLIHookRecommended
		}
		if err := s.store.SaveTokenSaverSettings(r.Context(), settings); err != nil {
			writeError(w, http.StatusInternalServerError, "token_saver_settings_failed", err.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "rtk_enabled": settings.RTKEnabled})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET/PUT/PATCH required", useClientRequestID(r))
	}
}

func (s *Server) applyTokenSaver(request *canonical.Request, w http.ResponseWriter, r *http.Request) {
	if request == nil {
		return
	}
	settings, err := s.store.TokenSaverSettings(r.Context())
	if err != nil {
		settings = store.DefaultTokenSaverSettings()
	}
	if settings.PerRequestOptOut && strings.EqualFold(strings.TrimSpace(r.Header.Get("X-TProxy-Token-Saver")), "off") {
		return
	}
	var tokensSaved int
	if settings.RTKEnabled {
		tokensSaved = rtk.CompressRequest(request).TokensSaved
	} else {
		tokensSaved = tokenizer.Compress(request).TokensSaved
	}
	if tokensSaved <= 0 {
		return
	}
	if request.Metadata == nil {
		request.Metadata = map[string]any{}
	}
	request.Metadata["tokens_saved"] = tokensSaved
	if w != nil {
		w.Header().Set("X-TProxy-Tokens-Saved", fmt.Sprint(tokensSaved))
	}
}
