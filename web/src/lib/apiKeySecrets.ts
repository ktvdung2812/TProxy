const STORAGE_KEY = "tproxy-api-key-secrets";

function readAll(): Record<string, string> {
  if (typeof window === "undefined") return {};
  try {
    return JSON.parse(window.localStorage.getItem(STORAGE_KEY) || "{}") as Record<string, string>;
  } catch {
    return {};
  }
}

export function getStoredApiKeySecret(id: string): string | undefined {
  const secret = readAll()[id]?.trim();
  return secret || undefined;
}

export function storeApiKeySecret(id: string, secret: string): void {
  if (typeof window === "undefined" || !id.trim() || !secret.trim()) return;
  const all = readAll();
  all[id] = secret.trim();
  window.localStorage.setItem(STORAGE_KEY, JSON.stringify(all));
}

export function removeStoredApiKeySecret(id: string): void {
  if (typeof window === "undefined") return;
  const all = readAll();
  delete all[id];
  window.localStorage.setItem(STORAGE_KEY, JSON.stringify(all));
}
