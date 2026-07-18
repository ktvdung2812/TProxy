package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type UsageBucketEntry struct {
	Requests         int     `json:"requests"`
	PromptTokens     int     `json:"promptTokens"`
	CompletionTokens int     `json:"completionTokens"`
	CachedTokens     int     `json:"cachedTokens"`
	Cost             float64 `json:"cost"`
	RawModel         string  `json:"rawModel,omitempty"`
	Provider         string  `json:"provider,omitempty"`
	ConnectionID     string  `json:"connectionId,omitempty"`
	AccountName      string  `json:"accountName,omitempty"`
	KeyName          string  `json:"keyName,omitempty"`
	APIKeyKey        string  `json:"apiKeyKey,omitempty"`
	Endpoint         string  `json:"endpoint,omitempty"`
	LastUsed         string  `json:"lastUsed,omitempty"`
}

type UsageRecentRequest struct {
	Timestamp        string `json:"timestamp"`
	Model            string `json:"model"`
	Provider         string `json:"provider"`
	PromptTokens     int    `json:"promptTokens"`
	CompletionTokens int    `json:"completionTokens"`
	CachedTokens     int    `json:"cachedTokens"`
	Status           string `json:"status"`
}

type CredentialUsageSummary struct {
	Requests         int `json:"requests"`
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
}

type UsageStatsPayload struct {
	TotalRequests         int                              `json:"totalRequests"`
	TotalPromptTokens     int                              `json:"totalPromptTokens"`
	TotalCompletionTokens int                              `json:"totalCompletionTokens"`
	TotalCachedTokens     int                              `json:"totalCachedTokens"`
	TotalCost             float64                          `json:"totalCost"`
	ByProvider            map[string]UsageBucketEntry      `json:"byProvider"`
	ByModel               map[string]UsageBucketEntry      `json:"byModel"`
	ByAccount             map[string]UsageBucketEntry      `json:"byAccount"`
	ByCredential          map[string]CredentialUsageSummary `json:"byCredential"`
	ByAPIKey              map[string]UsageBucketEntry      `json:"byApiKey"`
	ByEndpoint            map[string]UsageBucketEntry      `json:"byEndpoint"`
	RecentRequests        []UsageRecentRequest             `json:"recentRequests"`
	ActiveRequests        []any                            `json:"activeRequests"`
	Pending               map[string]any                   `json:"pending"`
	ErrorProvider         string                           `json:"errorProvider"`
}

type UsageChartPoint struct {
	Label  string  `json:"label"`
	Tokens int     `json:"tokens"`
	Cost   float64 `json:"cost"`
}

type UsageLookupMaps struct {
	ProviderNames  map[string]string
	CredentialName map[string]string
	APIKeyNames    map[string]string
}

func UsagePeriodSince(period string, now time.Time) (time.Time, error) {
	switch strings.TrimSpace(period) {
	case "", "today":
		return usageStartOfDayUTC(now), nil
	case "24h":
		return now.Add(-24 * time.Hour), nil
	case "7d":
		return now.Add(-7 * 24 * time.Hour), nil
	case "30d":
		return now.Add(-30 * 24 * time.Hour), nil
	case "60d":
		return now.Add(-60 * 24 * time.Hour), nil
	case "all":
		return time.Time{}, nil
	default:
		return time.Time{}, fmt.Errorf("invalid usage period %q", period)
	}
}

