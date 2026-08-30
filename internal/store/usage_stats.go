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
	RequestID        string `json:"requestId,omitempty"`
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
	TotalRequests         int                               `json:"totalRequests"`
	TotalPromptTokens     int                               `json:"totalPromptTokens"`
	TotalCompletionTokens int                               `json:"totalCompletionTokens"`
	TotalCachedTokens     int                               `json:"totalCachedTokens"`
	TotalCost             float64                           `json:"totalCost"`
	ByProvider            map[string]UsageBucketEntry       `json:"byProvider"`
	ByModel               map[string]UsageBucketEntry       `json:"byModel"`
	ByAccount             map[string]UsageBucketEntry       `json:"byAccount"`
	ByCredential          map[string]CredentialUsageSummary `json:"byCredential"`
	ByAPIKey              map[string]UsageBucketEntry       `json:"byApiKey"`
	ByEndpoint            map[string]UsageBucketEntry       `json:"byEndpoint"`
	RecentRequests        []UsageRecentRequest              `json:"recentRequests"`
	ActiveRequests        []any                             `json:"activeRequests"`
	Pending               map[string]any                    `json:"pending"`
	ErrorProvider         string                            `json:"errorProvider"`
}

type UsageChartPoint struct {
	Label  string  `json:"label"`
	Tokens int     `json:"tokens"`
	Cost   float64 `json:"cost"`
}

