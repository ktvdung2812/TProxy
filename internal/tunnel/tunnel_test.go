package tunnel

import "testing"

func TestPublicURL(t *testing.T) {
	t.Setenv("TPROXY_TUNNEL_PUBLIC_HOST", "example-tunnel.test")
	if got := PublicURL("abc123"); got != "https://rabc123.example-tunnel.test" {
		t.Fatalf("PublicURL() = %q", got)
	}
}

func TestGenerateShortID(t *testing.T) {
	id := GenerateShortID()
	if len(id) != 6 {
		t.Fatalf("short id length = %d", len(id))
	}
}

func TestSaveAndLoadState(t *testing.T) {
	dir := t.TempDir()
	layout := NewDataLayout(dir)
	state := State{ShortID: "abc123", TunnelURL: "https://foo.trycloudflare.com"}
	if err := SaveState(layout.StateFile, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadState(layout.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ShortID != state.ShortID || loaded.TunnelURL != state.TunnelURL {
		t.Fatalf("loaded = %+v", loaded)
	}
}
