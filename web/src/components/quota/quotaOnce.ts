import { fetchCredentialQuota, type CredentialQuota } from "./api";

export type CachedQuota = {
  quota?: CredentialQuota;
  error?: string;
};

const cache = new Map<string, CachedQuota>();
const inflight = new Map<string, Promise<CachedQuota>>();

export function peekCachedQuota(credentialId: string): CachedQuota | undefined {
  return cache.get(credentialId);
}

export function clearCachedQuota(credentialIds?: string[]) {
  if (!credentialIds) {
    cache.clear();
    inflight.clear();
    return;
  }
  for (const id of credentialIds) {
    cache.delete(id);
    inflight.delete(id);
  }
}

export function loadCredentialQuotaOnce(secret: string, credentialId: string, force = false): Promise<CachedQuota> {
  if (!force) {
    const cached = cache.get(credentialId);
    if (cached) return Promise.resolve(cached);
    const pending = inflight.get(credentialId);
    if (pending) return pending;
  }
  const request = fetchCredentialQuota(secret, credentialId)
    .then((quota) => {
      const entry: CachedQuota = { quota };
      cache.set(credentialId, entry);
      return entry;
    })
    .catch((cause) => {
      const entry: CachedQuota = { error: cause instanceof Error ? cause.message : "Quota check failed" };
      cache.set(credentialId, entry);
      return entry;
    })
    .finally(() => {
      inflight.delete(credentialId);
    });
  inflight.set(credentialId, request);
  return request;
}