func (s *Store) UsageStats(ctx context.Context, since time.Time, lookups UsageLookupMaps) (UsageStatsPayload, error) {
	stats := UsageStatsPayload{
		ByProvider:     map[string]UsageBucketEntry{},
		ByModel:        map[string]UsageBucketEntry{},
		ByAccount:      map[string]UsageBucketEntry{},
		ByCredential:   map[string]CredentialUsageSummary{},
		ByAPIKey:       map[string]UsageBucketEntry{},
		ByEndpoint:     map[string]UsageBucketEntry{},
		RecentRequests: []UsageRecentRequest{},
		ActiveRequests: []any{},
		Pending:        map[string]any{"byModel": map[string]int{}, "byAccount": map[string]any{}},
	}

	query := `SELECT public_model_id, provider_id, upstream_model, credential_id, client_api_key_id, status, input_tokens, output_tokens, tokens_saved, estimated_cost_usd, created_at FROM usage_events`
	args := []any{}
	if !since.IsZero() {
		query += ` WHERE created_at >= ?`
		args = append(args, since.UTC().Format(time.RFC3339Nano))
	}
	query += ` ORDER BY id DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return UsageStatsPayload{}, err
	}
	defer rows.Close()

	recentSeen := map[string]struct{}{}
	for rows.Next() {
		var (
			publicModelID, providerID, upstreamModel, credentialID, clientAPIKeyID, created string
			status, inputTokens, outputTokens, tokensSaved                                int
			cost                                                                            float64
		)
		if err := rows.Scan(&publicModelID, &providerID, &upstreamModel, &credentialID, &clientAPIKeyID, &status, &inputTokens, &outputTokens, &tokensSaved, &cost, &created); err != nil {
			return UsageStatsPayload{}, err
		}
		createdAt, _ := time.Parse(time.RFC3339Nano, created)
		providerName := lookupName(lookups.ProviderNames, providerID, providerID)
		rawModel := publicModelID
		if rawModel == "" {
			rawModel = upstreamModel
		}
		if rawModel == "" {
			rawModel = "unknown"
		}

		stats.TotalRequests++
		stats.TotalPromptTokens += inputTokens
		stats.TotalCompletionTokens += outputTokens
		stats.TotalCachedTokens += tokensSaved
		stats.TotalCost += cost

		addUsageBucket(stats.ByProvider, providerID, inputTokens, outputTokens, tokensSaved, cost, createdAt, func(entry *UsageBucketEntry) {
			entry.Provider = providerName
		})

		modelKey := rawModel
		if providerID != "" {
			modelKey = fmt.Sprintf("%s (%s)", rawModel, providerID)
		}
		addUsageBucket(stats.ByModel, modelKey, inputTokens, outputTokens, tokensSaved, cost, createdAt, func(entry *UsageBucketEntry) {
			entry.RawModel = rawModel
			entry.Provider = providerName
		})

		if credentialID != "" {
			accountName := lookupName(lookups.CredentialName, credentialID, fmt.Sprintf("Account %s...", shortID(credentialID)))
			accountKey := fmt.Sprintf("%s (%s - %s)", rawModel, providerID, accountName)
			addUsageBucket(stats.ByAccount, accountKey, inputTokens, outputTokens, tokensSaved, cost, createdAt, func(entry *UsageBucketEntry) {
				entry.RawModel = rawModel
				entry.Provider = providerName
				entry.ConnectionID = credentialID
				entry.AccountName = accountName
			})
			credentialUsage := stats.ByCredential[credentialID]
			credentialUsage.Requests++
			credentialUsage.PromptTokens += inputTokens
			credentialUsage.CompletionTokens += outputTokens
			stats.ByCredential[credentialID] = credentialUsage
		}

		apiKeyKey := "local-no-key"
		keyName := "Local (No API Key)"
		if clientAPIKeyID != "" {
			apiKeyKey = clientAPIKeyID
			keyName = lookupName(lookups.APIKeyNames, clientAPIKeyID, shortID(clientAPIKeyID)+"...")
		}
		akMapKey := fmt.Sprintf("%s|%s|%s", apiKeyKey, rawModel, providerID)
		addUsageBucket(stats.ByAPIKey, akMapKey, inputTokens, outputTokens, tokensSaved, cost, createdAt, func(entry *UsageBucketEntry) {
			entry.RawModel = rawModel
			entry.Provider = providerName
			entry.KeyName = keyName
			entry.APIKeyKey = apiKeyKey
		})

		if upstreamModel != "" {
			endpointKey := fmt.Sprintf("%s|%s|%s", upstreamModel, rawModel, providerID)
			addUsageBucket(stats.ByEndpoint, endpointKey, inputTokens, outputTokens, tokensSaved, cost, createdAt, func(entry *UsageBucketEntry) {
				entry.Endpoint = upstreamModel
				entry.RawModel = rawModel
				entry.Provider = providerName
			})
		}

		if len(stats.RecentRequests) < 20 && (inputTokens > 0 || outputTokens > 0) {
			minute := createdAt.UTC().Format("2006-01-02T15:04")
			dedupeKey := fmt.Sprintf("%s|%s|%d|%d|%s", rawModel, providerID, inputTokens, outputTokens, minute)
			if _, seen := recentSeen[dedupeKey]; !seen {
				recentSeen[dedupeKey] = struct{}{}
				stats.RecentRequests = append(stats.RecentRequests, UsageRecentRequest{
					Timestamp:        createdAt.UTC().Format(time.RFC3339Nano),
					Model:            rawModel,
					Provider:         providerName,
					PromptTokens:     inputTokens,
					CompletionTokens: outputTokens,
					CachedTokens:     tokensSaved,
					Status:           usageStatusLabel(status),
				})
			}
		}
	}
	if err := rows.Err(); err != nil {
		return UsageStatsPayload{}, err
	}
	return stats, nil
}

func (s *Store) UsageChart(ctx context.Context, period string, now time.Time) ([]UsageChartPoint, error) {
	since, err := UsagePeriodSince(period, now)
	if err != nil {
		return nil, err
	}

	switch period {
	case "today":
		return s.usageHourlyChart(ctx, usageStartOfDayUTC(now), usageStartOfDayUTC(now).Add(24*time.Hour), 24)
	case "24h":
		start := now.Add(-24 * time.Hour)
		return s.usageHourlyChart(ctx, start, now, 24)
	default:
		days := 7
		switch period {
		case "30d":
			days = 30
		case "60d":
			days = 60
		}
		return s.usageDailyChart(ctx, now, days, since)
	}
}

func (s *Store) usageHourlyChart(ctx context.Context, start, end time.Time, bucketCount int) ([]UsageChartPoint, error) {
	bucketMs := int64(time.Hour / time.Millisecond)
	startMs := start.UnixMilli()
	buckets := make([]UsageChartPoint, bucketCount)
	for i := range buckets {
		ts := startMs + int64(i)*bucketMs
		buckets[i].Label = time.UnixMilli(ts).Format("15:04")
	}

	rows, err := s.db.QueryContext(ctx, `SELECT input_tokens, output_tokens, estimated_cost_usd, created_at FROM usage_events WHERE created_at >= ? AND created_at <= ?`, start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var inputTokens, outputTokens int
		var cost float64
		var created string
		if err := rows.Scan(&inputTokens, &outputTokens, &cost, &created); err != nil {
			return nil, err
		}
		createdAt, _ := time.Parse(time.RFC3339Nano, created)
		idx := int((createdAt.UnixMilli() - startMs) / bucketMs)
		if idx < 0 || idx >= bucketCount {
			continue
		}
		buckets[idx].Tokens += inputTokens + outputTokens
		buckets[idx].Cost += cost
	}
	return buckets, rows.Err()
}

func (s *Store) usageDailyChart(ctx context.Context, now time.Time, days int, since time.Time) ([]UsageChartPoint, error) {
	startDay := usageStartOfDayUTC(now.Add(-time.Duration(days-1) * 24 * time.Hour))
	if !since.IsZero() && since.After(startDay) {
		startDay = usageStartOfDayUTC(since)
	}
	buckets := []UsageChartPoint{}
	day := usageStartOfDayUTC(startDay)
	endDay := usageStartOfDayUTC(now).Add(24 * time.Hour)
	for day.Before(endDay) {
		buckets = append(buckets, UsageChartPoint{Label: day.Format("Jan 2")})
		day = day.Add(24 * time.Hour)
	}

	rows, err := s.db.QueryContext(ctx, `SELECT input_tokens, output_tokens, estimated_cost_usd, created_at FROM usage_events WHERE created_at >= ?`, startDay.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var inputTokens, outputTokens int
		var cost float64
		var created string
		if err := rows.Scan(&inputTokens, &outputTokens, &cost, &created); err != nil {
			return nil, err
		}
		createdAt, _ := time.Parse(time.RFC3339Nano, created)
		idx := int(usageStartOfDayUTC(createdAt).Sub(startDay).Hours() / 24)
		if idx < 0 || idx >= len(buckets) {
			continue
		}
		buckets[idx].Tokens += inputTokens + outputTokens
		buckets[idx].Cost += cost
	}
	return buckets, rows.Err()
}

func addUsageBucket(target map[string]UsageBucketEntry, key string, inputTokens, outputTokens, tokensSaved int, cost float64, createdAt time.Time, decorate func(*UsageBucketEntry)) {
	entry := target[key]
	entry.Requests++
	entry.PromptTokens += inputTokens
	entry.CompletionTokens += outputTokens
	entry.CachedTokens += tokensSaved
	entry.Cost += cost
	if entry.LastUsed == "" || createdAt.After(parseUsageTime(entry.LastUsed)) {
		entry.LastUsed = createdAt.UTC().Format(time.RFC3339Nano)
	}
	decorate(&entry)
	target[key] = entry
}

func lookupName(table map[string]string, key, fallback string) string {
	if table == nil {
		return fallback
	}
	if value := strings.TrimSpace(table[key]); value != "" {
		return value
	}
	return fallback
}

func shortID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}

func usageStatusLabel(status int) string {
	if status >= 200 && status < 400 {
		return "ok"
	}
	if status == 0 {
		return "error"
	}
	return "error"
}

func parseUsageTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func usageStartOfDayUTC(now time.Time) time.Time {
	year, month, day := now.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func (s *Store) CredentialUsageByPeriod(ctx context.Context, since time.Time) (map[string]CredentialUsageSummary, error) {
	query := `SELECT credential_id, COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0)
