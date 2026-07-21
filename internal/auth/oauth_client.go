package auth

import (
	"net/http"
	"strings"
	"time"
)

const oauthUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

type oauthRoundTripper struct {
	base http.RoundTripper
}

func (t oauthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	cloned := req.Clone(req.Context())
	if cloned.Header.Get("User-Agent") == "" {
		cloned.Header.Set("User-Agent", oauthUserAgent)
	}
	return base.RoundTrip(cloned)
}

func oauthHTTPClient(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	transport := client.Transport
	if transport == nil {
		if _, ok := http.DefaultTransport.(*oauthRoundTripper); ok {
			transport = http.DefaultTransport
		}
	}
	if _, ok := transport.(oauthRoundTripper); ok {
		return client
	}
	cloned := *client
	cloned.Transport = oauthRoundTripper{base: transport}
	return &cloned
}

func providerAllowsStatelessCallback(providerType string) bool {
	switch providerType {
	case "cline", "clinepass", "kimchi":
		return true
	default:
		return false
	}
}

func looksLikeHTMLResponse(data []byte) bool {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "<!doctype") || strings.HasPrefix(lower, "<html")
}
