package tunnel

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
)

const shortIDChars = "abcdefghijklmnpqrstuvwxyz23456789"

type State struct {
	ShortID   string `json:"shortId"`
	TunnelURL string `json:"tunnelUrl"`
}

func LoadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func SaveState(path string, state State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func GenerateShortID() string {
	buf := make([]byte, 6)
	for i := range buf {
		buf[i] = shortIDChars[rand.Intn(len(shortIDChars))]
	}
	return string(buf)
}
