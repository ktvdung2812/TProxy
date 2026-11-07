package cluster

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func TestJoinTokenRoundTrip(t *testing.T) {
	want := JoinToken{Version: 1, HubURL: "https://hub.example.com", ClusterID: "c-1", Secret: NewSecret()}
	token, err := EncodeJoinToken(want)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, JoinTokenPrefix) {
		t.Fatalf("token %q lacks prefix", token)
	}
	got, err := DecodeJoinToken("  " + token + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}

func TestJoinTokenRejectsBadInput(t *testing.T) {
	encode := func(payload string) string {
		return JoinTokenPrefix + base64.RawURLEncoding.EncodeToString([]byte(payload))
	}
	for name, token := range map[string]string{
		"empty":         "",
		"bad prefix":    "tpc2_" + strings.TrimPrefix(encode(`{"v":1,"u":"https://h","c":"c","s":"s"}`), JoinTokenPrefix),
		"prefix only":   JoinTokenPrefix,
		"bad base64":    JoinTokenPrefix + "!!!",
		"bad json":      encode("not json"),
		"wrong version": encode(`{"v":2,"u":"https://h","c":"c","s":"s"}`),
		"no version":    encode(`{"u":"https://h","c":"c","s":"s"}`),
		"empty url":     encode(`{"v":1,"u":"","c":"c","s":"s"}`),
		"empty cluster": encode(`{"v":1,"u":"https://h","c":"","s":"s"}`),
		"empty secret":  encode(`{"v":1,"u":"https://h","c":"c","s":""}`),
	} {
		if _, err := DecodeJoinToken(token); !errors.Is(err, ErrJoinTokenInvalid) {
			t.Errorf("%s: got %v, want ErrJoinTokenInvalid", name, err)
		}
	}
	if _, err := EncodeJoinToken(JoinToken{Version: 1, HubURL: "https://h", ClusterID: "c"}); !errors.Is(err, ErrJoinTokenInvalid) {
		t.Errorf("encode without secret: got %v", err)
	}
	if _, err := EncodeJoinToken(JoinToken{Version: 2, HubURL: "https://h", ClusterID: "c", Secret: "s"}); !errors.Is(err, ErrJoinTokenInvalid) {
		t.Errorf("encode with version 2: got %v", err)
	}
}

func TestSecretAndHash(t *testing.T) {
	a, b := NewSecret(), NewSecret()
	if a == b {
		t.Fatal("secrets repeat")
	}
	raw, err := base64.RawURLEncoding.DecodeString(a)
	if err != nil || len(raw) != 32 || strings.ContainsAny(a, "=+/") {
		t.Fatalf("secret %q is not 32 bytes of unpadded base64url", a)
	}
	sum := sha256.Sum256([]byte(a))
	if got := HashSecret(a); got != hex.EncodeToString(sum[:]) || got != strings.ToLower(got) {
		t.Fatalf("HashSecret = %q", got)
	}
}

func TestNormalizeHubURL(t *testing.T) {
	for _, tc := range []struct {
		raw, want, code string
		insecure        bool
	}{
		{raw: " https://Hub.Example.com/ ", want: "https://hub.example.com"},
		{raw: "https://hub.example.com:8443", want: "https://hub.example.com:8443"},
		{raw: "http://127.0.0.1:8080", insecure: true, want: "http://127.0.0.1:8080"},
		{raw: "http://localhost:8080", insecure: true, want: "http://localhost:8080"},
		{raw: "http://192.168.1.4", insecure: true, want: "http://192.168.1.4"},
		{raw: "http://10.0.0.1", insecure: true, want: "http://10.0.0.1"},
		{raw: "http://100.100.1.1", insecure: true, want: "http://100.100.1.1"},
		{raw: "http://169.254.3.3", insecure: true, want: "http://169.254.3.3"},
		{raw: "http://[fd00::1]:80", insecure: true, want: "http://[fd00::1]:80"},
		{raw: "http://[fe80::1%25en0]:80", insecure: true, want: "http://[fe80::1%25en0]:80"},
		{raw: "http://127.0.0.1:8080", code: CodeInsecureHubURL},
		{raw: "http://8.8.8.8", insecure: true, code: CodeInsecureHubURL},
		{raw: "http://hub.lan", insecure: true, code: CodeInsecureHubURL},
		{raw: "http://100.128.0.1", insecure: true, code: CodeInsecureHubURL},
		{raw: "ftp://hub", code: CodeInvalidRequest},
		{raw: "hub.example.com", code: CodeInvalidRequest},
		{raw: "https://user:pw@hub.example.com", code: CodeInvalidRequest},
		{raw: "https://hub.example.com/tproxy", code: CodeInvalidRequest},
		{raw: "https://hub.example.com/?a=1", code: CodeInvalidRequest},
		{raw: "https://hub.example.com/#x", code: CodeInvalidRequest},
		{raw: "", code: CodeInvalidRequest},
	} {
		got, err := NormalizeHubURL(tc.raw, tc.insecure)
		if tc.code != "" {
			var clusterErr *Error
			if !errors.As(err, &clusterErr) || clusterErr.Code != tc.code {
				t.Errorf("%q: got %q, %v; want %s", tc.raw, got, err, tc.code)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%q: got %q, %v; want %q", tc.raw, got, err, tc.want)
		}
	}
}