FROM usage_events
WHERE credential_id != ''`
	args := []any{}
	if !since.IsZero() {
		query += ` AND created_at >= ?`
		args = append(args, since.UTC().Format(time.RFC3339Nano))
	}
	query += ` GROUP BY credential_id`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	usage := map[string]CredentialUsageSummary{}
	for rows.Next() {
		var credentialID string
		var summary CredentialUsageSummary
		if err := rows.Scan(&credentialID, &summary.Requests, &summary.PromptTokens, &summary.CompletionTokens); err != nil {
			return nil, err
		}
		usage[credentialID] = summary
	}
	return usage, rows.Err()
}

func (s *Store) RecentUsageRequests(ctx context.Context, limit int, lookups UsageLookupMaps) ([]UsageRecentRequest, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT created_at, COALESCE(NULLIF(public_model_id, ''), upstream_model, 'unknown') AS model,
       provider_id, input_tokens, output_tokens, reasoning_tokens, tokens_saved, status
FROM usage_events
ORDER BY created_at DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]UsageRecentRequest, 0, limit)
	for rows.Next() {
		var createdAt, model, providerID string
		var inputTokens, outputTokens, reasoningTokens, tokensSaved, status int
		if err := rows.Scan(&createdAt, &model, &providerID, &inputTokens, &outputTokens, &reasoningTokens, &tokensSaved, &status); err != nil {
			return nil, err
		}
		if inputTokens == 0 && outputTokens == 0 {
			continue
		}
		items = append(items, UsageRecentRequest{
			Timestamp:        createdAt,
			Model:            model,
			Provider:         lookupName(lookups.ProviderNames, providerID, providerID),
			PromptTokens:     inputTokens,
			CompletionTokens: outputTokens,
			CachedTokens:     tokensSaved,
			Status:           usageStatusLabel(status),
		})
	}
	return items, rows.Err()
}
