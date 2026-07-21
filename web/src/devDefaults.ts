/** Official local-dev management secret — must match TPROXY_MANAGEMENT_SECRET in .env.example */
export const DEV_MANAGEMENT_SECRET = "tproxy-local-management-secret";

/** Official local-dev client API key — must match TPROXY_API_KEY in .env.example */
export const DEV_API_KEY = "tproxy-local-dev-key";

export function isLocalDashboardHost(): boolean {
  if (typeof window === "undefined") return false;
  const host = window.location.hostname;
  return host === "localhost" || host === "127.0.0.1";
}

/** Default secret for the embedded dashboard on loopback during local development. */
export function defaultManagementSecret(): string {
  const fromRun = import.meta.env.VITE_TPROXY_MANAGEMENT_SECRET;
  if (fromRun) return fromRun;
  return isLocalDashboardHost() ? DEV_MANAGEMENT_SECRET : "";
}

/** Default client API key for chat on loopback during local development. */
export function defaultApiKey(): string {
  const fromRun = import.meta.env.VITE_TPROXY_API_KEY;
  if (fromRun) return fromRun;
  return isLocalDashboardHost() ? DEV_API_KEY : "";
}

/**
 * Resolve the chat playground client API key.
 * Prefer a stored key unless it is blank or accidentally set to the management secret.
 */
export function resolveChatApiKey(): string {
  const stored = typeof localStorage !== "undefined" ? localStorage.getItem("tproxy-api-key") || "" : "";
  const fallback = defaultApiKey();
  const management = defaultManagementSecret();
  if (stored && stored !== management) return stored;
  if (fallback) {
    if (typeof localStorage !== "undefined" && stored === management) {
      localStorage.setItem("tproxy-api-key", fallback);
    }
    return fallback;
  }
  return stored;
}
