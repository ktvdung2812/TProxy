package providers

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tproxy/tproxy/internal/store"
	xproxy "golang.org/x/net/proxy"
)

type proxyContextKey struct{}

func withCredentialProxy(ctx context.Context, credential store.Credential) context.Context {
	if strings.TrimSpace(credential.ProxyURL) == "" {
		return ctx
	}
	return context.WithValue(ctx, proxyContextKey{}, credential.ProxyURL)
}

type proxyTransport struct {
	direct *http.Transport
	mu     sync.Mutex
	items  map[string]*http.Transport
}

func newProxyTransport() *proxyTransport {
	base := cloneHTTPTransport()
	base.Proxy = http.ProxyFromEnvironment
	return &proxyTransport{direct: base, items: make(map[string]*http.Transport)}
}

func cloneHTTPTransport() *http.Transport {
	var transport *http.Transport
	if defaults, ok := http.DefaultTransport.(*http.Transport); ok && defaults != nil {
		transport = defaults.Clone()
	} else {
		transport = &http.Transport{}
	}
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 20
	transport.IdleConnTimeout = 90 * time.Second
	return transport
}

func (p *proxyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	raw, _ := request.Context().Value(proxyContextKey{}).(string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return p.direct.RoundTrip(request)
	}
	transport, err := p.transport(raw)
	if err != nil {
		return nil, err
	}
	return transport.RoundTrip(request)
}

func (p *proxyTransport) transport(raw string) (*http.Transport, error) {
	key := strings.TrimSpace(raw)
	p.mu.Lock()
	if existing := p.items[key]; existing != nil {
		p.mu.Unlock()
		return existing, nil
	}
	p.mu.Unlock()
	transport, err := buildProxyTransport(key)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	if existing := p.items[key]; existing != nil {
		p.mu.Unlock()
		transport.CloseIdleConnections()
		return existing, nil
	}
	p.items[key] = transport
	p.mu.Unlock()
	return transport, nil
}

func (p *proxyTransport) CloseIdleConnections() {
	p.direct.CloseIdleConnections()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, transport := range p.items {
		transport.CloseIdleConnections()
	}
}

func buildProxyTransport(raw string) (*http.Transport, error) {
	if strings.EqualFold(raw, "direct") || strings.EqualFold(raw, "none") {
		transport := cloneHTTPTransport()
		transport.Proxy = nil
		return transport, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("invalid proxy URL")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		transport := cloneHTTPTransport()
		transport.Proxy = http.ProxyURL(parsed)
		return transport, nil
	case "socks5", "socks5h":
		var auth *xproxy.Auth
		if parsed.User != nil {
			password, _ := parsed.User.Password()
			auth = &xproxy.Auth{User: parsed.User.Username(), Password: password}
		}
		dialer, dialErr := xproxy.SOCKS5("tcp", parsed.Host, auth, xproxy.Direct)
		if dialErr != nil {
			return nil, fmt.Errorf("create SOCKS5 proxy dialer: %w", dialErr)
		}
		transport := cloneHTTPTransport()
		transport.Proxy = nil
		if contextDialer, ok := dialer.(xproxy.ContextDialer); ok {
			transport.DialContext = contextDialer.DialContext
		} else {
			transport.DialContext = func(_ context.Context, network, address string) (net.Conn, error) {
				return dialer.Dial(network, address)
			}
		}
		return transport, nil
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q", parsed.Scheme)
	}
}

func NewProxyHTTPClient(raw string, timeout time.Duration) (*http.Client, error) {
	transport, err := buildProxyTransport(raw)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: transport, Timeout: timeout}, nil
}
