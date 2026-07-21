export type TokenSaverSettings = {
  rtk_enabled: boolean;
  compression_mode?: string;
  per_request_opt_out: boolean;
  cli_hook_recommended: boolean;
  upstream_project: string;
};

export type CompressionMode = "off" | "lite" | "rtk" | "caveman" | "stacked" | "full" | "ultra";

async function adminFetch<T>(secret: string, path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: {
      ...(secret ? { Authorization: `Bearer ${secret}` } : {}),
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
  });
  const data = await response.json();
  if (!response.ok) {
    throw new Error(data?.error?.message || `HTTP ${response.status}`);
  }
  return data as T;
}

export function fetchTokenSaverSettings(secret: string) {
  return adminFetch<TokenSaverSettings>(secret, "/api/admin/settings/token-saver");
}

export function updateTokenSaverSettings(
  secret: string,
  patch: Partial<Pick<TokenSaverSettings, "rtk_enabled" | "cli_hook_recommended" | "compression_mode">>,
) {
  return adminFetch<{ ok: boolean; rtk_enabled: boolean; compression_mode?: string }>(secret, "/api/admin/settings/token-saver", {
    method: "PATCH",
    body: JSON.stringify(patch),
  });
}
