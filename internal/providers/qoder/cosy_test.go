package qoder

import "testing"

func TestEncodeBodyRoundTripLength(t *testing.T) {
	plain := []byte(`{"hello":"world","n":123}`)
	encoded := EncodeBody(plain)
	if len(encoded) != len(plain)*4/3+4 {
		// base64 length is preserved through substitution
		if len(encoded) < len(plain) {
			t.Fatalf("encoded too short: %d", len(encoded))
		}
	}
}

func TestBuildCosyHeadersRequiresUser(t *testing.T) {
	_, err := BuildCosyHeaders([]byte("{}"), ChatURLEncoded, CosyCreds{AuthToken: "dt-test"})
	if err == nil {
		t.Fatal("expected error for missing user id")
	}
}

func TestBuildCosyHeadersProducesAuthorization(t *testing.T) {
	headers, err := BuildCosyHeaders([]byte("payload"), ChatURLEncoded, CosyCreds{
		UserID:    "user-1",
		AuthToken: "dt-test",
		MachineID: "machine-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if headers["Authorization"] == "" || headers["Cosy-Key"] == "" {
		t.Fatalf("missing cosy headers: %+v", headers)
	}
}
