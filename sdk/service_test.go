package sdk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/tproxy/tproxy/internal/config"
)

func TestBuilderCreatesHandler(t *testing.T) {
	cfg := &config.Config{Server: config.ServerConfig{Host: "127.0.0.1", Port: 0}, Database: config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "sdk.db")}}
	service, err := NewBuilder().WithConfig(cfg).Build()
	if err != nil {
		t.Fatal(err)
	}
	defer service.Shutdown(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
}
