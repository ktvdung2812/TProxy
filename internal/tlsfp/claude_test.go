package tlsfp

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	utls "github.com/refraction-networking/utls"
)

// The JA4 of the captured client encodes these counts, so a spec that drifts
// from them no longer matches the client it claims to be.
func TestClientHelloMatchesCapturedShape(t *testing.T) {
	spec := clientHelloSpec()

	if len(spec.CipherSuites) != 17 {
		t.Fatalf("cipher suites = %d, want 17 (JA4 says t13d17..)", len(spec.CipherSuites))
	}
	if len(spec.Extensions) != 14 {
		t.Fatalf("extensions = %d, want 14 (JA4 says ..1714h1)", len(spec.Extensions))
	}
	if spec.CipherSuites[0] != 0x1301 {
		t.Fatalf("first cipher = %#x, want 0x1301", spec.CipherSuites[0])
	}
	if spec.TLSVersMax != utls.VersionTLS13 {
		t.Fatalf("max version = %#x", spec.TLSVersMax)
	}
	if len(spec.CompressionMethods) != 1 || spec.CompressionMethods[0] != 0 {
		t.Fatalf("compression = %v, want null only", spec.CompressionMethods)
	}
}

// Node does not send GREASE; including it would describe a client that is
// neither Node nor Chrome.
func TestClientHelloHasNoGREASE(t *testing.T) {
	for index, extension := range clientHelloSpec().Extensions {
		if _, isGREASE := extension.(*utls.UtlsGREASEExtension); isGREASE {
			t.Fatalf("extension %d is GREASE", index)
		}
	}
}

// ALPN must offer http/1.1 alone: the headers claim Node's HTTP stack, and
// offering h2 would contradict them on the same connection.
func TestALPNOffersHTTP11Only(t *testing.T) {
	var found *utls.ALPNExtension
	for _, extension := range clientHelloSpec().Extensions {
		if alpn, ok := extension.(*utls.ALPNExtension); ok {
			found = alpn
		}
	}
	if found == nil {
		t.Fatal("no ALPN extension in the client hello")
	}
	if len(found.AlpnProtocols) != 1 || found.AlpnProtocols[0] != "http/1.1" {
		t.Fatalf("alpn = %v, want [http/1.1]", found.AlpnProtocols)
	}
}

// The extension order is part of the fingerprint, so it is pinned rather than
// left to whatever order the builder happens to append in.
func TestExtensionOrderIsPinned(t *testing.T) {
	want := []uint16{0, 0xfe0d, 23, 0xff01, 10, 11, 35, 16, 5, 13, 18, 51, 45, 43}
	if len(extensionOrder) != len(want) {
		t.Fatalf("extension order length = %d, want %d", len(extensionOrder), len(want))
	}
	for index := range want {
		if extensionOrder[index] != want[index] {
			t.Fatalf("extension %d = %d, want %d", index, extensionOrder[index], want[index])
		}
	}
}

func TestTransportRejectsUnsupportedProxy(t *testing.T) {
	if _, err := Transport("https://proxy.example.test:8443"); err != ErrProxyUnsupported {
		t.Fatalf("https proxy error = %v, want ErrProxyUnsupported", err)
	}
	if _, err := Transport("ftp://proxy.example.test"); err != ErrProxyUnsupported {
		t.Fatalf("ftp proxy error = %v, want ErrProxyUnsupported", err)
	}
	if _, err := Transport("://broken"); err == nil {
		t.Fatal("expected a malformed proxy URL to be rejected")
	}
}

func TestTransportNeverNegotiatesHTTP2(t *testing.T) {
	for _, raw := range []string{"", "direct", "http://proxy.example.test:8080", "socks5://proxy.example.test:1080"} {
		transport, err := Transport(raw)
		if err != nil {
			t.Fatalf("Transport(%q) = %v", raw, err)
		}
		if transport.ForceAttemptHTTP2 {
			t.Fatalf("Transport(%q) attempts h2, which the handshake never offers", raw)
		}
		if transport.DialTLSContext == nil {
			t.Fatalf("Transport(%q) has no fingerprinted dial", raw)
		}
	}
}

