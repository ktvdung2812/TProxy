package qoder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const catalogTTL = time.Hour

type ModelConfig map[string]any

type catalogEntry struct {
	expiresAt  time.Time
	rawConfigs map[string]ModelConfig
}

var (
	catalogMu sync.Mutex
	catalogs  = map[string]*catalogEntry{}
	inflight  = map[string]*sync.WaitGroup{}
)

func cacheKey(userID, accessToken string) string {
	seed := userID
	if seed == "" {
		seed = accessToken
	}
	sum := sha256.Sum256([]byte("qoder:" + seed))
	return hex.EncodeToString(sum[:])
}

// GetModelConfig returns the live model_config block for a model key.
func GetModelConfig(ctx context.Context, client *http.Client, creds CosyCreds, modelKey string) (ModelConfig, error) {
	entry, err := resolveCatalog(ctx, client, creds, false)
	if err != nil {
		return nil, err
	}
	if cfg, ok := entry.rawConfigs[modelKey]; ok {
		out := cloneConfig(cfg)
		out["key"] = modelKey
		return out, nil
	}
	entry, err = resolveCatalog(ctx, client, creds, true)
	if err != nil {
		return nil, err
	}
	if cfg, ok := entry.rawConfigs[modelKey]; ok {
		out := cloneConfig(cfg)
		out["key"] = modelKey
		return out, nil
	}
	return nil, fmt.Errorf("qoder: model_config for %q not found", modelKey)
}

func resolveCatalog(ctx context.Context, client *http.Client, creds CosyCreds, force bool) (*catalogEntry, error) {
	if creds.UserID == "" || creds.AuthToken == "" {
		return nil, fmt.Errorf("qoder: credential missing user id or access token")
	}
	key := cacheKey(creds.UserID, creds.AuthToken)
	catalogMu.Lock()
	if !force {
		if cached := catalogs[key]; cached != nil && time.Now().Before(cached.expiresAt) {
			catalogMu.Unlock()
			return cached, nil
		}
	}
	if !force {
		if wg := inflight[key]; wg != nil {
			catalogMu.Unlock()
			wg.Wait()
			catalogMu.Lock()
			cached := catalogs[key]
			catalogMu.Unlock()
			if cached != nil {
				return cached, nil
			}
			return nil, fmt.Errorf("qoder: model catalog fetch failed")
		}
	}
	wg := &sync.WaitGroup{}
	wg.Add(1)
	inflight[key] = wg
	catalogMu.Unlock()

	entry, fetchErr := fetchCatalog(ctx, client, creds)
	catalogMu.Lock()
	delete(inflight, key)
	wg.Done()
	if fetchErr == nil {
		catalogs[key] = entry
	}
	catalogMu.Unlock()
	if fetchErr != nil {
		return nil, fetchErr
	}
	return entry, nil
}

func fetchCatalog(ctx context.Context, client *http.Client, creds CosyCreds) (*catalogEntry, error) {
	headers, err := BuildCosyHeaders(nil, ModelListURL, creds)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ModelListURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("qoder model list HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	chat, _ := payload["chat"].([]any)
	rawConfigs := map[string]ModelConfig{}
	for _, raw := range chat {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		key := stringValue(item["key"])
		if key == "" {
			continue
		}
		rawConfigs[key] = item
	}
	if len(rawConfigs) == 0 {
		return nil, fmt.Errorf("qoder: empty model catalog")
	}
	return &catalogEntry{expiresAt: time.Now().Add(catalogTTL), rawConfigs: rawConfigs}, nil
}

func cloneConfig(cfg ModelConfig) ModelConfig {
	out := ModelConfig{}
	for k, v := range cfg {
		out[k] = v
	}
	return out
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
