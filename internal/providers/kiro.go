package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

var kiroFallbackURLs = []string{
	"https://runtime.us-east-1.kiro.dev/generateAssistantResponse",
	"https://codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse",
	"https://q.us-east-1.amazonaws.com/generateAssistantResponse",
}

type kiroAdapter struct{ client *http.Client }

func (a *kiroAdapter) Execute(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (*canonical.Response, error) {
	ctx = withCredentialProxy(ctx, credential)
	response, err := a.postKiro(ctx, provider, credential, request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, upstreamError(response)
	}
	return collectKiroResponse(ctx, response, request.UpstreamModel)
}

func (a *kiroAdapter) ExecuteStream(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (<-chan canonical.Event, error) {
	ctx = withCredentialProxy(ctx, credential)
	response, err := a.postKiro(ctx, provider, credential, request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		return nil, upstreamError(response)
	}
	out := make(chan canonical.Event, 32)
	go func() {
		defer close(out)
		streamKiroEventStream(ctx, response, request.UpstreamModel, out)
	}()
	return out, nil
}

func (a *kiroAdapter) postKiro(ctx context.Context, provider store.Provider, credential store.Credential, request canonical.Request) (*http.Response, error) {
	body, err := buildKiroPayload(request, credential)
	if err != nil {
		return nil, err
	}
	headers := kiroHeaders(credential)
	var lastErr error
	for _, target := range kiroOrderedURLs(provider, credential) {
		response, postErr := executeJSON(ctx, a.client, http.MethodPost, target, headers, body)
		if postErr != nil {
			lastErr = postErr
			continue
		}
		if response.StatusCode == http.StatusTooManyRequests {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			lastErr = upstreamError(response)
			continue
		}
		if response.StatusCode >= 500 {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			lastErr = upstreamError(response)
			continue
		}
		return response, nil
	}
	if lastErr != nil {
		return nil, &ProviderError{Code: "upstream_network", Err: lastErr}
	}
	return nil, &ProviderError{Code: "upstream_unavailable", Message: "all Kiro endpoints failed"}
}

func kiroOrderedURLs(provider store.Provider, credential store.Credential) []string {
	primary := kiroEndpoint(provider.BaseURL)
	authMethod := credentialExtraString(credential, "auth_method", "authMethod")
	region := credentialExtraString(credential, "region")
	if region == "" {
		region = "us-east-1"
	}
	isCodeWhisperer := authMethod == "api_key" || authMethod == "external_idp" || authMethod == "idc"
	urls := append([]string{}, kiroFallbackURLs...)
	if primary != "" {
		urls = append([]string{primary}, urls...)
	}
	seen := map[string]bool{}
	ordered := []string{}
	appendUnique := func(items ...string) {
		for _, item := range items {
			if item == "" || seen[item] {
				continue
			}
			seen[item] = true
			ordered = append(ordered, item)
		}
	}
	if isCodeWhisperer {
		amazon := []string{}
		others := []string{}
		for _, item := range urls {
			if strings.Contains(item, "amazonaws.com") {
				if region != "us-east-1" {
					item = strings.Replace(item, "us-east-1", region, 1)
				}
				amazon = append(amazon, item)
			} else {
				others = append(others, item)
			}
		}
		appendUnique(amazon...)
		appendUnique(others...)
	} else {
		kiroDev := []string{}
		others := []string{}
		for _, item := range urls {
			if strings.Contains(item, "kiro.dev") {
				kiroDev = append(kiroDev, item)
			} else {
				others = append(others, item)
			}
		}
		appendUnique(kiroDev...)
		appendUnique(others...)
	}
	return ordered
}

func kiroEndpoint(base string) string {
	base = strings.TrimSuffix(strings.TrimSpace(base), "/")
	if strings.HasSuffix(base, "generateAssistantResponse") {
		return base
	}
	if base == "" {
		return ""
	}
	return base + "/generateAssistantResponse"
}

func kiroHeaders(credential store.Credential) http.Header {
	headers := http.Header{
		"Content-Type":        {"application/json"},
		"Accept":              {"application/vnd.amazon.eventstream"},
		"Amz-Sdk-Request":     {"attempt=1; max=3"},
		"Amz-Sdk-Invocation-Id": {uuid.NewString()},
		"User-Agent":          {"AWS-SDK-JS/3.0.0 kiro-ide/1.0.0"},
		"X-Amz-User-Agent":     {"aws-sdk-js/3.0.0 kiro-ide/1.0.0"},
	}
	authMethod := credentialExtraString(credential, "auth_method", "authMethod")
	token := credential.Secret
	if credential.OAuthToken != nil && credential.OAuthToken.AccessToken != "" {
		token = credential.OAuthToken.AccessToken
	}
	if authMethod == "api_key" {
		apiKey := credentialExtraString(credential, "api_key", "apiKey")
		if apiKey == "" {
			apiKey = token
		}
		headers.Set("Authorization", "Bearer "+apiKey)
		headers.Set("tokentype", "API_KEY")
	} else if token != "" {
		headers.Set("Authorization", "Bearer "+token)
		if authMethod == "external_idp" {
			headers.Set("TokenType", "EXTERNAL_IDP")
		}
	}
	return headers
}

var _ Adapter = (*kiroAdapter)(nil)

// Debug helper for tests.
func marshalKiroPayload(request canonical.Request, credential store.Credential) ([]byte, error) {
	payload, err := buildKiroPayload(request, credential)
	if err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

func (a *kiroAdapter) String() string { return "kiro" }
