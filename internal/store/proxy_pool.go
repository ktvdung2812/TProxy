package store

import (
	"net/url"
	"strings"
)

func RedactProxyURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "direct") || strings.EqualFold(value, "none") {
		return value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return "<invalid proxy URL>"
	}
	parsed.User = nil
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}
