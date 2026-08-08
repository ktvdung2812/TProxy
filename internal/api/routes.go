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
		"generateContent":       true,
		"streamGenerateContent": true,
		"loadCodeAssist":        true,
		"countTokens":           true,
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

func normalizeProxyPath(path string) string {
	path = strings.Split(path, "?")[0]
	switch {
	case path == "/responses":
		return "/v1/responses"
	case path == "/codex", strings.HasPrefix(path, "/codex/"):
		return "/v1/responses"
	case path == "/v1/v1":
		return "/v1"
	case strings.HasPrefix(path, "/v1/v1/"):
		return "/v1/" + strings.TrimPrefix(path, "/v1/v1/")
	}

	for _, prefix := range knownProviderRoutePrefixes {
		doublePrefix := "/" + prefix + "/v1/v1/"
		if strings.HasPrefix(path, doublePrefix) {
			return "/" + prefix + "/v1/" + strings.TrimPrefix(path, doublePrefix)
		}
		if path == "/"+prefix+"/v1/v1" {
			return "/" + prefix + "/v1"
		}
	}

	if strings.HasPrefix(path, "/anthropic/anthropic/") {
		return "/anthropic/" + strings.TrimPrefix(path, "/anthropic/anthropic/")
	}

	return path
}

func classifyProxyIngress(path string) (ingressRoute, bool) {
	path = normalizeProxyPath(strings.Split(path, "?")[0])
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
	captureIngressClientHeaders(r, request)
}

var ingressClientHeaderNames = []string{
	"anthropic-beta",
	"user-agent",
	"x-claude-code-session-id",
	"anthropic-dangerous-direct-browser-access",
	"x-app",
	"x-stainless-helper-method",
	"x-stainless-retry-count",
	"x-stainless-runtime-version",
	"x-stainless-package-version",
	"x-stainless-runtime",
	"x-stainless-lang",
	"x-stainless-arch",
	"x-stainless-os",
	"x-stainless-timeout",
	"package-version",
	"runtime-version",
	"os",
	"arch",
	"originator",
	"version",
	"session_id",
	"chatgpt-account-id",
}

func captureIngressClientHeaders(r *http.Request, request *canonical.Request) {
	if request == nil || r == nil {
		return
	}
	client := map[string]string{}
	for _, name := range ingressClientHeaderNames {
		if value := strings.TrimSpace(r.Header.Get(name)); value != "" {
			client[strings.ToLower(name)] = value
		}
	}
	if len(client) == 0 {
		return
	}
	if request.Metadata == nil {
		request.Metadata = map[string]any{}
	}
	request.Metadata["client_headers"] = client
}
