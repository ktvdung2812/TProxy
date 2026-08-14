// Package tlsfp reproduces the TLS ClientHello of the Claude Code CLI.
//
// The gateway already presents Claude Code's HTTP identity — User-Agent, the
// Stainless SDK tuple, the billing block, Claude Code tool names. Underneath
// that, Go's crypto/tls sends a handshake nothing like Node.js's, and offers
// HTTP/2 where the CLI offers only HTTP/1.1. Those two layers are visible in
// the same connection, so a request can claim to be Node.js while proving it is
// not. This package closes that gap for Claude traffic.
//
// Everything here describes one captured client and is meaningless if edited
// piecemeal: the cipher list, the curve list, the extension order and the ALPN
// value are a single fingerprint. Changing one value in isolation produces a
// handshake that matches no real client at all, which is worse than not
// imitating one.
package tlsfp

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
	xproxy "golang.org/x/net/proxy"
)

// Fingerprint captured from Claude Code running on Node.js 24.x.
//
//	JA3 44f88fca027f27bab4bb08d4af15f23e
//	JA4 t13d1714h1_5b57614c22b0_7baf387fc6ff
//
// The JA4 encodes what the values below must add up to: TLS 1.3, 17 cipher
// suites, 14 extensions, ALPN http/1.1. Treat it as a checksum when updating.
var (
	// cipherSuites — 17 suites, in Node's order. Order is part of the hash.
	cipherSuites = []uint16{
		0x1301, // TLS_AES_128_GCM_SHA256
		0x1302, // TLS_AES_256_GCM_SHA384
		0x1303, // TLS_CHACHA20_POLY1305_SHA256
		0xc02b, // ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
		0xc02f, // ECDHE_RSA_WITH_AES_128_GCM_SHA256
		0xc02c, // ECDHE_ECDSA_WITH_AES_256_GCM_SHA384
		0xc030, // ECDHE_RSA_WITH_AES_256_GCM_SHA384
		0xcca9, // ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256
		0xcca8, // ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256
		0xc009, // ECDHE_ECDSA_WITH_AES_128_CBC_SHA
		0xc013, // ECDHE_RSA_WITH_AES_128_CBC_SHA
		0xc00a, // ECDHE_ECDSA_WITH_AES_256_CBC_SHA
		0xc014, // ECDHE_RSA_WITH_AES_256_CBC_SHA
		0x009c, // RSA_WITH_AES_128_GCM_SHA256
		0x009d, // RSA_WITH_AES_256_GCM_SHA384
		0x002f, // RSA_WITH_AES_128_CBC_SHA
		0x0035, // RSA_WITH_AES_256_CBC_SHA
	}

	curves = []utls.CurveID{utls.X25519, utls.CurveP256, utls.CurveP384}

	signatureAlgorithms = []utls.SignatureScheme{
		0x0403, // ecdsa_secp256r1_sha256
		0x0804, // rsa_pss_rsae_sha256
		0x0401, // rsa_pkcs1_sha256
		0x0503, // ecdsa_secp384r1_sha384
		0x0805, // rsa_pss_rsae_sha384
		0x0501, // rsa_pkcs1_sha384
		0x0806, // rsa_pss_rsae_sha512
		0x0601, // rsa_pkcs1_sha512
		0x0201, // rsa_pkcs1_sha1
	}

	// extensionOrder — 14 extensions in the order Node emits them. GREASE is
	// absent on purpose: that is a Chrome behaviour, and adding it here would
	// describe a client that does not exist.
	extensionOrder = []uint16{
		0,      // server_name
		0xfe0d, // encrypted_client_hello
		23,     // extended_master_secret
		0xff01, // renegotiation_info
		10,     // supported_groups
		11,     // ec_point_formats
		35,     // session_ticket
		16,     // alpn
		5,      // status_request
		13,     // signature_algorithms
		18,     // signed_certificate_timestamp
		51,     // key_share
		45,     // psk_key_exchange_modes
		43,     // supported_versions
	}
)

// alpnProtocols is http/1.1 only. The CLI runs on Node's HTTP stack, which does
// not negotiate h2 by default; offering h2 here would contradict the very
// headers this fingerprint exists to support.
var alpnProtocols = []string{"http/1.1"}

// ErrProxyUnsupported reports a proxy this dialer cannot tunnel through. The
// caller is expected to fall back to an ordinary transport rather than fail the
// request: losing the fingerprint is better than losing the connection.
var ErrProxyUnsupported = errors.New("tlsfp: proxy scheme cannot carry a fingerprinted handshake")

