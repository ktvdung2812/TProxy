package providers

import (
	"context"
	"net/http"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

const copilotDefaultAPI = "https://api.githubcopilot.com"

type copilotAdapter struct {
	client *http.Client
	openAI *openAIAdapter
}

func (a *copilotAdapter) Execute(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (*canonical.Response, error) {
	return a.openAI.Execute(ctx, a.providerForRequest(provider, credential), credential, request)
}

func (a *copilotAdapter) ExecuteStream(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (<-chan canonical.Event, error) {
	return a.openAI.ExecuteStream(ctx, a.providerForRequest(provider, credential), credential, request)
}

func (a *copilotAdapter) providerForRequest(provider store.Provider, credential store.Credential) store.Provider {
	clone := provider
	baseURL := strings.TrimRight(copilotDefaultAPI, "/")
	if credential.Metadata != nil {
		if endpoint := strings.TrimSpace(stringValue(credential.Metadata["copilot_api_endpoint"])); endpoint != "" {
			baseURL = strings.TrimRight(endpoint, "/")
		}
	}
	if clone.BaseURL == "" {
		clone.BaseURL = baseURL
	} else {
		clone.BaseURL = baseURL
	}
	return clone
}
