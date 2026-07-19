export type RetentionSettings = {
  usage_events?: string;
  request_logs?: string;
  audit_events?: string;
  media_jobs?: string;
  oauth_sessions?: string;
  cleanup_interval?: string;
};

export type AdminSettings = {
  retention: RetentionSettings;
  payload_capture: boolean;
  allow_remote_management: boolean;
  allow_lan_management: boolean;
  server_host: string;
  server_port: number;
  token_saver: {
    enabled: boolean;
    rtk_enabled: boolean;
    per_request_opt_out: boolean;
    cli_hook_recommended: boolean;
    upstream_project: string;
  };
};

async function adminFetch<T>(secret: string, path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: {
      ...(secret ? { Authorization: `Bearer ${secret}` } : {}),
      ...(init?.body && !(init.body instanceof FormData) ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
  });
  const contentType = response.headers.get("content-type") || "";
  if (!response.ok) {
    if (contentType.includes("json")) {
      const data = await response.json();
      throw new Error(data?.error?.message || `HTTP ${response.status}`);
    }
    throw new Error(`HTTP ${response.status}`);
  }
  if (contentType.includes("json")) {
    return (await response.json()) as T;
  }
  return (await response.blob()) as T;
}

export function fetchAdminSettings(secret: string) {
  return adminFetch<AdminSettings>(secret, "/api/admin/settings");
}

export function reloadConfig(secret: string) {
  return adminFetch<{ ok: boolean; config_path?: string }>(secret, "/api/admin/reload", { method: "POST" });
}

export async function exportConfig(secret: string, format: "json" | "yaml") {
  const path = format === "yaml" ? "/api/admin/config/export?format=yaml" : "/api/admin/config/export";
  const headers: Record<string, string> = {};
  if (secret) headers.Authorization = `Bearer ${secret}`;
  if (format === "yaml") headers.Accept = "application/yaml";
  const response = await fetch(path, { headers });
  if (!response.ok) {
    const data = await response.json().catch(() => null);
    throw new Error(data?.error?.message || `HTTP ${response.status}`);
  }
  const blob = await response.blob();
  const filename = format === "yaml" ? "tproxy-export.yaml" : "tproxy-export.json";
  return { blob, filename };
}

export function importConfig(secret: string, file: File) {
  const contentType = file.name.endsWith(".yaml") || file.name.endsWith(".yml")
    ? "application/yaml"
    : "application/json";
  return adminFetch<{ ok: boolean; database?: string }>(secret, "/api/admin/config/import", {
    method: "POST",
    headers: { "Content-Type": contentType },
    body: file,
  });
}

export function changeDashboardPassword(secret: string, currentPassword: string, newPassword: string) {
  return adminFetch<{ ok: boolean }>(secret, "/api/admin/settings/dashboard-password", {
    method: "PUT",
    body: JSON.stringify({
      current_password: currentPassword,
      new_password: newPassword,
    }),
  });
}

export function saveGatewaySettings(secret: string, allowLanManagement: boolean) {
  return adminFetch<{
    ok: boolean;
    allow_lan_management: boolean;
    server_host: string;
    server_port: number;
    restart_required?: boolean;
  }>(secret, "/api/admin/settings/gateway", {
    method: "PATCH",
    body: JSON.stringify({ allow_lan_management: allowLanManagement }),
  });
}
