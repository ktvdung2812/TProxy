import { getStoredApiKeySecret } from "./lib/apiKeySecrets";

type ApiKeyOption = {
  id: string;
  enabled: boolean;
};

export function isLocalDashboardHost(): boolean {
  if (typeof window === "undefined") return false;
  const host = window.location.hostname;
  return host === "localhost" || host === "127.0.0.1" || host === "::1";
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
  const disallowed = new Set([managementSecret.trim()]);

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
