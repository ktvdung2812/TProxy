/* ============================================================
   Provider/credential/OAuth API wrappers for tdproxy.
   All admin endpoints live under /api/admin and require a Bearer management
   secret. Mapping per the Go router in internal/api/server.go (admin dispatcher).
   Note the casing asymmetry: reads (snapshot) return providers in PascalCase,
   writes accept snake_case bodies.
   ============================================================ */

export type DiscoveredModel = {
  id: string;
  name?: string;
  owned_by?: string;
  capabilities?: string[];
  credential_ids?: string[];
};
export type ProxyPoolOption = { id: string; name: string; url: string; enabled: boolean; status: string };

export type ProviderHealthResult = {
  ok: boolean;
  provider_id: string;
  status?: string;
  last_error?: string;
  last_checked_at?: string;
  checked?: number;
  healthy?: number;
  failed?: number;
};

export type CredentialHealthResult = {
  ok: boolean;
  credential_id: string;
  provider_id: string;
  status?: string;
  last_error?: string;
  error?: string;
};

export type OAuthStartParams = {
  provider_id: string;
  credential_id?: string;
  label?: string;
  email?: string;
  mode?: "browser" | "device";
  redirect_url?: string;
};

export type OAuthStartResponse = {
  session_id: string;
  provider_id: string;
  credential_id: string;
  mode: string;
  authorization_url?: string;
  user_code?: string;
  verification_uri?: string;
  interval_seconds?: number;
  expires_at?: string;
};

export type OAuthSessionStatus = {
  session_id: string;
  provider_id: string;
  credential_id: string;
  mode: string;
  status: string;
  expires_at?: string;
  consumed_at?: string;
  error_code?: string;
};

/** Common fetch wrapper that injects the Bearer secret and JSON content type. */
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
    throw new Error(data?.error?.message || `HTTP ${response.status}`);
  }
  return data;
}

/* ------------------------------------------------------------
   Provider mutations
   ------------------------------------------------------------ */

export type SaveProviderBody = {
  id: string;
  type: string;
  name?: string;
  base_url?: string;
  enabled?: boolean;
  proxy_pools?: string[];
  // OAuth config is optional and typically applied via backend defaults; allow override.
  oauth?: Record<string, unknown>;
};

export type NinerouterPreset = {
  id: string;
  type: string;
  name: string;
  base_url?: string;
  auth_type: string;
  category: string;
  credential_auth?: string;
  auth_hint?: string;
  api_key_url?: string;
  auth_modes?: string[];
  has_oauth?: boolean;
  supports_quota: boolean;
  no_auth: boolean;
};

export function fetchNinerouterPresets(secret: string) {
  return adminFetch<{ presets: NinerouterPreset[]; aliases: Record<string, string> }>(
    secret,
    "/api/admin/ninerouter/presets",
    "GET",
  );
}

export function saveProvider(secret: string, body: SaveProviderBody) {
  return adminFetch<{ ok: boolean; provider_id: string }>(secret, "/api/admin/providers", "PUT", body);
}

export function deleteProvider(secret: string, id: string) {
  return adminFetch<{ ok: boolean; provider_id: string }>(secret, `/api/admin/providers/${encodeURIComponent(id)}`, "DELETE");
}

export function checkProviderHealth(secret: string, id: string) {
  return adminFetch<ProviderHealthResult>(secret, `/api/admin/providers/${encodeURIComponent(id)}/health`, "POST");
}

export function checkCredentialHealth(secret: string, id: string) {
  return adminFetch<CredentialHealthResult>(
    secret,
    `/api/admin/credentials/${encodeURIComponent(id)}/health`,
    "POST",
  );
}

export function discoverProviderModels(secret: string, id: string) {
  return adminFetch<{ provider_id: string; data: DiscoveredModel[]; error?: { message: string } }>(
    secret,
    `/api/admin/providers/${encodeURIComponent(id)}/models`,
    "GET",
  );
}

export function discoverCredentialModels(secret: string, credentialId: string) {
  return adminFetch<{
    credential_id: string;
    provider_id: string;
    data: DiscoveredModel[];
    error?: { message: string };
  }>(secret, `/api/admin/credentials/${encodeURIComponent(credentialId)}/models`, "GET");
}

export type ModelTestResult = {
  ok: boolean;
  latency_ms: number;
  error?: string;
  status?: number;
  provider_id?: string;
  model_id?: string;
  public_model_id?: string;
  credential_id?: string;
};

export function testModel(
  secret: string,
  body: {
    provider_id?: string;
    model_id?: string;
    public_model_id?: string;
    kind?: string;
    credential_id?: string;
    credential_ids?: string[];
  },
) {
  return adminFetch<ModelTestResult>(secret, "/api/admin/models/test", "POST", body);
}

/* ------------------------------------------------------------
   Credential mutations
   ------------------------------------------------------------ */

export type SaveCredentialBody = {
  provider_id: string;
  credential: {
    id: string;
    label?: string;
    email?: string;
    auth_type: "api_key" | "oauth" | "service_account" | "none";
    secret?: string;
    priority?: number;
    weight?: number;
    enabled?: boolean;
    proxy_pools?: string[];
  };
};

