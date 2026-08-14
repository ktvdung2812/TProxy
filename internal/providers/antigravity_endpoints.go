package providers

import (
	"net/http"
	"strings"

	"github.com/tproxy/tproxy/internal/store"
)

// Cloud Code is served from more than one host, and they do not behave alike.
// Measured against a live account: cloudcode-pa.googleapis.com answered every
// generate call with 429 RESOURCE_EXHAUSTED for over an hour — while the same
// token, model and payload succeeded on daily-cloudcode-pa.googleapis.com, and
// the Antigravity IDE kept working throughout. The throttle is per-host, not
// per-account, so a gateway pinned to one host looks broken while the product
// it proxies is fine.
//
// The order matches CLIProxyAPI's antigravityBaseURLFallbackOrder, which puts
// daily first for the same reason.
// Variables rather than constants so tests can point the fallback at local
// servers; they are never reassigned at runtime.
var (
	antigravityDailyBaseURL = "https://daily-cloudcode-pa.googleapis.com"
	antigravityProdBaseURL  = "https://cloudcode-pa.googleapis.com"
)

// antigravityBaseURLs returns the hosts to try, in order. An operator who
// configured a host other than the two defaults gets exactly that host: the
// override exists to pin traffic somewhere specific, so silently falling back
// would defeat it.
func antigravityBaseURLs(provider store.Provider) []string {
	base := antigravityBaseURL(provider.BaseURL)
	if base != "" && !antigravityDefaultBaseURL(base) {
		return []string{base}
	}
	return []string{antigravityDailyBaseURL, antigravityProdBaseURL}
}

func antigravityDefaultBaseURL(base string) bool {
	normalized := strings.ToLower(strings.TrimRight(strings.TrimSpace(base), "/"))
	return normalized == antigravityDailyBaseURL || normalized == antigravityProdBaseURL
}

// antigravityShouldTryNextHost reports whether a failure is worth retrying on
// another host. Authentication and request-shape problems travel with the
// request, so repeating them elsewhere only wastes quota; capacity and
// availability answers are host-specific and are exactly what the fallback is
// for.
func antigravityShouldTryNextHost(err error) bool {
	if err == nil {
		return false
	}
	switch status := Status(err); {
	case status == http.StatusTooManyRequests:
		return true
	case status == http.StatusNotFound:
		// The hosts do not carry the same catalogue: the 3.7 Flash family is
		// absent from daily and answers 404 there while prod serves it. A 404 is
		// therefore a statement about this host, not about the model.
		return true
	case status == 0: // network failure reaching this host
		return true
	case status >= 500:
		return true
	default:
		return false
	}
}
