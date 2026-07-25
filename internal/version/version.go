package version

import (
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	NPMPackageName = "@ktvdung1606/tproxy"
	registryURL    = "https://registry.npmjs.org/" + NPMPackageName + "/latest"
	cacheTTL       = time.Hour
)

//go:embed current.txt
var currentBytes []byte

var cache struct {
	mu        sync.Mutex
	latest    string
	fetchedAt time.Time
}

// Info describes the running build and optional npm update metadata.
type Info struct {
	CurrentVersion      string `json:"current_version"`
	LatestVersion       string `json:"latest_version,omitempty"`
	HasUpdate           bool   `json:"has_update"`
	InstallCommand      string `json:"install_command"`
	SourceUpdateCommand string `json:"source_update_command"`
	ReleaseURL          string `json:"release_url"`
}

// Current returns the embedded build version.
func Current() string {
	if value := strings.TrimSpace(string(currentBytes)); value != "" {
		return value
	}
	return "0.0.0"
}

// Compare returns 1 if a > b, -1 if a < b, 0 if equal (semver major.minor.patch).
func Compare(a, b string) int {
	pa := parseParts(a)
	pb := parseParts(b)
	for i := 0; i < 3; i++ {
		if pa[i] > pb[i] {
			return 1
		}
		if pa[i] < pb[i] {
			return -1
		}
	}
	return 0
}

func parseParts(value string) [3]int {
	parts := [3]int{}
	for i, piece := range strings.Split(strings.TrimSpace(value), ".") {
		if i >= 3 {
			break
		}
		var n int
		for _, ch := range piece {
			if ch < '0' || ch > '9' {
				break
			}
			n = n*10 + int(ch-'0')
		}
		parts[i] = n
	}
	return parts
}

// Check resolves update metadata, optionally querying npm when checkRemote is true.
func Check(ctx context.Context, checkRemote bool) Info {
	current := Current()
	info := Info{
		CurrentVersion:      current,
		InstallCommand:      "npm i -g " + NPMPackageName + "@latest --prefer-online",
		SourceUpdateCommand: "git pull && npm run build && npm start",
		ReleaseURL:          "https://github.com/ktvdung2812/TProxy/releases/latest",
	}
	if !checkRemote {
		return info
	}
	latest, err := latestCached(ctx)
	if err != nil || latest == "" {
		return info
	}
	info.LatestVersion = latest
	info.HasUpdate = Compare(latest, current) > 0
	return info
}

func latestCached(ctx context.Context) (string, error) {
	cache.mu.Lock()
	if cache.latest != "" && time.Since(cache.fetchedAt) < cacheTTL {
		latest := cache.latest
		cache.mu.Unlock()
		return latest, nil
	}
	cache.mu.Unlock()

	latest, err := fetchLatest(ctx)
	if err != nil {
		return "", err
	}
	if latest != "" {
		cache.mu.Lock()
		cache.latest = latest
		cache.fetchedAt = time.Now()
		cache.mu.Unlock()
	}
	return latest, nil
}

func fetchLatest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, registryURL, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var payload struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	return strings.TrimSpace(payload.Version), nil
}
