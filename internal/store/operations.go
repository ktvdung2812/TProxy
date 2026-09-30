package store

import (
	"context"
	"time"
)

type OperationalMetric struct {
	ProviderID          string  `json:"provider_id"`
	ModelID             string  `json:"model_id"`
	CredentialID        string  `json:"credential_id"`
	Requests            int     `json:"requests"`
	Errors              int     `json:"errors"`
	Fallbacks           int     `json:"fallbacks"`
	P95MS               int64   `json:"p95_ms"`
	TTFTP95MS           *int64  `json:"ttft_p95_ms"`
	QueueMS             float64 `json:"queue_ms"`
	InputTokens         int     `json:"input_tokens"`
	OutputTokens        int     `json:"output_tokens"`
	CachedTokens        int     `json:"cached_tokens"`
	CacheCreationTokens int     `json:"cache_creation_tokens"`
}

// OperationalMetrics counts attempts; local capacity limits and cancellations
// are not upstream errors.
func (s *Store) OperationalMetrics(ctx context.Context, since time.Time) ([]OperationalMetric, error) {
	rows, err := s.db.QueryContext(ctx, `WITH ranked AS (
 SELECT *, ROW_NUMBER() OVER (PARTITION BY provider_id,public_model_id,credential_id ORDER BY latency_ms) AS latency_rank,
 ROW_NUMBER() OVER (PARTITION BY provider_id,public_model_id,credential_id ORDER BY ttft_ms NULLS LAST) AS ttft_rank,
 COUNT(*) OVER (PARTITION BY provider_id,public_model_id,credential_id) AS samples,
 COUNT(ttft_ms) OVER (PARTITION BY provider_id,public_model_id,credential_id) AS ttft_samples
 FROM usage_events WHERE created_at>=?
)
SELECT provider_id,public_model_id,credential_id,COUNT(*),
 SUM(CASE WHEN error_code IN ('provider_concurrency_limit','account_queue_full','account_queue_timeout','account_unavailable') THEN 0
 WHEN status=0 OR status=429 OR status>=500 OR status IN (401,403) THEN 1 ELSE 0 END),
 SUM(CASE WHEN attempt>1 THEN 1 ELSE 0 END),
 MIN(CASE WHEN latency_rank >= (samples*95+99)/100 THEN latency_ms END),
 MIN(CASE WHEN ttft_rank >= (ttft_samples*95+99)/100 THEN ttft_ms END),
 AVG(queue_ms),SUM(input_tokens),SUM(output_tokens),SUM(cached_tokens),SUM(cache_creation_tokens)
FROM ranked GROUP BY provider_id,public_model_id,credential_id ORDER BY COUNT(*) DESC`, since.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []OperationalMetric{}
	for rows.Next() {
		var item OperationalMetric
		if err = rows.Scan(&item.ProviderID, &item.ModelID, &item.CredentialID, &item.Requests, &item.Errors, &item.Fallbacks, &item.P95MS, &item.TTFTP95MS, &item.QueueMS, &item.InputTokens, &item.OutputTokens, &item.CachedTokens, &item.CacheCreationTokens); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