func clientHelloSpec() *utls.ClientHelloSpec {
	extensions := make([]utls.TLSExtension, 0, len(extensionOrder))
	for _, id := range extensionOrder {
		switch id {
		case 0:
			extensions = append(extensions, &utls.SNIExtension{})
		case 5:
			extensions = append(extensions, &utls.StatusRequestExtension{})
		case 10:
			extensions = append(extensions, &utls.SupportedCurvesExtension{Curves: curves})
		case 11:
			extensions = append(extensions, &utls.SupportedPointsExtension{SupportedPoints: []uint8{0}})
		case 13:
			extensions = append(extensions, &utls.SignatureAlgorithmsExtension{SupportedSignatureAlgorithms: signatureAlgorithms})
		case 16:
			extensions = append(extensions, &utls.ALPNExtension{AlpnProtocols: alpnProtocols})
		case 18:
			extensions = append(extensions, &utls.SCTExtension{})
		case 23:
			extensions = append(extensions, &utls.ExtendedMasterSecretExtension{})
		case 35:
			extensions = append(extensions, &utls.SessionTicketExtension{})
		case 43:
			extensions = append(extensions, &utls.SupportedVersionsExtension{Versions: []uint16{utls.VersionTLS13, utls.VersionTLS12}})
		case 45:
			extensions = append(extensions, &utls.PSKKeyExchangeModesExtension{Modes: []uint8{utls.PskModeDHE}})
		case 51:
			extensions = append(extensions, &utls.KeyShareExtension{KeyShares: []utls.KeyShare{{Group: utls.X25519}}})
		case 0xfe0d:
			// A GREASE ECH carries a plausible random payload. An empty
			// extension of this type makes servers that validate ECH reject the
			// handshake outright.
			extensions = append(extensions, &utls.GREASEEncryptedClientHelloExtension{})
		case 0xff01:
			extensions = append(extensions, &utls.RenegotiationInfoExtension{})
		default:
			extensions = append(extensions, &utls.GenericExtension{Id: id})
		}
	}
	return &utls.ClientHelloSpec{
		CipherSuites:       cipherSuites,
		CompressionMethods: []uint8{0},
		Extensions:         extensions,
		TLSVersMax:         utls.VersionTLS13,
		TLSVersMin:         utls.VersionTLS10,
	}
}

// handshake wraps an established TCP connection in the fingerprinted TLS
// handshake. The connection is closed on failure so callers never leak it.
func handshake(ctx context.Context, conn net.Conn, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	tlsConn := utls.UClient(conn, &utls.Config{ServerName: host}, utls.HelloCustom)
	if err := tlsConn.ApplyPreset(clientHelloSpec()); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("tlsfp: apply client hello: %w", err)
	}
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("tlsfp: handshake: %w", err)
	}
	return tlsConn, nil
}

func dialDirect(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := (&net.Dialer{Timeout: 30 * time.Second}).DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	return handshake(ctx, conn, addr)
}

func dialViaSOCKS5(ctx context.Context, proxyURL *url.URL, network, addr string) (net.Conn, error) {
	var auth *xproxy.Auth
	if proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		auth = &xproxy.Auth{User: proxyURL.User.Username(), Password: password}
	}
	proxyAddr := proxyURL.Host
	if proxyURL.Port() == "" {
		proxyAddr = net.JoinHostPort(proxyURL.Hostname(), "1080")
	}
	dialer, err := xproxy.SOCKS5("tcp", proxyAddr, auth, xproxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("tlsfp: socks5 dialer: %w", err)
	}
	var conn net.Conn
	if contextDialer, ok := dialer.(xproxy.ContextDialer); ok {
		conn, err = contextDialer.DialContext(ctx, network, addr)
	} else {
		conn, err = dialer.Dial(network, addr)
	}
	if err != nil {
		return nil, fmt.Errorf("tlsfp: socks5 connect: %w", err)
	}
	return handshake(ctx, conn, addr)
}

// dialViaHTTPProxy opens a CONNECT tunnel by hand. net/http would do this
// itself, but it then performs its own TLS on the tunnel and never calls
// DialTLSContext, which would discard the fingerprint.
func dialViaHTTPProxy(ctx context.Context, proxyURL *url.URL, addr string) (net.Conn, error) {
	proxyAddr := proxyURL.Host
	if proxyURL.Port() == "" {
		proxyAddr = net.JoinHostPort(proxyURL.Hostname(), "80")
	}
	conn, err := (&net.Dialer{Timeout: 30 * time.Second}).DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("tlsfp: connect to proxy: %w", err)
	}
	request := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: make(http.Header),
	}
	if proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		credentials := base64.StdEncoding.EncodeToString([]byte(proxyURL.User.Username() + ":" + password))
		request.Header.Set("Proxy-Authorization", "Basic "+credentials)
	}
	if err := request.Write(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("tlsfp: write CONNECT: %w", err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("tlsfp: read CONNECT response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, fmt.Errorf("tlsfp: proxy CONNECT failed: %s", response.Status)
	}
	// The handshake continues on the raw conn, so anything the proxy pipelined
	// into the reader would be silently dropped. That should never happen after
	// a 200 CONNECT; failing loudly beats a handshake that fails obscurely.
	if reader.Buffered() > 0 {
		_ = conn.Close()
		return nil, errors.New("tlsfp: proxy sent data before the tunnel was ready")
	}
	return handshake(ctx, conn, addr)
}

// Transport returns an http.Transport whose TLS handshake carries the Claude
// Code fingerprint. proxyRaw may be empty, "direct", an http:// or a socks5://
// URL; anything else returns ErrProxyUnsupported.
func Transport(proxyRaw string) (*http.Transport, error) {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		// The handshake offers http/1.1 only, so h2 could never be negotiated;
		// leaving this on would just have net/http look for a protocol the
		// fingerprint declines to ask for.
		ForceAttemptHTTP2: false,
	}

	raw := strings.TrimSpace(proxyRaw)
	if raw == "" || strings.EqualFold(raw, "direct") || strings.EqualFold(raw, "none") {
		transport.DialTLSContext = dialDirect
		return transport, nil
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("tlsfp: invalid proxy URL")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http":
		transport.DialTLSContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
			return dialViaHTTPProxy(ctx, parsed, addr)
		}
	case "socks5", "socks5h":
		transport.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialViaSOCKS5(ctx, parsed, network, addr)
		}
	default:
		// An https:// proxy needs its own TLS session before the tunnel exists,
		// which this dialer has no way to establish.
		return nil, ErrProxyUnsupported
	}
	return transport, nil
}
