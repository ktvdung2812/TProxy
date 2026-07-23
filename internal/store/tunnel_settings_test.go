package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestTunnelSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "tproxy.db")
	store, err := OpenSQLite(dbPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	settings := TunnelSettings{
		Enabled:               true,
		TunnelURL:             "https://foo.trycloudflare.com",
		TailscaleEnabled:      true,
		TailscaleURL:          "https://device.ts.net",
		TunnelDashboardAccess: false,
	}
	if err := store.SaveTunnelSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.TunnelSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != settings {
		t.Fatalf("loaded = %+v want %+v", loaded, settings)
	}
}
