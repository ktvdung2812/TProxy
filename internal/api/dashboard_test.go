package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardReturnsNotFoundForMissingAsset(t *testing.T) {
	handler := dashboardHandler()
	request := httptest.NewRequest(http.MethodGet, "/dashboard/missing.webmanifest", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("missing asset returned HTML content type: %q", recorder.Header().Get("Content-Type"))
	}
}

func TestDashboardServesManifestAsJSON(t *testing.T) {
	handler := dashboardHandler()
	request := httptest.NewRequest(http.MethodGet, "/dashboard/manifest.webmanifest", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Header().Get("Content-Type"), "application/manifest+json") {
		t.Fatalf("content type=%q", recorder.Header().Get("Content-Type"))
	}
	if !strings.HasPrefix(strings.TrimSpace(recorder.Body.String()), "{") {
		t.Fatalf("manifest body is not JSON: %q", recorder.Body.String())
	}
}
