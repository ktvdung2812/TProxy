package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type TopologyModelUsage struct {
	Provider     string `json:"provider"`
	CredentialID string `json:"credential_id"`
	AccountLabel string `json:"account_label"`
	Model        string `json:"model"`
	RequestCount int    `json:"request_count"`
	LastUsedAt   string `json:"last_used_at"`
}

type TopologyClient struct {
	ClientKeyID   string               `json:"client_key_id"`
	ClientLabel   string               `json:"client_label"`
	TotalRequests int                  `json:"total_requests"`
	TodayRequests int                  `json:"today_requests"`
	LastSeenAt    string               `json:"last_seen_at"`
	FirstSeenAt   string               `json:"first_seen_at"`
	Providers     []string             `json:"providers"`
	ModelUsage    []TopologyModelUsage `json:"model_usage"`
}

type TopologyClientSummary struct {
	TotalRequests int    `json:"total_requests"`
	TodayRequests int    `json:"today_requests"`
	LastSeenAt    string `json:"last_seen_at"`
	FirstSeenAt   string `json:"first_seen_at"`
}

type TopologyClientModelRow struct {
	Model        string `json:"model"`
	Provider     string `json:"provider"`
	CredentialID string `json:"credential_id"`
	AccountLabel string `json:"account_label"`
	RequestCount int    `json:"request_count"`
	LastUsedAt   string `json:"last_used_at"`
}

type TopologyClientDetail struct {
	ClientKeyID string                   `json:"client_key_id"`
	ClientLabel string                   `json:"client_label"`
	Summary     TopologyClientSummary    `json:"summary"`
	Models      []TopologyClientModelRow `json:"models"`
}

