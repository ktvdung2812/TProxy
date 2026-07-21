const STORAGE_KEY = "tproxy.public-base-url";

export function normalizePublicOrigin(origin: string): string {
  const trimmed = origin.trim().replace(/\/+$/, "");
  if (!trimmed) return "";
  return trimmed.endsWith("/v1") ? trimmed.slice(0, -3) : trimmed;
}

export function normalizeCliBaseUrl(origin: string): string {
  const trimmed = origin.trim().replace(/\/+$/, "");
  if (!trimmed) return "";
  return trimmed.endsWith("/v1") ? trimmed : `${trimmed}/v1`;
}

export function isLocalDashboardHost(hostname: string): boolean {
  return hostname === "localhost" || hostname === "127.0.0.1" || hostname === "::1";
}

export function readStoredPublicBaseUrl(): string {
  if (typeof window === "undefined") return "";
  try {
    return normalizePublicOrigin(localStorage.getItem(STORAGE_KEY) || "");
  } catch {
    return "";
  }
}

export function storePublicBaseUrl(value: string) {
  if (typeof window === "undefined") return;
  const normalized = normalizePublicOrigin(value);
  try {
    if (normalized) localStorage.setItem(STORAGE_KEY, normalized);
    else localStorage.removeItem(STORAGE_KEY);
  } catch {
    /* ignore */
  }
}

export function resolveCliBaseUrl(serverPublicBaseUrl?: string): string {
  const configured = normalizePublicOrigin(serverPublicBaseUrl || "");
  if (configured) return normalizeCliBaseUrl(configured);

  const stored = readStoredPublicBaseUrl();
  if (stored) return normalizeCliBaseUrl(stored);

  if (typeof window !== "undefined") {
    const host = window.location.hostname;
    if (!isLocalDashboardHost(host)) {
      return normalizeCliBaseUrl(window.location.origin);
    }
  }
  return normalizeCliBaseUrl(typeof window !== "undefined" ? window.location.origin : "http://localhost:28120");
}

export function needsPublicBaseUrlForCliTools(serverPublicBaseUrl?: string): boolean {
  const configured = normalizePublicOrigin(serverPublicBaseUrl || "") || readStoredPublicBaseUrl();
  if (configured) return false;
  if (typeof window === "undefined") return true;
  return isLocalDashboardHost(window.location.hostname);
}
