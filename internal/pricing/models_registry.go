package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tproxy/tproxy/internal/store"
)

const defaultModelsJSONURL = "https://models.dev/models.json"

type ModelsRegistry struct {
	mu         sync.RWMutex
	full       map[string]struct{}
	bare       map[string]struct{}
	names      map[string]string
	apiURL     string
	cachePath  string
	httpClient *http.Client
}

type ModelsRegistryOptions struct {
	APIURL     string
	CachePath  string
	HTTPClient *http.Client
}

func NewModelsRegistry(opts ModelsRegistryOptions) *ModelsRegistry {
	apiURL := strings.TrimSpace(opts.APIURL)
	if apiURL == "" {
		apiURL = defaultModelsJSONURL
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &ModelsRegistry{
		full:       map[string]struct{}{},
		bare:       map[string]struct{}{},
		names:      map[string]string{},
		apiURL:     apiURL,
		cachePath:  opts.CachePath,
		httpClient: client,
	}
}

func DefaultModelsRegistryCachePath(databaseDSN string) string {
	if strings.TrimSpace(databaseDSN) == "" {
		return filepath.Join(".cache", "models.dev-catalog.json")
	}
	return filepath.Join(filepath.Dir(databaseDSN), "models.dev-catalog.json")
}

func (r *ModelsRegistry) Start(ctx context.Context, refreshInterval time.Duration) {
	if refreshInterval <= 0 {
		refreshInterval = time.Hour
	}
	r.refresh(ctx)
	go func() {
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.refresh(ctx)
			}
		}
	}()
}

func (r *ModelsRegistry) FilteringEnabled() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.full) > 0 || len(r.bare) > 0
}

func (r *ModelsRegistry) KnownModelRef(model string) bool {
	if !r.FilteringEnabled() {
		return true
	}
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return false
	}
	if r.hasFull(model) {
		return true
	}
	for _, key := range modelLookupKeys(model) {
		if r.hasBare(key) {
			return true
		}
	}
	return false
}

func (r *ModelsRegistry) ModelDisplayName(model string) (string, bool) {
	if !r.FilteringEnabled() {
		return "", false
	}
	for _, key := range modelLookupKeys(model) {
		if name, ok := r.lookupName(key); ok {
			return name, true
		}
	}
	return "", false
}

func (r *ModelsRegistry) lookupName(key string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	name, ok := r.names[key]
	return name, ok && strings.TrimSpace(name) != ""
}

func (r *ModelsRegistry) KnownRoute(provider store.Provider, upstream string) bool {
	if !r.FilteringEnabled() {
		return true
	}
	for _, providerKey := range modelsDevProvidersFor(provider) {
		for _, upstreamKey := range modelLookupKeys(upstream) {
			if r.hasFull(providerKey + "/" + upstreamKey) {
				return true
			}
		}
	}
	return r.KnownModelRef(upstream)
}

func (r *ModelsRegistry) hasFull(value string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.full[value]
	return ok
}

func (r *ModelsRegistry) hasBare(value string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.bare[value]
	return ok
}

func (r *ModelsRegistry) refresh(ctx context.Context) {
	payload, err := r.fetchPayload(ctx)
	if err != nil {
		log.Printf("models.dev catalog refresh failed: %v", err)
		return
	}
	full, bare, names := buildModelsRegistryIndex(payload)
	r.mu.Lock()
	r.full = full
	r.bare = bare
	r.names = names
	r.mu.Unlock()
}

func (r *ModelsRegistry) fetchPayload(ctx context.Context) (map[string]any, error) {
	if payload, err := r.readCache(); err == nil && len(payload) > 0 {
		go func() {
			remote, err := r.fetchRemote(context.Background())
			if err != nil {
				return
			}
			_ = r.writeCache(remote)
			full, bare, names := buildModelsRegistryIndex(remote)
			r.mu.Lock()
			r.full = full
			r.bare = bare
			r.names = names
			r.mu.Unlock()
		}()
		return payload, nil
	}
	remote, err := r.fetchRemote(ctx)
	if err != nil {
		if payload, cacheErr := r.readCache(); cacheErr == nil && len(payload) > 0 {
			return payload, nil
		}
		return nil, err
	}
	_ = r.writeCache(remote)
	return remote, nil
}

func (r *ModelsRegistry) fetchRemote(ctx context.Context) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "tproxy/1.0")
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("models.dev returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (r *ModelsRegistry) readCache() (map[string]any, error) {
	if strings.TrimSpace(r.cachePath) == "" {
		return nil, os.ErrNotExist
	}
	body, err := os.ReadFile(r.cachePath)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (r *ModelsRegistry) writeCache(payload map[string]any) error {
	if strings.TrimSpace(r.cachePath) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.cachePath), 0o755); err != nil {
		return err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return os.WriteFile(r.cachePath, encoded, 0o644)
}

func buildModelsRegistryIndex(payload map[string]any) (map[string]struct{}, map[string]struct{}, map[string]string) {
	full := map[string]struct{}{}
	bare := map[string]struct{}{}
	names := map[string]string{}
	addFull := func(value string, displayName string) {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			return
		}
		full[value] = struct{}{}
		if idx := strings.LastIndex(value, "/"); idx >= 0 && idx < len(value)-1 {
			bareKey := value[idx+1:]
			bare[bareKey] = struct{}{}
			if strings.TrimSpace(displayName) != "" {
				names[bareKey] = strings.TrimSpace(displayName)
				names[value] = strings.TrimSpace(displayName)
			}
		} else if strings.TrimSpace(displayName) != "" {
			names[value] = strings.TrimSpace(displayName)
		}
	}
	for key, raw := range payload {
		entry, ok := raw.(map[string]any)
		displayName := ""
		if ok {
			if name, ok := entry["name"].(string); ok {
				displayName = name
			}
		}
		addFull(key, displayName)
		if !ok {
			continue
		}
		if id, ok := entry["id"].(string); ok {
			addFull(id, displayName)
		}
	}
	return full, bare, names
}

// BuildModelsRegistryIndexForTest exposes catalog index building for other packages' tests.
func BuildModelsRegistryIndexForTest(payload map[string]any) (map[string]struct{}, map[string]struct{}, map[string]string) {
	return buildModelsRegistryIndex(payload)
}

// SetIndexForTest seeds registry indexes in unit tests.
func (r *ModelsRegistry) SetIndexForTest(full, bare map[string]struct{}, names map[string]string) {
	r.mu.Lock()
	r.full = full
	r.bare = bare
	r.names = names
	r.mu.Unlock()
}