func (s *Store) TopologyClients(ctx context.Context, lookups UsageLookupMaps) ([]TopologyClient, error) {
	todayStart := usageStartOfDayUTC(time.Now().UTC())
	rows, err := s.db.QueryContext(ctx, `
SELECT
  COALESCE(NULLIF(client_api_key_id, ''), 'local-no-key') AS client_key_id,
  COUNT(*) AS total_requests,
  SUM(CASE WHEN created_at >= ? THEN 1 ELSE 0 END) AS today_requests,
  MAX(created_at) AS last_seen_at,
  MIN(created_at) AS first_seen_at
FROM usage_events
GROUP BY client_key_id
ORDER BY MAX(created_at) DESC
LIMIT 50`, todayStart.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	clients := make([]TopologyClient, 0)
	clientKeys := make([]string, 0)
	for rows.Next() {
		var item TopologyClient
		var lastSeen, firstSeen string
		if err := rows.Scan(&item.ClientKeyID, &item.TotalRequests, &item.TodayRequests, &lastSeen, &firstSeen); err != nil {
			return nil, err
		}
		item.LastSeenAt = lastSeen
		item.FirstSeenAt = firstSeen
		item.ClientLabel = topologyClientLabel(item.ClientKeyID, lookups)
		clients = append(clients, item)
		clientKeys = append(clientKeys, item.ClientKeyID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(clients) == 0 {
		return clients, nil
	}

	providerRows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
SELECT COALESCE(NULLIF(client_api_key_id, ''), 'local-no-key') AS client_key_id, provider_id
FROM usage_events
WHERE COALESCE(NULLIF(client_api_key_id, ''), 'local-no-key') IN (%s)
GROUP BY client_key_id, provider_id`, sqlPlaceholders(len(clientKeys))), stringSliceArgs(clientKeys)...)
	if err != nil {
		return nil, err
	}
	defer providerRows.Close()
	providersByClient := map[string]map[string]struct{}{}
	for providerRows.Next() {
		var clientKey, providerID string
		if err := providerRows.Scan(&clientKey, &providerID); err != nil {
			return nil, err
		}
		if providerID == "" {
			continue
		}
		if providersByClient[clientKey] == nil {
			providersByClient[clientKey] = map[string]struct{}{}
		}
		providersByClient[clientKey][lookupName(lookups.ProviderNames, providerID, providerID)] = struct{}{}
	}
	if err := providerRows.Err(); err != nil {
		return nil, err
	}

	usageRows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
SELECT
  COALESCE(NULLIF(client_api_key_id, ''), 'local-no-key') AS client_key_id,
  provider_id,
  credential_id,
  COALESCE(NULLIF(public_model_id, ''), upstream_model, 'unknown') AS model,
  COUNT(*) AS request_count,
  MAX(created_at) AS last_used_at
FROM usage_events
WHERE COALESCE(NULLIF(client_api_key_id, ''), 'local-no-key') IN (%s)
GROUP BY client_key_id, provider_id, credential_id, model
ORDER BY COUNT(*) DESC`, sqlPlaceholders(len(clientKeys))), stringSliceArgs(clientKeys)...)
	if err != nil {
		return nil, err
	}
	defer usageRows.Close()
	usageByClient := map[string][]TopologyModelUsage{}
	for usageRows.Next() {
		var clientKey, providerID, credentialID, model, lastUsed string
		var requestCount int
		if err := usageRows.Scan(&clientKey, &providerID, &credentialID, &model, &requestCount, &lastUsed); err != nil {
			return nil, err
		}
		usageByClient[clientKey] = append(usageByClient[clientKey], TopologyModelUsage{
			Provider:     lookupName(lookups.ProviderNames, providerID, providerID),
			CredentialID: credentialID,
			AccountLabel: lookupName(lookups.CredentialName, credentialID, credentialID),
			Model:        model,
			RequestCount: requestCount,
			LastUsedAt:   lastUsed,
		})
	}
	if err := usageRows.Err(); err != nil {
		return nil, err
	}

	for index := range clients {
		key := clients[index].ClientKeyID
		providerSet := providersByClient[key]
		providers := make([]string, 0, len(providerSet))
		for provider := range providerSet {
			providers = append(providers, provider)
		}
		clients[index].Providers = providers
		clients[index].ModelUsage = usageByClient[key]
	}
	return clients, nil
}

func (s *Store) TopologyClientDetail(ctx context.Context, clientKeyID string, lookups UsageLookupMaps) (TopologyClientDetail, error) {
	if strings.TrimSpace(clientKeyID) == "" {
		return TopologyClientDetail{}, sql.ErrNoRows
	}
	todayStart := usageStartOfDayUTC(time.Now().UTC())
	row := s.db.QueryRowContext(ctx, `
SELECT
  COUNT(*) AS total_requests,
  SUM(CASE WHEN created_at >= ? THEN 1 ELSE 0 END) AS today_requests,
  MAX(created_at) AS last_seen_at,
  MIN(created_at) AS first_seen_at
FROM usage_events
WHERE COALESCE(NULLIF(client_api_key_id, ''), 'local-no-key') = ?`,
		todayStart.UTC().Format(time.RFC3339Nano), clientKeyID)

	detail := TopologyClientDetail{
		ClientKeyID: clientKeyID,
		ClientLabel: topologyClientLabel(clientKeyID, lookups),
	}
	if err := row.Scan(&detail.Summary.TotalRequests, &detail.Summary.TodayRequests, &detail.Summary.LastSeenAt, &detail.Summary.FirstSeenAt); err != nil {
		return TopologyClientDetail{}, err
	}

	modelRows, err := s.db.QueryContext(ctx, `
SELECT
  COALESCE(NULLIF(public_model_id, ''), upstream_model, 'unknown') AS model,
  provider_id,
  credential_id,
  COUNT(*) AS request_count,
  MAX(created_at) AS last_used_at
FROM usage_events
WHERE COALESCE(NULLIF(client_api_key_id, ''), 'local-no-key') = ?
GROUP BY model, provider_id, credential_id
ORDER BY COUNT(*) DESC
LIMIT 50`, clientKeyID)
	if err != nil {
		return TopologyClientDetail{}, err
	}
	defer modelRows.Close()
	for modelRows.Next() {
		var item TopologyClientModelRow
		var providerID, credentialID string
		if err := modelRows.Scan(&item.Model, &providerID, &credentialID, &item.RequestCount, &item.LastUsedAt); err != nil {
			return TopologyClientDetail{}, err
		}
		item.Provider = lookupName(lookups.ProviderNames, providerID, providerID)
		item.CredentialID = credentialID
		item.AccountLabel = lookupName(lookups.CredentialName, credentialID, credentialID)
		detail.Models = append(detail.Models, item)
	}
	return detail, modelRows.Err()
}

func topologyClientLabel(clientKeyID string, lookups UsageLookupMaps) string {
	if clientKeyID == "" || clientKeyID == "local-no-key" {
		return "Local (No API Key)"
	}
	return lookupName(lookups.APIKeyNames, clientKeyID, clientKeyID)
}

func sqlPlaceholders(count int) string {
	if count <= 0 {
		return "?"
	}
	parts := make([]string, count)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}

func stringSliceArgs(items []string) []any {
	args := make([]any, len(items))
	for i, item := range items {
		args[i] = item
	}
	return args
}
