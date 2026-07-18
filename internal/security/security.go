package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
)

type Encryptor struct {
	aead cipher.AEAD
}

func NewEncryptor(secret string) (*Encryptor, error) {
	if strings.TrimSpace(secret) == "" {
		return &Encryptor{}, nil
	}
	key, err := decodeKey(secret)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}
	return &Encryptor{aead: aead}, nil
}

func decodeKey(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	decoded, err = base64.StdEncoding.DecodeString(value)
	if err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	return nil, errors.New("master key must be a base64 encoded 32-byte value")
}

func GenerateMasterKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(key), nil
}

func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if e == nil || e.aead == nil {
		return "", errors.New("TPROXY_MASTER_KEY is required to store provider credentials")
	}
	nonce := make([]byte, e.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := e.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (e *Encryptor) Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	if e == nil || e.aead == nil {
		return "", errors.New("master key is unavailable")
	}
	data, err := base64.RawURLEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	nonceSize := e.aead.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("invalid encrypted credential")
	}
	plaintext, err := e.aead.Open(nil, data[:nonceSize], data[nonceSize:], nil)
	if err != nil {
		return "", errors.New("decrypt credential")
	}
	return string(plaintext), nil
}

func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func ConstantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func BearerToken(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(value) < 7 || !strings.EqualFold(value[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(value[7:])
}

func IsLoopback(r *http.Request) bool {
	if r == nil {
		return false
	}
	// Without an explicit trusted-proxy configuration, forwarded headers are
	// untrusted input. Treating a loopback reverse proxy as the client would
	// otherwise bypass local-only management and client-key gates.
	if r.Header != nil && (r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-IP") != "") {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && ip.IsLoopback()
}

func NewID(prefix string) string {
	buf := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		panic("security: cryptographic entropy unavailable")
	}
	return prefix + base64.RawURLEncoding.EncodeToString(buf)
}

func RedactHeader(name, value string) string {
	lower := strings.ToLower(name)
	normalized := strings.NewReplacer("-", "", "_", "", " ", "").Replace(lower)
	if normalized == "authorization" || normalized == "cookie" || strings.Contains(normalized, "apikey") || strings.Contains(normalized, "token") {
		return "[redacted]"
	}
	return value
}

var secretTextPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(authorization\s*[:=]\s*(?:bearer\s+)?)[^\s,;"']+`),
	regexp.MustCompile(`(?i)(\b(?:api[_-]?key|access[_-]?token|refresh[_-]?token|client[_-]?secret|password|token)\b\s*[":=]+\s*")[^"]+`),
	regexp.MustCompile(`(?i)(\b(?:api[_-]?key|access[_-]?token|refresh[_-]?token|client[_-]?secret|password|token)\b\s*[=:]\s*)[^\s,;]+`),
	regexp.MustCompile(`(?i)(\b(?:https?|socks5h?)://)[^\s/@:]+:[^\s/@]+@`),
}

func RedactText(value string) string {
	for _, pattern := range secretTextPatterns {
		value = pattern.ReplaceAllString(value, `${1}[REDACTED]`)
	}
	return value
}
