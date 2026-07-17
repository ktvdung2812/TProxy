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
  return isLocalDashboardHost() ? DEV_MANAGEMENT_SECRET : "";
}

/** Default client API key for chat on loopback during local development. */
export function defaultApiKey(): string {
  return isLocalDashboardHost() ? DEV_API_KEY : "";
}
