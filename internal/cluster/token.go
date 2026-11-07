package cluster

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

const joinTokenVersion = 1

// JoinToken is the decoded form of a "tpc1_" join token.
type JoinToken struct {
	Version   int    `json:"v"`
	HubURL    string `json:"u"`
	ClusterID string `json:"c"`
	Secret    string `json:"s"`
}

func (t JoinToken) validate() error {
	if t.Version != joinTokenVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrJoinTokenInvalid, t.Version)
	}
	if strings.TrimSpace(t.HubURL) == "" || strings.TrimSpace(t.ClusterID) == "" || strings.TrimSpace(t.Secret) == "" {
		return fmt.Errorf("%w: hub URL, cluster id and secret are required", ErrJoinTokenInvalid)
	}
	return nil
}

func EncodeJoinToken(t JoinToken) (string, error) {
	if err := t.validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	return JoinTokenPrefix + base64.RawURLEncoding.EncodeToString(data), nil
}

func DecodeJoinToken(token string) (JoinToken, error) {
	payload, ok := strings.CutPrefix(strings.TrimSpace(token), JoinTokenPrefix)
	if !ok || payload == "" {
		return JoinToken{}, fmt.Errorf("%w: expected a %s token", ErrJoinTokenInvalid, JoinTokenPrefix)
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(payload, "="))
	if err != nil {
		return JoinToken{}, fmt.Errorf("%w: malformed encoding", ErrJoinTokenInvalid)
	}
	var t JoinToken
	if err := json.Unmarshal(data, &t); err != nil {
		return JoinToken{}, fmt.Errorf("%w: malformed payload", ErrJoinTokenInvalid)
	}
	if err := t.validate(); err != nil {
		return JoinToken{}, err
	}
	return t, nil
}

// NewSecret returns 32 random bytes encoded as unpadded base64url.
func NewSecret() string {
	buf := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		panic("cluster: cryptographic entropy unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// HashSecret is the only form in which the hub stores join and node secrets.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

// NormalizeHubURL validates a hub base URL and returns it as scheme://host.
// Plain http is accepted only with allowInsecure and a host that cannot be on
// the public internet; a hostname other than localhost cannot be verified
// without DNS, so it never qualifies.
func NormalizeHubURL(raw string, allowInsecure bool) (string, error) {
	invalid := newError(http.StatusBadRequest, CodeInvalidRequest, "hub URL must be an absolute http(s) URL without credentials, path, query or fragment")
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Opaque != "" || u.Host == "" || u.Hostname() == "" {
		return "", invalid
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", invalid
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", invalid
	}
	if scheme == "http" && (!allowInsecure || !insecureHostAllowed(u.Hostname())) {
		return "", newError(http.StatusBadRequest, CodeInsecureHubURL, "a plain http hub URL needs allow_insecure and a loopback or private network address")
	}
	host := u.Host
	if !strings.Contains(host, "%") { // interface names in IPv6 zones keep their case
		host = strings.ToLower(host)
	}
	return (&url.URL{Scheme: scheme, Host: host}).String(), nil
}

func insecureHostAllowed(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || cgnatPrefix.Contains(addr)
}
