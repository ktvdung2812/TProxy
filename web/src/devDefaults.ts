import { getStoredApiKeySecret } from "./lib/apiKeySecrets";

/** Official local-dev management secret — must match TPROXY_MANAGEMENT_SECRET in .env.example */
export const DEV_MANAGEMENT_SECRET = "tproxy-local-management-secret";

/**
 * A historical dashboard fallback. It must never be sent automatically: a
 * running gateway may use a different client-key store than .env.example.
 */
const LEGACY_DEV_API_KEY = "tproxy-local-dev-key";

type ApiKeyOption = {
  id: string;
  enabled: boolean;
};

export function isLocalDashboardHost(): boolean {
  if (typeof window === "undefined") return false;
  const host = window.location.hostname;
  return host === "localhost" || host === "127.0.0.1";
}

/** Default secret for the embedded dashboard on loopback during local development. */
export function defaultManagementSecret(): string {
  return isLocalDashboardHost() ? DEV_MANAGEMENT_SECRET : "";
}

/**
 * Resolve the chat playground client API key.
 *
 * Client keys are installation-specific. Prefer a manually saved client key,
 * then a secret saved for an enabled API-key record. Never substitute an
 * example key or the dashboard management secret.
 */
export function resolveChatApiKey(apiKeys: ApiKeyOption[], managementSecret = ""): string {
  const stored = typeof localStorage !== "undefined" ? localStorage.getItem("tproxy-api-key")?.trim() || "" : "";
  const disallowed = new Set([managementSecret.trim(), DEV_MANAGEMENT_SECRET, LEGACY_DEV_API_KEY]);

  if (stored && !disallowed.has(stored)) return stored;

  if (stored && typeof localStorage !== "undefined") {
    localStorage.removeItem("tproxy-api-key");
  }

  for (const apiKey of apiKeys) {
    if (!apiKey.enabled) continue;
    const savedSecret = getStoredApiKeySecret(apiKey.id);
    if (savedSecret && !disallowed.has(savedSecret)) return savedSecret;
  }
  return "";
}
