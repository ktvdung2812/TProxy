export type UsagePeriod = "today" | "24h" | "7d" | "30d" | "60d";

export type UsageBucketEntry = {
  requests: number;
  promptTokens: number;
  completionTokens: number;
  cachedTokens: number;
  cost: number;
  rawModel?: string;
  provider?: string;
  connectionId?: string;
  accountName?: string;
  keyName?: string;
  apiKeyKey?: string;
  endpoint?: string;
  lastUsed?: string;
};

export type UsageRecentRequest = {
  requestId?: string;
  timestamp: string;
  model: string;
  provider: string;
  promptTokens: number;
  completionTokens: number;
  cachedTokens: number;
  status: string;
};

export type UsageActiveRequest = {
  request_id?: string;
  provider?: string;
  model?: string;
  account?: string;
  credential_id?: string;
};

export type UsageStats = {
  totalRequests: number;
  totalPromptTokens: number;
  totalCompletionTokens: number;
  totalCachedTokens: number;
  totalCost: number;
  byProvider: Record<string, UsageBucketEntry>;
  byModel: Record<string, UsageBucketEntry>;
  byAccount: Record<string, UsageBucketEntry>;
  byCredential?: Record<string, { requests: number; promptTokens: number; completionTokens: number }>;
  byApiKey: Record<string, UsageBucketEntry>;
  byEndpoint: Record<string, UsageBucketEntry>;
  recentRequests: UsageRecentRequest[];
  activeRequests?: UsageActiveRequest[];
  errorProvider?: string;
  pending?: Record<string, Record<string, number>>;
};

export type UsageChartPoint = {
  label: string;
  tokens: number;
  cost: number;
};

export type UsageEvent = {
  request_id: string;
  client_api_key_id?: string;
  public_model_id: string;
  provider_id: string;
  upstream_model: string;
  credential_id: string;
  attempt: number;
  status: number;
  input_tokens: number;
  output_tokens: number;
  reasoning_tokens: number;
  cached_tokens: number;
  tokens_saved: number;
  estimated_cost_usd: number;
  latency_ms: number;
  error_code?: string;
  created_at: string;
};

async function adminFetch<T>(secret: string, path: string) {
  const response = await fetch(path, {
    headers: secret ? { Authorization: `Bearer ${secret}` } : {},
  });
  const data = await response.json();
  if (!response.ok) {
    throw new Error(data?.error?.message || `HTTP ${response.status}`);
  }
  return data as T;
}

export function fetchUsageStats(secret: string, period: UsagePeriod) {
  return adminFetch<UsageStats>(secret, `/api/admin/usage/stats?period=${encodeURIComponent(period)}`).then((data) => ({
    ...data,
    recentRequests: data.recentRequests ?? [],
    activeRequests: data.activeRequests ?? [],
  }));
}

export function fetchUsageChart(secret: string, period: UsagePeriod) {
  return adminFetch<UsageChartPoint[]>(secret, `/api/admin/usage/chart?period=${encodeURIComponent(period)}`);
}

export function fetchUsageEvents(secret: string, params: { limit?: number; offset?: number; providerId?: string } = {}) {
  const search = new URLSearchParams();
  if (params.limit) search.set("limit", String(params.limit));
  if (params.offset) search.set("offset", String(params.offset));
  if (params.providerId) search.set("provider_id", params.providerId);
  const suffix = search.toString();
  return adminFetch<{ data: UsageEvent[]; total: number; limit: number; offset: number }>(
    secret,
    `/api/admin/usage${suffix ? `?${suffix}` : ""}`,
  );
}
