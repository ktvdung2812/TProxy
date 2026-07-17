export type ProxyPoolRow = {
  id: string;
  name: string;
  url: string;
  enabled: boolean;
  status: string;
  last_error?: string;
  last_tested_at?: string;
  usage_count: number;
};

export type ProxyPoolTestResult = {
  ok: boolean;
  status?: number;
  elapsed_ms?: number;
  error?: string;
};

type ApiError = Error & { status?: number };

async function adminFetch<T>(
  secret: string,
  path: string,
  method: string,
  body?: unknown,
): Promise<T> {
  const headers: Record<string, string> = { ...(secret ? { Authorization: `Bearer ${secret}` } : {}) };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const response = await fetch(path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const data = (await response.json().catch(() => ({}))) as T & { error?: { message?: string } };
  if (!response.ok) {
    const error = new Error(data?.error?.message || `HTTP ${response.status}`) as ApiError;
    error.status = response.status;
    throw error;
  }
  return data;
}

export function listProxyPools(secret: string) {
  return adminFetch<{ proxy_pools: ProxyPoolRow[] }>(secret, "/api/admin/proxy-pools", "GET");
}

export function createProxyPool(
  secret: string,
  body: { id: string; name: string; url: string; enabled?: boolean },
) {
  return adminFetch<{ ok: boolean; proxy_pool_id: string }>(secret, "/api/admin/proxy-pools", "POST", body);
}

export function updateProxyPool(
  secret: string,
  id: string,
  body: { name?: string; url?: string; enabled?: boolean },
) {
  return adminFetch<{ ok: boolean; proxy_pool_id: string }>(
    secret,
    `/api/admin/proxy-pools/${encodeURIComponent(id)}`,
    "PUT",
    body,
  );
}

export function deleteProxyPool(secret: string, id: string) {
  return adminFetch<{ ok: boolean; proxy_pool_id: string }>(
    secret,
    `/api/admin/proxy-pools/${encodeURIComponent(id)}`,
    "DELETE",
  );
}

export function testProxyPool(secret: string, id: string) {
  return adminFetch<ProxyPoolTestResult>(
    secret,
    `/api/admin/proxy-pools/${encodeURIComponent(id)}/test`,
    "POST",
  );
}