// CredentialUsageChartPoint is the compact time-series payload used by the
// account detail modal. Requests are counted independently of token totals so
// zero-token/error attempts remain visible in the request series.
type CredentialUsageChartPoint struct {
	Label    string `json:"label"`
	Requests int    `json:"requests"`
	Tokens   int    `json:"tokens"`
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

	query := `SELECT public_model_id, provider_id, upstream_model, credential_id, client_api_key_id, status, input_tokens, output_tokens, cached_tokens, estimated_cost_usd, created_at FROM usage_events`
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
			status, inputTokens, outputTokens, cachedTokens                                 int
			cost                                                                            float64
		)
		if err := rows.Scan(&publicModelID, &providerID, &upstreamModel, &credentialID, &clientAPIKeyID, &status, &inputTokens, &outputTokens, &cachedTokens, &cost, &created); err != nil {
			return UsageStatsPayload{}, err
		}
		createdAt, _ := time.Parse(time.RFC3339Nano, created)
		providerName := lookupName(lookups.ProviderNames, providerID, providerID)
		rawModel := usageDisplayModel(publicModelID, upstreamModel, providerID)

		stats.TotalRequests++
		stats.TotalPromptTokens += inputTokens
		stats.TotalCompletionTokens += outputTokens
		stats.TotalCachedTokens += cachedTokens
		stats.TotalCost += cost

		addUsageBucket(stats.ByProvider, providerID, inputTokens, outputTokens, cachedTokens, cost, createdAt, func(entry *UsageBucketEntry) {
			entry.Provider = providerName
		})

		modelKey := rawModel
		if providerID != "" {
			modelKey = fmt.Sprintf("%s (%s)", rawModel, providerID)
		}
		addUsageBucket(stats.ByModel, modelKey, inputTokens, outputTokens, cachedTokens, cost, createdAt, func(entry *UsageBucketEntry) {
			entry.RawModel = rawModel
			entry.Provider = providerName
		})

		if credentialID != "" {
			accountName := lookupName(lookups.CredentialName, credentialID, fmt.Sprintf("Account %s...", shortID(credentialID)))
			accountKey := fmt.Sprintf("%s (%s - %s)", rawModel, providerID, accountName)
			addUsageBucket(stats.ByAccount, accountKey, inputTokens, outputTokens, cachedTokens, cost, createdAt, func(entry *UsageBucketEntry) {
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
		addUsageBucket(stats.ByAPIKey, akMapKey, inputTokens, outputTokens, cachedTokens, cost, createdAt, func(entry *UsageBucketEntry) {
			entry.RawModel = rawModel
			entry.Provider = providerName
			entry.KeyName = keyName
			entry.APIKeyKey = apiKeyKey
		})

		if upstreamModel != "" {
			endpointKey := fmt.Sprintf("%s|%s|%s", upstreamModel, rawModel, providerID)
			addUsageBucket(stats.ByEndpoint, endpointKey, inputTokens, outputTokens, cachedTokens, cost, createdAt, func(entry *UsageBucketEntry) {
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
					CachedTokens:     cachedTokens,
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

// CredentialUsageChart returns the last calendar day, week, or month of usage
// for one credential. The day view uses hourly buckets; week and month use
// calendar-day buckets in UTC, matching the existing dashboard usage charts.
func (s *Store) CredentialUsageChart(ctx context.Context, credentialID, period string, now time.Time) ([]CredentialUsageChartPoint, error) {
	if strings.TrimSpace(credentialID) == "" {
		return nil, fmt.Errorf("credential id is required")
	}

	period = strings.TrimSpace(strings.ToLower(period))
	startOfToday := usageStartOfDayUTC(now)
	var start, end time.Time
	var bucketCount int
	var bucketSize time.Duration
	var label func(time.Time) string
	switch period {
	case "day":
		start, end = startOfToday, startOfToday.Add(24*time.Hour)
		bucketCount, bucketSize, label = 24, time.Hour, func(value time.Time) string { return value.Format("15:04") }
	case "week":
		start, end = usageStartOfDayUTC(now.Add(-6*24*time.Hour)), startOfToday.Add(24*time.Hour)
		bucketCount, bucketSize, label = 7, 24*time.Hour, func(value time.Time) string { return value.Format("Mon") }
	case "month":
		start, end = usageStartOfDayUTC(now.Add(-29*24*time.Hour)), startOfToday.Add(24*time.Hour)
		bucketCount, bucketSize, label = 30, 24*time.Hour, func(value time.Time) string { return value.Format("Jan 2") }
	default:
		return nil, fmt.Errorf("invalid credential usage chart period %q", period)
	}

	buckets := make([]CredentialUsageChartPoint, bucketCount)
	for index := range buckets {
		buckets[index].Label = label(start.Add(time.Duration(index) * bucketSize))
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT input_tokens, output_tokens, created_at
FROM usage_events
WHERE credential_id=? AND created_at >= ? AND created_at < ?`,
		credentialID, start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	startMs := start.UnixMilli()
	bucketMs := bucketSize.Milliseconds()
	for rows.Next() {
		var inputTokens, outputTokens int
		var created string
		if err := rows.Scan(&inputTokens, &outputTokens, &created); err != nil {
			return nil, err
		}
		createdAt, parseErr := time.Parse(time.RFC3339Nano, created)
		if parseErr != nil {
			continue
		}
		index := int((createdAt.UnixMilli() - startMs) / bucketMs)
		if index < 0 || index >= len(buckets) {
			continue
		}
		buckets[index].Requests++
		buckets[index].Tokens += inputTokens + outputTokens
	}
	return buckets, rows.Err()
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

func addUsageBucket(target map[string]UsageBucketEntry, key string, inputTokens, outputTokens, cachedTokens int, cost float64, createdAt time.Time, decorate func(*UsageBucketEntry)) {
	entry := target[key]
	entry.Requests++
	entry.PromptTokens += inputTokens
	entry.CompletionTokens += outputTokens
	entry.CachedTokens += cachedTokens
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

// usageDisplayModel returns the upstream model name for usage tables.
// Direct provider:model requests are shown without the provider prefix.
func usageDisplayModel(publicModelID, upstreamModel, providerID string) string {
	if trimmed := strings.TrimSpace(upstreamModel); trimmed != "" {
		return trimmed
	}
	rawModel := strings.TrimSpace(publicModelID)
	if rawModel == "" {
		return "unknown"
	}
	if prefix, modelPart, ok := strings.Cut(rawModel, ":"); ok && prefix != "" && modelPart != "" {
		if providerID == "" || prefix == providerID {
			return modelPart
		}
	}
	return rawModel
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
SELECT request_id, created_at, public_model_id, upstream_model, provider_id, input_tokens, output_tokens, reasoning_tokens, cached_tokens, status
FROM usage_events
ORDER BY created_at DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]UsageRecentRequest, 0, limit)
	for rows.Next() {
		var requestID, createdAt, publicModelID, upstreamModel, providerID string
		var inputTokens, outputTokens, reasoningTokens, cachedTokens, status int
		if err := rows.Scan(&requestID, &createdAt, &publicModelID, &upstreamModel, &providerID, &inputTokens, &outputTokens, &reasoningTokens, &cachedTokens, &status); err != nil {
			return nil, err
		}
		if inputTokens == 0 && outputTokens == 0 {
			continue
		}
		items = append(items, UsageRecentRequest{
			RequestID:        requestID,
			Timestamp:        createdAt,
			Model:            usageDisplayModel(publicModelID, upstreamModel, providerID),
			Provider:         lookupName(lookups.ProviderNames, providerID, providerID),
			PromptTokens:     inputTokens,
			CompletionTokens: outputTokens,
			CachedTokens:     cachedTokens,
			Status:           usageStatusLabel(status),
		})
	}
	return items, rows.Err()
}
