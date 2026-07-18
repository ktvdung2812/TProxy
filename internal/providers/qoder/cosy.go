package qoder

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

type CosyCreds struct {
	UserID     string
	AuthToken  string
	Name       string
	Email      string
	MachineID  string
}

type encryptedUserInfo struct {
	CosyKey string
	Info    string
}

// BuildCosyHeaders signs a Qoder request with the COSY hybrid scheme.
func BuildCosyHeaders(body []byte, requestURL string, creds CosyCreds) (map[string]string, error) {
	if strings.TrimSpace(creds.UserID) == "" {
		return nil, errors.New("cosy: user id is empty")
	}
	if strings.TrimSpace(creds.AuthToken) == "" {
		return nil, errors.New("cosy: auth token is empty")
	}
	if body == nil {
		body = []byte{}
	}
	enc, err := encryptUserInfo(creds)
	if err != nil {
		return nil, err
	}
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	requestID := uuid.NewString()
	payloadJSON, _ := json.Marshal(map[string]string{
		"version":     "v1",
		"requestId":   requestID,
		"info":        enc.Info,
		"cosyVersion": IDEVersion,
		"ideVersion":  "",
	})
	payloadB64 := base64.StdEncoding.EncodeToString(payloadJSON)
	sigPath := computeSigPath(requestURL)
	sigInput := payloadB64 + "\n" + enc.CosyKey + "\n" + timestamp + "\n" + string(body) + "\n" + sigPath
	sig := md5Hex([]byte(sigInput))
	machineID := creds.MachineID
	if machineID == "" {
		machineID = uuid.NewString()
	}
	bodyHash := md5Hex(body)
	return map[string]string{
		"Authorization":          "Bearer COSY." + payloadB64 + "." + sig,
		"Cosy-Key":               enc.CosyKey,
		"Cosy-User":              creds.UserID,
		"Cosy-Date":              timestamp,
		"Cosy-Version":           IDEVersion,
		"Cosy-Machineid":         machineID,
		"Cosy-Machinetoken":      machineID,
		"Cosy-Machinetype":       MachineType,
		"Cosy-Machineos":         MachineOS,
		"Cosy-Clienttype":        ClientType,
		"Cosy-Clientip":          "127.0.0.1",
		"Cosy-Bodyhash":          bodyHash,
		"Cosy-Bodylength":        fmt.Sprintf("%d", len(body)),
		"Cosy-Sigpath":           sigPath,
		"Cosy-Data-Policy":       DataPolicy,
		"Cosy-Organization-Id":   "",
		"Cosy-Organization-Tags": "",
		"Login-Version":          LoginVersion,
		"X-Request-Id":           uuid.NewString(),
	}, nil
}

func encryptUserInfo(creds CosyCreds) (encryptedUserInfo, error) {
	aesKey := uuid.NewString()[:16]
	plaintext, _ := json.Marshal(map[string]string{
		"uid":                  creds.UserID,
		"security_oauth_token": creds.AuthToken,
		"name":                 creds.Name,
		"aid":                  "",
		"email":                creds.Email,
	})
	infoB64, err := aesEncryptCBCBase64(string(plaintext), aesKey)
	if err != nil {
		return encryptedUserInfo{}, err
	}
	cosyKey, err := rsaEncryptBase64(aesKey)
	if err != nil {
		return encryptedUserInfo{}, err
	}
	return encryptedUserInfo{CosyKey: cosyKey, Info: infoB64}, nil
}

func aesEncryptCBCBase64(plaintext, keyStr string) (string, error) {
	key := []byte(keyStr)
	if len(key) != 16 {
		return "", fmt.Errorf("aes key must be 16 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	iv := key
	padded := pkcs7Pad([]byte(plaintext), block.BlockSize())
	out := make([]byte, len(padded))
	mode := cipher.NewCBCEncrypter(block, iv)
	mode.CryptBlocks(out, padded)
	return base64.StdEncoding.EncodeToString(out), nil
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - (len(data) % blockSize)
	out := make([]byte, len(data)+padding)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(padding)
	}
	return out
}

func rsaEncryptBase64(data string) (string, error) {
	block, _ := pem.Decode([]byte(RSAPublicKey))
	if block == nil {
		return "", errors.New("invalid RSA public key")
	}
	pubAny, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return "", err
	}
	pub, ok := pubAny.(*rsa.PublicKey)
	if !ok {
		return "", errors.New("not an RSA public key")
	}
	encrypted, err := rsa.EncryptPKCS1v15(rand.Reader, pub, []byte(data))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

func computeSigPath(requestURL string) string {
	parsed, err := url.Parse(requestURL)
	if err != nil {
		return ""
	}
	path := parsed.Path
	if strings.HasPrefix(path, "/algo") {
		return path[len("/algo"):]
	}
	return path
}

func md5Hex(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}
