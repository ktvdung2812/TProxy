package tunnel

import (
	"os"
	"path/filepath"
)

// DataLayout resolves on-disk paths for tunnel state and binaries.
type DataLayout struct {
	Root           string
	BinDir         string
	Cloudflared    string
	TunnelDir      string
	StateFile      string
	CloudflaredPID string
}

func NewDataLayout(dataRoot string) DataLayout {
	root := filepath.Clean(dataRoot)
	tunnelDir := filepath.Join(root, "tunnel")
	binName := "cloudflared"
	if os.PathSeparator == '\\' {
		binName += ".exe"
	}
	return DataLayout{
		Root:           root,
		BinDir:         filepath.Join(root, "bin"),
		Cloudflared:    filepath.Join(root, "bin", binName),
		TunnelDir:      tunnelDir,
		StateFile:      filepath.Join(tunnelDir, "state.json"),
		CloudflaredPID: filepath.Join(tunnelDir, "cloudflared.pid"),
	}
}

func DataLayoutFromDatabasePath(databasePath string) DataLayout {
	return NewDataLayout(filepath.Dir(databasePath))
}
