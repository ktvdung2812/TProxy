package mitm

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// RootCACommonName identifies tproxy's interception root in OS trust stores.
// Trust and untrust commands look the certificate up by this name.
const RootCACommonName = "tproxy MITM Root CA"

const (
	rootCAValidity = 10 * 365 * 24 * time.Hour
	rootCARenewal  = 30 * 24 * time.Hour
	leafValidity   = 365 * 24 * time.Hour
)

// CA owns the interception root and signs per-host leaf certificates on demand.
type CA struct {
	dir  string
	cert *x509.Certificate
	key  crypto.Signer
	der  []byte

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

// CertPath and KeyPath are the PEM files on disk. CertPath is what OS trust
// commands install.
func (c *CA) CertPath() string { return filepath.Join(c.dir, "rootCA.crt") }
func (c *CA) KeyPath() string  { return filepath.Join(c.dir, "rootCA.key") }

// LoadOrCreateCA loads the root from dir, generating a new one when it is
// missing, unreadable or within rootCARenewal of expiry. A regenerated root
// must be trusted again, which Status reports through CertTrusted.
func LoadOrCreateCA(dir string) (*CA, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create MITM directory: %w", err)
	}
	ca := &CA{dir: dir, leaves: map[string]*tls.Certificate{}}
	if err := ca.load(); err == nil && time.Until(ca.cert.NotAfter) > rootCARenewal {
		return ca, nil
	}
	if err := ca.generate(); err != nil {
		return nil, err
	}
	return ca, nil
}

func (c *CA) load() error {
	certPEM, err := os.ReadFile(c.CertPath())
	if err != nil {
		return err
	}
	keyPEM, err := os.ReadFile(c.KeyPath())
	if err != nil {
		return err
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return err
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	signer, ok := pair.PrivateKey.(crypto.Signer)
	if !ok || !cert.IsCA {
		return errors.New("invalid MITM root CA")
	}
	c.cert, c.key, c.der = cert, signer, pair.Certificate[0]
	return nil
}

func (c *CA) generate() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          randomSerial(),
		Subject:               pkix.Name{CommonName: RootCACommonName, Organization: []string{"tproxy"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(rootCAValidity),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create MITM root CA: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(c.KeyPath(), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return fmt.Errorf("write MITM root key: %w", err)
	}
	if err := os.WriteFile(c.CertPath(), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return fmt.Errorf("write MITM root certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	c.cert, c.key, c.der = cert, key, der
	return nil
}

// Fingerprint returns the uppercase hex SHA-1 of the root, the identifier the
// Windows certificate store and macOS `security -Z` print.
func (c *CA) Fingerprint() string {
	sum := sha1.Sum(c.der)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// FingerprintSHA256 is shown in the dashboard so operators can match the
// installed root against the file on disk.
func (c *CA) FingerprintSHA256() string {
	sum := sha256.Sum256(c.der)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// NotAfter reports the root's expiry.
func (c *CA) NotAfter() time.Time { return c.cert.NotAfter }

// Pool returns a pool holding only this root, for tests and self-checks.
func (c *CA) Pool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(c.cert)
	return pool
}

// GetCertificate implements tls.Config.GetCertificate, issuing a leaf for the
// requested SNI name. Without SNI there is no host to sign for.
func (c *CA) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	host := strings.ToLower(strings.TrimSuffix(hello.ServerName, "."))
	if host == "" {
		return nil, errors.New("mitm: client offered no SNI")
	}
	return c.Leaf(host)
}

// Leaf returns a cached certificate for host, signing one when needed.
func (c *CA) Leaf(host string) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if leaf, ok := c.leaves[host]; ok && time.Until(leaf.Leaf.NotAfter) > 24*time.Hour {
		return leaf, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	notAfter := now.Add(leafValidity)
	if notAfter.After(c.cert.NotAfter) {
		notAfter = c.cert.NotAfter
	}
	template := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, c.cert, &key.PublicKey, c.key)
	if err != nil {
		return nil, fmt.Errorf("sign leaf for %s: %w", host, err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	leaf := &tls.Certificate{Certificate: [][]byte{der, c.der}, PrivateKey: key, Leaf: parsed}
	c.leaves[host] = leaf
	return leaf, nil
}

func randomSerial() *big.Int {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return serial
}
