package cursor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	AvailableModelsPath = "/aiserver.v1.AiService/AvailableModels"
)

type CatalogCredentials struct {
	AccessToken string
	MachineID   string
	GhostMode   bool
}

// DiscoverModels fetches the live Cursor model catalog for a credential.
func DiscoverModels(ctx context.Context, client *http.Client, baseURL string, creds CatalogCredentials) ([]DiscoveredModelEntry, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if creds.AccessToken == "" || creds.MachineID == "" {
		return nil, fmt.Errorf("cursor: credential missing access token or machine ID")
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = BaseURL
	}
	target := strings.TrimRight(baseURL, "/") + AvailableModelsPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, http.NoBody)
	if err != nil {
		return nil, err
	}
	headers := BuildCursorHeaders(creds.AccessToken, &creds.MachineID, creds.GhostMode)
	for key, value := range headers {
		if strings.EqualFold(key, "content-type") {
			continue
		}
		req.Header.Set(key, value)
	}
	req.Header.Set("Content-Type", "application/proto")
	req.Header.Set("Accept", "application/proto")
	req.Header.Set("Connect-Protocol-Version", "1")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("cursor available models HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	models := DecodeAvailableModelsResponse(body)
	if len(models) == 0 {
		return nil, fmt.Errorf("cursor: empty available models response")
	}
	entries := modelsFromParameterizedMetadata(models)
	if len(entries) == 0 {
		return nil, fmt.Errorf("cursor: no models decoded from available models response")
	}
	return entries, nil
}

// StaticFallbackModels returns the built-in fallback catalog.
func StaticFallbackModels() []DiscoveredModelEntry {
	items := make([]DiscoveredModelEntry, 0, len(StaticCursorModels))
	for _, model := range StaticCursorModels {
		items = append(items, DiscoveredModelEntry{
			ID:                model.ID,
			Name:              model.Name,
			SupportsReasoning: supportsReasoningModelID(model.ID),
		})
	}
	return items
}
