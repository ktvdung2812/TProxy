package cluster

import (
	"net/http"
	"strings"
	"time"
)

const revokeGrace = time.Second

// memberLinkHandler serves the hub's requests arriving over the link. Only
// the hub can reach it, so the origin headers it sets are trusted.
func (s *Service) memberLinkHandler(run *roleRun, linkDone <-chan struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest, ok := strings.CutPrefix(r.URL.EscapedPath(), LinkPathPrefix)
		if !ok {
			writeError(w, http.StatusNotFound, CodeNotFound, "not found")
			return
		}
		switch {
		case rest == "ping":
			w.WriteHeader(http.StatusNoContent)
		case rest == "inventory":
			if requireMethod(w, r, http.MethodGet) {
				writeJSON(w, http.StatusOK, s.buildInventory(r.Context()))
			}
		case rest == "notify":
			if requireMethod(w, r, http.MethodPost) {
				s.memberNotify(w, r, run, linkDone)
			}
		case strings.HasPrefix(rest, "serve/"):
			s.memberServe(w, r, strings.TrimPrefix(rest, "serve/"))
		default:
			writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		}
	})
}

func (s *Service) memberNotify(w http.ResponseWriter, r *http.Request, run *roleRun, linkDone <-chan struct{}) {
	var body struct {
		Event string `json:"event"`
	}
	if err := decodeBody(r.Body, 4<<10, &body); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid notification")
		return
	}
	switch body.Event {
	case "state", "catalog":
		run.poke()
	case "revoked":
		// Revocation closes the link this answer travels on. A dissolving
		// hub closes the link itself once it has read every answer, so wait
		// for that, with a short fallback for a hub that does not.
		s.goBackground(func() {
			timer := time.NewTimer(revokeGrace)
			defer timer.Stop()
			select {
			case <-linkDone:
			case <-run.ctx.Done():
			case <-timer.C:
			}
			s.handleRevoked(run, run.gen, http.StatusOK, "hub_notified")
		})
	default:
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "unknown notification event")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) memberServe(w http.ResponseWriter, r *http.Request, restEscaped string) {
	originID := strings.TrimSpace(r.Header.Get(HeaderOrigin))
	if originID == "" {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "missing cluster origin")
		return
	}
	s.serveLocal(w, r, originID, strings.TrimSpace(r.Header.Get(HeaderOriginName)), restEscaped)
}