// End-to-end against a local TLS server: the handshake must complete and the
// server must see the captured cipher list rather than Go's.
func TestHandshakeCompletesAndPresentsCapturedCiphers(t *testing.T) {
	var offered []uint16
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	server.TLS = &tls.Config{
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			offered = append([]uint16(nil), hello.CipherSuites...)
			return nil, nil
		},
	}
	server.StartTLS()
	defer server.Close()

	host := strings.TrimPrefix(server.URL, "https://")
	conn, err := net.Dial("tcp", host)
	if err != nil {
		t.Fatal(err)
	}
	// The test server uses a self-signed certificate, so verification is
	// disabled here; the handshake shape is what is under test.
	tlsConn := utls.UClient(conn, &utls.Config{ServerName: "127.0.0.1", InsecureSkipVerify: true}, utls.HelloCustom)
	if err := tlsConn.ApplyPreset(clientHelloSpec()); err != nil {
		t.Fatal(err)
	}
	if err := tlsConn.HandshakeContext(context.Background()); err != nil {
		t.Fatalf("handshake failed: %v", err)
	}
	defer tlsConn.Close()

	if len(offered) != len(cipherSuites) {
		t.Fatalf("server saw %d ciphers, client sends %d", len(offered), len(cipherSuites))
	}
	for index := range cipherSuites {
		if offered[index] != cipherSuites[index] {
			t.Fatalf("cipher %d: server saw %#x, want %#x", index, offered[index], cipherSuites[index])
		}
	}
	if got := tlsConn.ConnectionState().NegotiatedProtocol; got != "http/1.1" {
		t.Fatalf("negotiated protocol = %q, want http/1.1", got)
	}
}

// Streaming is the path real traffic takes, and it behaves differently from a
// buffered response: chunks must arrive as the server writes them rather than
// after the body completes. HTTP/1.1 chunked transfer over the fingerprinted
// connection is what carries Anthropic's SSE.
func TestTransportStreamsIncrementally(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("test server cannot flush")
			return
		}
		_, _ = w.Write([]byte("event: first\ndata: {\"n\":1}\n\n"))
		flusher.Flush()
		<-release // hold the response open so a buffered read would block here
		_, _ = w.Write([]byte("event: second\ndata: {\"n\":2}\n\n"))
		flusher.Flush()
	}))
	server.EnableHTTP2 = false
	server.StartTLS()
	defer server.Close()

	transport, err := Transport("")
	if err != nil {
		t.Fatal(err)
	}
	transport.TLSClientConfig = nil
	// The fingerprinted dialer builds its own TLS config, so the test server's
	// self-signed certificate is trusted by dialing with verification off.
	transport.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, dialErr := net.Dial(network, addr)
		if dialErr != nil {
			return nil, dialErr
		}
		tlsConn := utls.UClient(conn, &utls.Config{ServerName: "127.0.0.1", InsecureSkipVerify: true}, utls.HelloCustom)
		if applyErr := tlsConn.ApplyPreset(clientHelloSpec()); applyErr != nil {
			_ = conn.Close()
			return nil, applyErr
		}
		if hsErr := tlsConn.HandshakeContext(ctx); hsErr != nil {
			_ = conn.Close()
			return nil, hsErr
		}
		return tlsConn, nil
	}

	client := &http.Client{Transport: transport}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer response.Body.Close()

	if response.Proto != "HTTP/1.1" {
		t.Fatalf("proto = %s, want HTTP/1.1", response.Proto)
	}

	buffer := make([]byte, 64)
	read, err := response.Body.Read(buffer)
	if err != nil {
		t.Fatalf("first chunk read failed: %v", err)
	}
	if !strings.Contains(string(buffer[:read]), "first") {
		t.Fatalf("first chunk = %q", buffer[:read])
	}

	close(release)
	rest := make([]byte, 64)
	read, err = response.Body.Read(rest)
	if err != nil {
		t.Fatalf("second chunk read failed: %v", err)
	}
	if !strings.Contains(string(rest[:read]), "second") {
		t.Fatalf("second chunk = %q", rest[:read])
	}
}