export function saveCredential(secret: string, body: SaveCredentialBody) {
  return adminFetch<{ ok: boolean; credential_id: string }>(secret, "/api/admin/credentials", "PUT", body);
}

export function deleteCredential(secret: string, id: string) {
  return adminFetch<{ ok: boolean; credential_id: string }>(secret, `/api/admin/credentials/${encodeURIComponent(id)}`, "DELETE");
}

export type CredentialRefreshResult = {
  ok: boolean;
  credential_id: string;
  status?: {
    credential_id: string;
    provider_id: string;
    status: string;
    expires_at?: string;
    token_type?: string;
  };
};

export function refreshCredential(secret: string, id: string) {
  return adminFetch<CredentialRefreshResult>(
    secret,
    `/api/admin/credentials/${encodeURIComponent(id)}/refresh`,
    "POST",
  );
}

export function clearCredentialCooldown(secret: string, id: string) {
  return adminFetch<{ ok: boolean; credential_id: string }>(
    secret,
    `/api/admin/credentials/${encodeURIComponent(id)}/clear-cooldown`,
    "POST",
  );
}

export function resetProviderCooldowns(secret: string, providerId: string) {
  return adminFetch<{ ok: boolean; provider_id: string; credentials_cleared: number }>(
    secret,
    `/api/admin/providers/${encodeURIComponent(providerId)}/cooldowns/reset`,
    "POST",
  );
}

export async function exportAuthBundle(secret: string): Promise<Blob> {
  const response = await fetch("/api/admin/auth/export", {
    headers: secret ? { Authorization: `Bearer ${secret}` } : {},
  });
  if (!response.ok) {
    const data = (await response.json().catch(() => ({}))) as { error?: { message?: string } };
    throw new Error(data?.error?.message || `HTTP ${response.status}`);
  }
  return response.blob();
}

export function importAuthBundle(secret: string, bundle: unknown) {
  return adminFetch<{ ok: boolean }>(secret, "/api/admin/auth/import", "POST", bundle);
}

/* ------------------------------------------------------------
   OAuth flow
   ------------------------------------------------------------ */

export function startOAuth(secret: string, params: OAuthStartParams) {
  return adminFetch<OAuthStartResponse>(secret, "/api/admin/oauth/start", "POST", params);
}

export function pollOAuthStatus(secret: string, sessionId: string) {
  return adminFetch<OAuthSessionStatus>(
    secret,
    `/api/admin/oauth/status?session_id=${encodeURIComponent(sessionId)}`,
    "GET",
  );
}

export function cancelOAuth(secret: string, sessionId: string) {
  return adminFetch<{ ok: boolean; session_id: string }>(
    secret,
    `/api/admin/oauth/session?session_id=${encodeURIComponent(sessionId)}`,
    "DELETE",
  );
}

export function completeOAuthCallback(secret: string, state: string, code: string) {
  const params = new URLSearchParams({ state, code });
  return adminFetch<OAuthSessionStatus>(secret, `/api/admin/oauth/callback?${params.toString()}`, "GET");
}

/* ------------------------------------------------------------
   Proxy pools (for credential/provider binding dropdowns)
   ------------------------------------------------------------ */

export function listProxyPools(secret: string) {
  return adminFetch<{ proxy_pools: ProxyPoolOption[] }>(secret, "/api/admin/proxy-pools", "GET");
}

/* ------------------------------------------------------------
   Provider enable/disable (toggle whole provider)
   tdproxy persists Enabled on the provider itself via PUT /api/admin/providers.
   ------------------------------------------------------------ */

export function setProviderEnabled(secret: string, provider: SaveProviderBody) {
  return adminFetch<{ ok: boolean; provider_id: string }>(secret, "/api/admin/providers", "PUT", provider);
}

/* ------------------------------------------------------------
   Model aliases (scoped) — POST/DELETE /api/admin/aliases
   tdproxy aliases are global (alias -> public_model_id), optionally scoped
   to a client API key or team. Used here to surface "models routed to this
   provider" and let users add a friendly alias.
   ------------------------------------------------------------ */

export type ModelAliasBody = {
  alias: string;
  public_model_id: string;
  api_key_id?: string;
  team_id?: string;
};

export function saveModelAlias(secret: string, body: ModelAliasBody) {
  return adminFetch<{ ok: boolean; alias: string }>(secret, "/api/admin/aliases", "POST", body);
}

export function deleteModelAlias(secret: string, alias: string, apiKeyId?: string, teamId?: string) {
  const params = new URLSearchParams();
  if (apiKeyId) params.set("api_key_id", apiKeyId);
  if (teamId) params.set("team_id", teamId);
  const query = params.toString();
  return adminFetch<{ ok: boolean; alias: string }>(
    secret,
    `/api/admin/aliases/${encodeURIComponent(alias)}${query ? `?${query}` : ""}`,
    "DELETE",
  );
}

