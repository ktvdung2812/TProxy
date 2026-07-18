const STORAGE_KEY = "tproxy-api-key-secrets";

const listeners = new Set<() => void>();
let snapshotVersion = 0;
let cachedSnapshot: Record<string, string> = readAll();

function readAll(): Record<string, string> {
  if (typeof window === "undefined") return {};
  try {
    return JSON.parse(window.localStorage.getItem(STORAGE_KEY) || "{}") as Record<string, string>;
  } catch {
    return {};
  }
}

function refreshSnapshot(): void {
  cachedSnapshot = readAll();
  snapshotVersion += 1;
}

function emit(): void {
  refreshSnapshot();
  for (const listener of listeners) {
    listener();
  }
}

export function subscribeApiKeySecrets(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** Stable version token for useSyncExternalStore — must not allocate new objects per call. */
export function getApiKeySecretsVersion(): number {
  return snapshotVersion;
}

export function getStoredApiKeySecret(id: string): string | undefined {
  const secret = cachedSnapshot[id]?.trim();
  return secret || undefined;
}

export function maskApiKeySecret(secret: string): string {
  const trimmed = secret.trim();
  if (trimmed.length <= 8) return trimmed;
  return `${trimmed.slice(0, 4)}••••${trimmed.slice(-4)}`;
}

export function storeApiKeySecret(id: string, secret: string): void {
  if (typeof window === "undefined" || !id.trim() || !secret.trim()) return;
  const trimmedId = id.trim();
  const trimmedSecret = secret.trim();
  if (cachedSnapshot[trimmedId] === trimmedSecret) return;
  const all = { ...cachedSnapshot, [trimmedId]: trimmedSecret };
  window.localStorage.setItem(STORAGE_KEY, JSON.stringify(all));
  emit();
}

export function removeStoredApiKeySecret(id: string): void {
  if (typeof window === "undefined" || !id.trim()) return;
  if (!(id in cachedSnapshot)) return;
  const all = { ...cachedSnapshot };
  delete all[id];
  window.localStorage.setItem(STORAGE_KEY, JSON.stringify(all));
  emit();
}

type NineRouterApiKey = {
  id?: string;
  key?: string;
  name?: string;
};

type NineRouterBackup = {
  apiKeys?: NineRouterApiKey[];
};

function slugify(value: string): string {
  return value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

function firstNonEmpty(...values: Array<string | undefined>): string {
  for (const value of values) {
    const trimmed = value?.trim();
    if (trimmed) return trimmed;
  }
  return "";
}

/** Persist client API key secrets from a 9router backup into this browser. */
export function storeApiKeySecretsFrom9routerBackup(payload: unknown): number {
  const backup = payload as NineRouterBackup;
  let count = 0;
  for (const key of backup.apiKeys || []) {
    const secret = key.key?.trim();
    if (!secret) continue;
    const id = firstNonEmpty(key.id, slugify(key.name || ""));
    if (!id) continue;
    storeApiKeySecret(id, secret);
    count += 1;
  }
  return count;
}
