export type TunnelDownloadStatus = {
  downloading: boolean;
  progress: number;
};

export type CloudflareTunnelStatus = {
  enabled: boolean;
  settingsEnabled: boolean;
  tunnelUrl?: string;
  shortId?: string;
  publicUrl?: string;
  running: boolean;
  connected?: boolean;
  reachable?: boolean;
};

export type TailscaleTunnelStatus = {
  enabled: boolean;
  settingsEnabled: boolean;
  tunnelUrl?: string;
  running: boolean;
  loggedIn: boolean;
  reachable?: boolean;
};

export type TunnelStatusResponse = {
  tunnel: CloudflareTunnelStatus;
  tailscale: TailscaleTunnelStatus;
  download: TunnelDownloadStatus;
};

export type EnableTunnelResponse = {
  success: boolean;
  tunnelUrl?: string;
  shortId?: string;
  publicUrl?: string;
  alreadyRunning?: boolean;
  error?: string;
};

export type TailscaleEnableResponse = {
  success: boolean;
  tunnelUrl?: string;
  needsLogin?: boolean;
  authUrl?: string;
  funnelNotEnabled?: boolean;
  enableUrl?: string;
  error?: string;
};

export type TailscaleCheckResponse = {
  installed: boolean;
  logged_in: boolean;
  running: boolean;
};

async function adminFetch<T>(secret: string, path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: {
      ...(secret ? { Authorization: `Bearer ${secret}` } : {}),
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
  });
  const contentType = response.headers.get("content-type") || "";
  if (!response.ok) {
    if (contentType.includes("json")) {
      const data = await response.json();
      throw new Error(data?.error?.message || data?.error || `HTTP ${response.status}`);
    }
    throw new Error(`HTTP ${response.status}`);
  }
  if (contentType.includes("json")) {
    return (await response.json()) as T;
  }
  return {} as T;
}

export function fetchTunnelStatus(secret: string) {
  return adminFetch<TunnelStatusResponse>(secret, "/api/admin/tunnel/status", { cache: "no-store" });
}

export function enableTunnel(secret: string) {
  return adminFetch<EnableTunnelResponse>(secret, "/api/admin/tunnel/enable", { method: "POST" });
}

export function disableTunnel(secret: string) {
  return adminFetch<{ success: boolean }>(secret, "/api/admin/tunnel/disable", { method: "POST" });
}

export function checkTailscale(secret: string) {
  return adminFetch<TailscaleCheckResponse>(secret, "/api/admin/tunnel/tailscale-check");
}

export function enableTailscale(secret: string) {
  return adminFetch<TailscaleEnableResponse>(secret, "/api/admin/tunnel/tailscale-enable", { method: "POST" });
}

export function disableTailscale(secret: string) {
  return adminFetch<{ success: boolean }>(secret, "/api/admin/tunnel/tailscale-disable", { method: "POST" });
}

export function saveTunnelDashboardAccess(secret: string, enabled: boolean) {
  return adminFetch<{ tunnel_dashboard_access: boolean }>(secret, "/api/admin/tunnel/dashboard-access", {
    method: "PATCH",
    body: JSON.stringify({ tunnel_dashboard_access: enabled }),
  });
}

export async function pingHealth(baseUrl: string): Promise<boolean> {
  const target = `${baseUrl.replace(/\/+$/, "")}/healthz`;
  try {
    const response = await fetch(target, { mode: "cors", cache: "no-store", signal: AbortSignal.timeout(8000) });
    if (response.ok) return true;
  } catch {
    // CORS or network error — fall through to opaque probe.
  }
  try {
    await fetch(target, { mode: "no-cors", cache: "no-store", signal: AbortSignal.timeout(8000) });
    return true;
  } catch {
    return false;
  }
}

export async function pingAnyHealth(...urls: Array<string | undefined>): Promise<boolean> {
  const targets = urls.filter(Boolean) as string[];
  if (targets.length === 0) return false;
  const results = await Promise.all(targets.map((url) => pingHealth(url)));
  return results.some(Boolean);
}
