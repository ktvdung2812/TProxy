package api

import (
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/thinking"
)

// applyClaudeTierReasoningEffort contributes the Claude tier alias's effort to a
// request whose client expressed no thinking preference of its own.
//
// It records the alias effort as a hint in Metadata instead of writing
// reasoning_effort into Raw: Raw is the client's pass-through body and stays
// untouched, and thinking.Normalize (running on the router) resolves the hint
// against every client source before anything reaches an adapter.
func (s *Server) applyClaudeTierReasoningEffort(request *canonical.Request) {
	if request == nil {
		return
	}
	clientModel := strings.TrimSpace(request.PublicModelID)
	if request.Metadata != nil {
		if stored, ok := request.Metadata[claudeClientModelMetadataKey].(string); ok {
			if trimmed := strings.TrimSpace(stored); trimmed != "" {
				clientModel = trimmed
			}
		}
	}
	effort := s.claudeAliasResolver().ReasoningEffortForClientModel(clientModel)
	if effort == "" || claudeRequestHasReasoningPreference(request.Raw) {
		return
	}
	thinking.SetAliasEffort(request, effort)
}

// claudeRequestHasReasoningPreference reports whether the client already chose a
// thinking level, in which case an alias must not override it.
func claudeRequestHasReasoningPreference(body map[string]any) bool {
	return thinking.FromRaw(body).Set
}
