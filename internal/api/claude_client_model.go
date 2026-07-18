package api

import (
	"strings"

	"github.com/tproxy/tproxy/internal/bridge"
	"github.com/tproxy/tproxy/internal/canonical"
)

const claudeClientModelMetadataKey = "client_model"

// preserveClaudeClientModel keeps the placeholder name Claude Code sent so responses
// can echo it back without changing client configuration.
func preserveClaudeClientModel(request *canonical.Request) {
	if request == nil {
		return
	}
	clientModel := strings.TrimSpace(request.PublicModelID)
	if clientModel == "" {
		return
	}
	if !bridge.IsClaudePlaceholder(clientModel) {
		return
	}
	if request.Metadata == nil {
		request.Metadata = map[string]any{}
	}
	request.Metadata[claudeClientModelMetadataKey] = clientModel
}

func clientFacingModel(request canonical.Request, resolvedModelID string) string {
	if request.Metadata != nil {
		if clientModel, ok := request.Metadata[claudeClientModelMetadataKey].(string); ok {
			clientModel = strings.TrimSpace(clientModel)
			if clientModel != "" {
				return clientModel
			}
		}
	}
	return resolvedModelID
}
