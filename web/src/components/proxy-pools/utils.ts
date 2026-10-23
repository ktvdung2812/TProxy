import type { Tone } from "../page-kit";

/** The fields of a proxy pool every view needs (API list and snapshot agree on them). */
export type PoolLike = {
  id: string;
  name: string;
  enabled: boolean;
  status: string;
  last_error?: string;
  usage_count: number;
};

export type PoolHealth = "healthy" | "failing" | "untested" | "off";

export const POOL_HEALTH_ORDER: PoolHealth[] = ["failing", "untested", "healthy", "off"];

export const POOL_HEALTH_TONE: Record<PoolHealth, Tone> = {
  healthy: "success",
  failing: "danger",
  untested: "neutral",
  off: "muted",
};

/** A pool that is off is ignored at runtime, so its last test result no longer matters. */
export function poolHealth(pool: Pick<PoolLike, "enabled" | "status">): PoolHealth {
  if (!pool.enabled) return "off";
  const status = (pool.status || "").trim().toLowerCase();
  if (status === "healthy" || status === "active") return "healthy";
  if (!status || status === "unknown") return "untested";
  return "failing";
}

export function parsePoolFilter(raw: string | null): PoolHealth | "all" {
  const value = raw?.trim().toLowerCase() || "";
  return (POOL_HEALTH_ORDER as string[]).includes(value) ? (value as PoolHealth) : "all";
}

/** Host and port of a proxy URL without its credentials, for compact display. */
export function proxyEndpoint(url: string) {
  try {
    const parsed = new URL(url);
    return { scheme: parsed.protocol.replace(/:$/, ""), host: parsed.port ? `${parsed.hostname}:${parsed.port}` : parsed.hostname };
  } catch {
    return { scheme: "", host: url };
  }
}

export function formatDateTime(value?: string) {
  if (!value) return "Never";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "Never";
  return date.toLocaleString();
}

export function statusVariant(status?: string): "success" | "error" | "default" {
  if (status === "healthy" || status === "active") return "success";
  if (status === "error") return "error";
  return "default";
}

export function suggestPoolId(name: string) {
  const base = name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
  return base || `proxy-pool-${Date.now()}`;
}

export function parseProxyLine(line: string) {
  const trimmed = line.trim();
  if (!trimmed) return null;

  if (trimmed.includes("://")) {
    const parsed = new URL(trimmed);
    const hostLabel = parsed.port ? `${parsed.hostname}:${parsed.port}` : parsed.hostname;
    return {
      proxyUrl: parsed.toString(),
      name: `Imported ${hostLabel}`,
    };
  }

  const parts = trimmed.split(":");
  if (parts.length === 4) {
    const [host, port, username, password] = parts;
    if (!host || !port || !username || !password) {
      throw new Error("Invalid host:port:user:pass format");
    }
    const proxyUrl = `http://${encodeURIComponent(username)}:${encodeURIComponent(password)}@${host}:${port}`;
    return {
      proxyUrl: new URL(proxyUrl).toString(),
      name: `Imported ${host}:${port}`,
    };
  }

  throw new Error("Unsupported format");
}
