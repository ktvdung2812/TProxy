package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func (s *Server) adminLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	limit := queryLimit(r, 100)
	items := s.liveLogs.Recent(limit)
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "live": true, "persisted": false})
}

func (s *Server) adminLogsStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "stream_unsupported", "streaming is not supported", useClientRequestID(r))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	limit := queryLimit(r, 100)
	notify, unsubscribe := s.liveLogs.Subscribe()
	defer unsubscribe()

	send := func() bool {
		payload := map[string]any{
			"data":      s.liveLogs.Recent(limit),
			"live":      true,
			"persisted": false,
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", encoded); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if !send() {
		return
	}

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-notify:
			if !send() {
				return
			}
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
