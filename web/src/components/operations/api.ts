export type OperationsMetric = {
  provider_id: string;
  model_id: string;
  credential_id: string;
  requests: number;
  errors: number;
  fallbacks: number;
  p95_ms: number;
  ttft_p95_ms: number | null;
  queue_ms: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  cache_creation_tokens: number;
};

export type OperationsAccount = {
  credential_id: string;
  in_flight: number;
  waiting: number;
  samples: number;
  latency_ms: number;
  ttft_ms: number;
  error_rate: number;
  quota_remaining?: number;
  quota_at: string;
};

export type OperationsAlertKind =
  | "high_error_rate"
  | "quota_exhausted"
  | "authentication_failed"
  | "account_expired"
  | "route_unavailable";

export type OperationsAlert = {
  id: string;
  kind: OperationsAlertKind | string;
  resource_id: string;
  first_seen: string;
  last_seen?: string;
};

export type OperationsSnapshot = {
  metrics: OperationsMetric[];
  accounts: OperationsAccount[];
  alerts: OperationsAlert[];
  since: string;
  evaluated_at: string;
};

export async function fetchOperations(secret: string, signal?: AbortSignal) {
  const response = await fetch("/api/admin/operations", {
    headers: secret ? { Authorization: `Bearer ${secret}` } : {},
    signal,
  });
  const body = await response.json();
  if (!response.ok) throw new Error(body?.error?.message || `HTTP ${response.status}`);
  return body as OperationsSnapshot;
}
