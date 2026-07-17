package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

type tavilyAdapter struct{ client *http.Client }

func (a *tavilyAdapter) Execute(context.Context, store.Provider, store.Credential, canonical.Request) (*canonical.Response, error) {
	return nil, &ProviderError{Status: http.StatusBadRequest, Code: "capability_not_supported", Message: "Tavily supports /v1/search only"}
}

func (a *tavilyAdapter) ExecuteStream(context.Context, store.Provider, store.Credential, canonical.Request) (<-chan canonical.Event, error) {
	return nil, &ProviderError{Status: http.StatusBadRequest, Code: "capability_not_supported", Message: "Tavily does not support gateway streaming"}
}

func (a *tavilyAdapter) Proxy(ctx context.Context, provider store.Provider, credential store.Credential, rawRequest RawRequest) (*RawResponse, error) {
	ctx = withCredentialProxy(ctx, credential)
	if rawRequest.Path != "/v1/search" {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "capability_not_supported", Message: "Tavily supports /v1/search only"}
	}
	var input map[string]any
	if err := json.Unmarshal(rawRequest.Body, &input); err != nil {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "search request must be JSON", Err: err}
	}
	query := strings.TrimSpace(stringValue(firstValue(input, "query", "q")))
	if query == "" {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "search query is required"}
	}
	upstreamModel := stringValue(input["model"])
	delete(input, "model")
	delete(input, "q")
	input["query"] = query
	if value := firstAny(input["max_results"], input["limit"]); value != nil {
		input["max_results"] = value
		delete(input, "limit")
	}
	if _, exists := input["search_depth"]; !exists {
		input["search_depth"] = "advanced"
	}
	if _, exists := input["include_answer"]; !exists {
		input["include_answer"] = true
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Err: err}
	}
	target := strings.TrimRight(provider.BaseURL, "/")
	if !strings.HasSuffix(strings.ToLower(target), "/search") {
		target += "/search"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, &ProviderError{Code: "upstream_network", Err: err}
	}
	request.Header = authHeaders(provider, credential)
	for _, name := range []string{"X-Request-ID", "Idempotency-Key"} {
		if value := rawRequest.Headers.Get(name); value != "" {
			request.Header.Set(name, value)
		}
	}
	response, err := a.client.Do(request)
	if err != nil {
		return nil, &ProviderError{Code: "upstream_network", Err: err}
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if readErr != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_read_error", Err: readErr}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &RawResponse{Status: response.StatusCode, Headers: response.Header.Clone(), Body: data, ContentType: response.Header.Get("Content-Type")}, upstreamResponseError(response, data)
	}
	var output map[string]any
	if err = json.Unmarshal(data, &output); err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "invalid_upstream_response", Err: err}
	}
	output["object"] = "search.results"
	output["model"] = upstreamModel
	if output["id"] == nil {
		output["id"] = firstAny(output["request_id"], rawRequest.Headers.Get("X-Request-ID"))
	}
	normalized, err := json.Marshal(output)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "invalid_upstream_response", Err: err}
	}
	return &RawResponse{Status: response.StatusCode, Headers: response.Header.Clone(), Body: normalized, ContentType: "application/json"}, nil
}

var _ Adapter = (*tavilyAdapter)(nil)
var _ RawProxyAdapter = (*tavilyAdapter)(nil)
