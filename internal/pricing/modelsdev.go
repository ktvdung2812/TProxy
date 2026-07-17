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

const defaultModelsDevURL = "https://models.dev/api.json"

var canonicalProviderOrder = []string{
	"openai",
	"anthropic",
	"github-copilot",
	"google",
	"deepseek",
	"groq",
	"mistral",
	"xai",
	"openrouter",
}

var providerTypeAliases = map[string][]string{
	"openai-codex":        {"openai", "github-copilot"},
	"codex":               {"openai", "github-copilot"},
	"openai-compatible":   {"openai", "github-copilot"},
	"openai":              {"openai"},
	"anthropic":           {"anthropic"},
	"google":              {"google"},
	"gemini":              {"google"},
	"github-copilot":      {"github-copilot"},
	"copilot":             {"github-copilot"},
	"deepseek":            {"deepseek"},
	"groq":                {"groq"},
	"mistral":             {"mistral"},
	"xai":                 {"xai"},
	"ollama":              {"ollama"},
}

type modelEntry struct {
	Provider string
	ModelID  string
	Rates    Rates
}

type Catalog struct {
	mu         sync.RWMutex
	byModel    map[string][]modelEntry
	apiURL     string
	cachePath  string
	httpClient *http.Client
}

type Options struct {
	APIURL     string
	CachePath  string
	HTTPClient *http.Client
}

func NewCatalog(opts Options) *Catalog {
	apiURL := strings.TrimSpace(opts.APIURL)
	if apiURL == "" {
		apiURL = defaultModelsDevURL
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Catalog{
		byModel:    map[string][]modelEntry{},
		apiURL:     apiURL,
		cachePath:  opts.CachePath,
		httpClient: client,
	}
}

func DefaultCachePath(databaseDSN string) string {
	if strings.TrimSpace(databaseDSN) == "" {
		return filepath.Join(".cache", "models.dev.json")
	}
	return filepath.Join(filepath.Dir(databaseDSN), "models.dev.json")
}

func (c *Catalog) Start(ctx context.Context, refreshInterval time.Duration) {
	if refreshInterval <= 0 {
		refreshInterval = time.Hour
	}
	c.refresh(ctx)
	go func() {
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.refresh(ctx)
			}
		}
	}()
}

func (c *Catalog) refresh(ctx context.Context) {
	payload, err := c.fetchPayload(ctx)
	if err != nil {
		log.Printf("models.dev pricing refresh failed: %v", err)
		return
	}
	index := buildIndex(payload)
	c.mu.Lock()
	c.byModel = index
	c.mu.Unlock()
}

func (c *Catalog) fetchPayload(ctx context.Context) (map[string]any, error) {
	if payload, err := c.readCache(); err == nil && len(payload) > 0 {
		go func() {
			remote, err := c.fetchRemote(context.Background())
			if err != nil {
				return
			}
			_ = c.writeCache(remote)
			c.mu.Lock()
			c.byModel = buildIndex(remote)
			c.mu.Unlock()
		}()
		return payload, nil
	}
	remote, err := c.fetchRemote(ctx)
	if err != nil {
		if payload, cacheErr := c.readCache(); cacheErr == nil && len(payload) > 0 {
			return payload, nil
		}
		return nil, err
	}
	_ = c.writeCache(remote)
	return remote, nil
}

func (c *Catalog) fetchRemote(ctx context.Context) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "tproxy/1.0")
	resp, err := c.httpClient.Do(req)
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

func (c *Catalog) readCache() (map[string]any, error) {
	if strings.TrimSpace(c.cachePath) == "" {
		return nil, os.ErrNotExist
	}
	body, err := os.ReadFile(c.cachePath)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (c *Catalog) writeCache(payload map[string]any) error {
	if strings.TrimSpace(c.cachePath) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.cachePath), 0o755); err != nil {
		return err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return os.WriteFile(c.cachePath, encoded, 0o644)
}

func buildIndex(payload map[string]any) map[string][]modelEntry {
	index := map[string][]modelEntry{}
	for providerKey, providerValue := range payload {
		providerMap, ok := providerValue.(map[string]any)
		if !ok {
			continue
		}
		providerID := providerKey
		if raw, ok := providerMap["id"].(string); ok && strings.TrimSpace(raw) != "" {
			providerID = raw
		}
		modelsValue, ok := providerMap["models"].(map[string]any)
		if !ok {
			continue
		}
		for modelKey, modelValue := range modelsValue {
			modelMap, ok := modelValue.(map[string]any)
			if !ok {
				continue
			}
			modelID := modelKey
			if raw, ok := modelMap["id"].(string); ok && strings.TrimSpace(raw) != "" {
				modelID = raw
			}
			rates, ok := parseRates(modelMap["cost"])
			if !ok {
				continue
			}
			entry := modelEntry{Provider: providerID, ModelID: modelID, Rates: rates}
			for _, key := range modelLookupKeys(modelID) {
				index[key] = append(index[key], entry)
			}
		}
	}
	return index
}

func parseRates(value any) (Rates, bool) {
	costMap, ok := value.(map[string]any)
	if !ok {
		return Rates{}, false
	}
	rates := Rates{
		InputPerMillion:     asFloat(costMap["input"]),
		OutputPerMillion:    asFloat(costMap["output"]),
		ReasoningPerMillion: asFloat(costMap["reasoning"]),
		CacheReadPerMillion: asFloat(costMap["cache_read"]),
		RequestUSD:          asFloat(costMap["request"]),
	}
	if rates.InputPerMillion <= 0 && rates.OutputPerMillion <= 0 && rates.ReasoningPerMillion <= 0 {
		return Rates{}, false
	}
	return rates, true
}

func asFloat(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	default:
		return 0
	}
}

func (c *Catalog) Lookup(provider store.Provider, upstreamModel string) (Rates, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	preferred := modelsDevProvidersFor(provider)
	for _, key := range modelLookupKeys(upstreamModel) {
		candidates := c.byModel[key]
		if len(candidates) == 0 {
			continue
		}
		if entry, ok := pickEntry(candidates, preferred); ok {
			return entry.Rates, true
		}
	}
	return Rates{}, false
}

func pickEntry(candidates []modelEntry, preferred []string) (modelEntry, bool) {
	if len(candidates) == 0 {
		return modelEntry{}, false
	}
	preferredSet := map[string]int{}
	for index, provider := range preferred {
		preferredSet[strings.ToLower(provider)] = index
	}
	best := -1
	var chosen modelEntry
	for _, candidate := range candidates {
		rank, ok := preferredSet[strings.ToLower(candidate.Provider)]
		if !ok {
			rank = canonicalRank(candidate.Provider) + 100
		}
		if best == -1 || rank < best {
			best = rank
			chosen = candidate
		}
	}
	return chosen, best >= 0
}

func canonicalRank(provider string) int {
	provider = strings.ToLower(provider)
	for index, candidate := range canonicalProviderOrder {
		if candidate == provider {
			return index
		}
	}
	return len(canonicalProviderOrder) + 1
}

func modelsDevProvidersFor(provider store.Provider) []string {
	keys := make([]string, 0, 6)
	add := func(value string) {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" {
			return
		}
		for _, existing := range keys {
			if existing == value {
				return
			}
		}
		keys = append(keys, value)
	}
	for _, alias := range providerTypeAliases[strings.ToLower(provider.Type)] {
		add(alias)
	}
	add(provider.Type)
	add(provider.ID)
	return keys
}
