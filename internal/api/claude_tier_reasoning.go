package api

import (
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
)

func (s *Server) applyClaudeTierReasoningEffort(request *canonical.Request) {
	if request == nil || request.Raw == nil {
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
	request.Raw["reasoning_effort"] = effort
}

func claudeRequestHasReasoningPreference(body map[string]any) bool {
	if _, ok := body["reasoning_effort"]; ok {
		return true
	}
	if reasoning, ok := body["reasoning"].(map[string]any); ok {
		if effort, ok := reasoning["effort"].(string); ok && strings.TrimSpace(effort) != "" {
			return true
		}
	}
	if thinking, ok := body["thinking"].(map[string]any); ok {
		switch strings.TrimSpace(strings.ToLower(stringValue(thinking["type"]))) {
		case "enabled", "adaptive":
			return true
		}
		if budget, ok := thinking["budget_tokens"]; ok && intValue(budget) > 0 {
			return true
		}
	}
	return false
}
