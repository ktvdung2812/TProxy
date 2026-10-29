package api

import (
	"net/http/httptest"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/config"
)

func TestParentSessionIDFromHeader(t *testing.T) {
	request := httptest.NewRequest("POST", "/v1/messages", nil)
	request.Header.Set(config.ParentSessionHeader, "parent-from-header")

	if got := parentSessionIDFromRequest(request); got != "parent-from-header" {
		t.Fatalf("parent = %q", got)
	}
}

func TestParentSessionIDFromMetadata(t *testing.T) {
	request := httptest.NewRequest("POST", "/v1/messages", nil)
	body := map[string]any{
		"metadata": map[string]any{config.ParentSessionMetadataKey: "parent-from-metadata"},
	}

	if got := parentSessionIDFromRequest(request, body); got != "parent-from-metadata" {
		t.Fatalf("parent = %q", got)
	}
}

func TestParentSessionIDHeaderBeatsMetadata(t *testing.T) {
	request := httptest.NewRequest("POST", "/v1/messages", nil)
	request.Header.Set(config.ParentSessionHeader, "parent-from-header")
	body := map[string]any{
		"metadata": map[string]any{config.ParentSessionMetadataKey: "parent-from-metadata"},
	}

	if got := parentSessionIDFromRequest(request, body); got != "parent-from-header" {
		t.Fatalf("parent = %q, want the header value", got)
	}
}

func TestAttachParentSessionIDRecordsMetadata(t *testing.T) {
	request := httptest.NewRequest("POST", "/v1/messages", nil)
	request.Header.Set(config.ParentSessionHeader, "parent-session")
	canonicalRequest := canonical.Request{SessionID: "child-session", Raw: map[string]any{}}

	attachParentSessionID(request, &canonicalRequest)

	if got := canonicalRequest.Metadata[config.ParentSessionMetadataKey]; got != "parent-session" {
		t.Fatalf("parent metadata = %#v", got)
	}
}

func TestAttachParentSessionIDSkipsSelfReference(t *testing.T) {
	request := httptest.NewRequest("POST", "/v1/messages", nil)
	request.Header.Set(config.ParentSessionHeader, "same-session")
	canonicalRequest := canonical.Request{SessionID: "same-session", Raw: map[string]any{}}

	attachParentSessionID(request, &canonicalRequest)

	if _, exists := canonicalRequest.Metadata[config.ParentSessionMetadataKey]; exists {
		t.Fatal("a session must not be its own parent")
	}
}
