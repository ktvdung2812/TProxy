package store

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/tproxy/tproxy/internal/security"
	"path/filepath"
	"testing"
)

func TestTunnelSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "tproxy.db")
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenSQLite(dbPath, encryptor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	settings := TunnelSettings{
		Enabled:               true,
		TunnelURL:             "https://api.example.com",
		TunnelHostname:        "api.example.com",
		TunnelToken:           "private-tunnel-token",
		TailscaleEnabled:      true,
		TailscaleURL:          "https://device.ts.net",
		TunnelDashboardAccess: false,
	}
	if err := store.SaveTunnelSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	raw, err := store.GetAppSettingJSON(ctx, AppSettingTunnel)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(raw)
	if strings.Contains(string(encoded), settings.TunnelToken) || raw["tunnel_token_ciphertext"] == "" {
		t.Fatal("tunnel token must be encrypted at rest")
	}
	encoded, _ = json.Marshal(settings)
	if strings.Contains(string(encoded), settings.TunnelToken) {
		t.Fatal("settings serialization must omit the token")
	}
	loaded, err := store.TunnelSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != settings {
		t.Fatalf("loaded = %+v want %+v", loaded, settings)
	}
}

func TestTunnelAccessPolicyDoesNotDecryptToken(t *testing.T) {
	key, err := security.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	dataStore, err := OpenSQLite(filepath.Join(t.TempDir(), "tunnel-policy.db"), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })

	ctx := context.Background()
	settings := TunnelSettings{
		Enabled:               true,
		TunnelURL:             "https://api.example.com",
		TunnelHostname:        "api.example.com",
		TailscaleEnabled:      true,
		TailscaleURL:          "https://device.ts.net",
		TunnelDashboardAccess: false,
	}
	if err := dataStore.SaveTunnelSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	raw, err := dataStore.GetAppSettingJSON(ctx, AppSettingTunnel)
	if err != nil {
		t.Fatal(err)
	}
	raw["tunnel_token_ciphertext"] = "corrupt-ciphertext"
	if err := dataStore.SetAppSettingJSON(ctx, AppSettingTunnel, raw); err != nil {
		t.Fatal(err)
	}

	policy, err := dataStore.TunnelAccessPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := TunnelAccessPolicy{
		TunnelURL:             settings.TunnelURL,
		TunnelHostname:        settings.TunnelHostname,
		TailscaleURL:          settings.TailscaleURL,
		TunnelDashboardAccess: settings.TunnelDashboardAccess,
	}
	if policy != want {
		t.Fatalf("TunnelAccessPolicy = %+v, want %+v", policy, want)
	}
	if _, err := dataStore.TunnelSettings(ctx); err == nil || !strings.Contains(err.Error(), "decrypt Cloudflare Tunnel token") {
		t.Fatalf("TunnelSettings error = %v, want token decryption error", err)
	}
}
