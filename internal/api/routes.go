package api

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
)

const proxyRouteContext contextKey = "proxy-route"

var (
	geminiNativeActions = map[string]bool{
		"generateContent":         true,
		"streamGenerateContent":   true,
		"loadCodeAssist":          true,
		"countTokens":             true,
	}
	knownProviderRoutePrefixes = []string{
		"openai",
		"chatgpt",
		"anthropic",
		"gemini",
		"gemini-cli",
		"copilot",
	}
	providerPrefixedPathPattern = regexp.MustCompile(`^/([a-z0-9][a-z0-9._-]*)/(v1(?:beta)?)(/.*)$`)
)

type ingressRoute struct {
	CanonicalPath   string
	RouteProvider   string
	DisableFallback bool
	GeminiAction    string
}

func classifyProxyIngress(path string) (ingressRoute, bool) {
	path = strings.Split(path, "?")[0]
	if path == "" {
		return ingressRoute{}, false
	}

	if strings.HasPrefix(path, "/v1internal:") {
		action := strings.TrimPrefix(path, "/v1internal:")
		if !geminiNativeActions[action] {
			return ingressRoute{}, false
		}
		return ingressRoute{
			CanonicalPath:   path,
			RouteProvider:   "gemini-cli",
			DisableFallback: true,
			GeminiAction:    action,
		}, true
	}

	for _, prefix := range knownProviderRoutePrefixes {
		marker := "/" + prefix + "/"
		if path == "/"+prefix {
			return ingressRoute{CanonicalPath: "/", RouteProvider: prefix, DisableFallback: true}, true
		}
		if strings.HasPrefix(path, marker) {
			return ingressRoute{
				CanonicalPath:   strings.TrimPrefix(path, "/"+prefix),
				RouteProvider:   prefix,
				DisableFallback: true,
			}, true
		}
	}

	if matches := providerPrefixedPathPattern.FindStringSubmatch(path); matches != nil {
		provider := strings.ToLower(matches[1])
		if provider == "github" || provider == "api" || provider == "v1" || provider == "v1beta" {
			return ingressRoute{}, false
		}
		return ingressRoute{
			CanonicalPath:   "/" + matches[2] + matches[3],
			RouteProvider:   provider,
			DisableFallback: true,
		}, true
	}

	if strings.HasPrefix(path, "/v1/") || path == "/v1" || strings.HasPrefix(path, "/v1beta/") || path == "/v1beta" {
		return ingressRoute{CanonicalPath: path}, true
	}

	return ingressRoute{}, false
}

func attachIngressRoute(r *http.Request, route ingressRoute) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), proxyRouteContext, route))
}

func ingressRouteFromContext(r *http.Request) (ingressRoute, bool) {
	route, ok := r.Context().Value(proxyRouteContext).(ingressRoute)
	return route, ok
}

func resolveIngressModel(r *http.Request, bodyModel string) string {
	model := strings.TrimSpace(bodyModel)
	if header := strings.TrimSpace(r.Header.Get("X-Model")); header != "" {
		if parsed := parseModelSelector(header); parsed.Model != "" {
			model = parsed.Model
			if parsed.Provider != "" {
				return formatProviderModel(parsed.Provider, parsed.Model)
			}
		}
	}
	if parsed := parseModelSelector(model); parsed.Provider != "" {
		return formatProviderModel(parsed.Provider, parsed.Model)
	}
	if route, ok := ingressRouteFromContext(r); ok && route.RouteProvider != "" && model != "" {
		if _, _, scoped := splitModelSelector(model); !scoped {
			return formatProviderModel(route.RouteProvider, model)
		}
	}
	return model
}

func attachIngressMetadata(r *http.Request, request *canonical.Request) {
	if request == nil {
		return
	}
	route, ok := ingressRouteFromContext(r)
	if !ok {
		return
	}
	if request.Metadata == nil {
		request.Metadata = map[string]any{}
	}
	if route.RouteProvider != "" {
		request.Metadata["pinned_provider"] = route.RouteProvider
	}
	if route.DisableFallback {
		request.Metadata["disable_fallback"] = true
	}
}
