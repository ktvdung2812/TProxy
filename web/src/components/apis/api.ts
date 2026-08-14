import type { ApiKeyFormData, ApiKeyRecord, ApiKeyUsage } from "./types";
import { formToPayload } from "./utils";

async function adminFetch<T>(
  secret: string,
  path: string,
  method: "GET" | "POST" | "PUT" | "DELETE" = "GET",
  body?: unknown,
) {
  const response = await fetch(path, {
    method,
    headers: {
      ...(secret ? { Authorization: `Bearer ${secret}` } : {}),
      ...(body ? { "Content-Type": "application/json" } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(data?.error?.message || `HTTP ${response.status}`);
  }
  return data as T;
}

export function fetchApiKeyUsage(secret: string) {
  return adminFetch<{ api_keys: ApiKeyUsage[] }>(secret, "/api/admin/quota/summary");
}

export function createApiKey(secret: string, form: ApiKeyFormData) {
  return adminFetch<{ id: string; key: string; warning?: string }>(
    secret,
    "/api/admin/api-keys",
    "POST",
    formToPayload(form, false),
  );
}

export function updateApiKey(secret: string, id: string, form: ApiKeyFormData) {
  return adminFetch<{ ok: boolean; id: string }>(
    secret,
    `/api/admin/api-keys/${encodeURIComponent(id)}`,
    "PUT",
    formToPayload(form, true),
  );
}

export function deleteApiKey(secret: string, id: string) {
  return adminFetch<{ ok: boolean; id: string }>(
    secret,
    `/api/admin/api-keys/${encodeURIComponent(id)}`,
    "DELETE",
  );
}

export function toggleApiKey(secret: string, key: ApiKeyRecord, enabled: boolean) {
  return updateApiKey(secret, key.id, {
    id: key.id,
    name: key.name,
    models: Array.isArray(key.models) ? key.models.join(", ") : "*",
    enabled,
    team: key.policy?.team || "",
    endpoints: key.policy?.endpoints?.join(", ") || "",
    rpm: key.policy?.limits?.requests_per_minute || 0,
    streams: key.policy?.limits?.concurrent_streams || 0,
    max_input_bytes: Number(key.policy?.limits?.max_input_bytes || 0),
    max_output_tokens: key.policy?.limits?.max_output_tokens || 0,
    media_jobs: key.policy?.limits?.media_jobs || 0,
    budget_usd_per_day: key.policy?.limits?.budget_usd_per_day || 0,
  });
}

export function fetchResolvableApiKeySecrets(secret: string) {
  return adminFetch<{ secrets: Record<string, string> }>(secret, "/api/admin/api-key-secrets");
}
