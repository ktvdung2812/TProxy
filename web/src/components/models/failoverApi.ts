/* ============================================================
   Provider failover state for the Provider Priority Manager.
   Backed by /api/admin/failover in internal/api/failover_admin.go: the router
   pulls a provider out of a model's chain after repeated failures, and these
   endpoints expose that state so the UI can explain why P1 is not serving.
   ============================================================ */

export type FailoverState = {
  model_id: string;
  provider_id: string;
  state: "closed" | "degraded" | "open" | "half_open";
  failures: number;
  threshold: number;
  trips: number;
  opened_at?: string;
  retry_at?: string;
  last_status?: number;
  last_error?: string;
  last_failure_at?: string;
  retry_in_seconds: number;
};

async function failoverFetch<T>(secret: string, path: string, method: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { ...(secret ? { Authorization: `Bearer ${secret}` } : {}) };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const response = await fetch(path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const data = (await response.json().catch(() => ({}))) as T & { error?: { message?: string } };
  if (!response.ok) throw new Error(data?.error?.message || `HTTP ${response.status}`);
  return data;
}

export async function fetchFailoverState(secret: string, modelId?: string): Promise<FailoverState[]> {
  const query = modelId ? `?model_id=${encodeURIComponent(modelId)}` : "";
  const data = await failoverFetch<{ failover?: FailoverState[] }>(secret, `/api/admin/failover${query}`, "GET");
  return data.failover || [];
}

export async function resetFailoverState(secret: string, modelId: string, providerId: string): Promise<void> {
  await failoverFetch(secret, "/api/admin/failover/reset", "POST", { model_id: modelId, provider_id: providerId });
}
