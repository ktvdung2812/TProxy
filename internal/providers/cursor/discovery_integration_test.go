package cursor

import (
	"context"
	"os"
	"testing"

	"github.com/tproxy/tproxy/internal/auth"
)

func TestDiscoverModelsLive(t *testing.T) {
	if os.Getenv("CURSOR_LIVE_TEST") != "1" {
		t.Skip("set CURSOR_LIVE_TEST=1 to run live cursor discovery test")
	}
	result := auth.AutoImportCursor()
	if !result.Found {
		t.Fatalf("auto import failed: %v", result.Err)
	}
	entries, err := DiscoverModels(context.Background(), nil, "", CatalogCredentials{
		AccessToken: result.Tokens.AccessToken,
		MachineID:   result.Tokens.MachineID,
		GhostMode:   false,
	})
	if err != nil {
		t.Fatalf("DiscoverModels() error: %v", err)
	}
	t.Logf("discovered %d models", len(entries))
	if len(entries) <= len(StaticCursorModels) {
		t.Fatalf("expected more than static fallback (%d), got %d", len(StaticCursorModels), len(entries))
	}
}
