import type { CredentialQuota, QuotaEntry } from "./api";

export const REFRESH_INTERVAL_MS = 60000;
export const QUOTA_REFRESH_CONCURRENCY = 4;
export const DEPLETED_QUOTA_THRESHOLD = 5;
export const AUTO_REFRESH_STORAGE_KEY = "tproxy.quotaAutoRefresh";
export const QUOTA_VISIBILITY_KEY = "tproxy.quotaVisibility";

export const ACCOUNT_FILTER_OPTIONS = [
  { value: "all", label: "All accounts" },
  { value: "active", label: "Active" },
  { value: "inactive", label: "Turned off" },
] as const;

export type AccountFilter = (typeof ACCOUNT_FILTER_OPTIONS)[number]["value"];

export function getConnectionLabel(credential: { label?: string; email?: string; id: string }) {
  return credential.label?.trim() || credential.email?.trim() || null;
}

export function buildProviderCountMap<T>(
  items: T[],
  providerKey: (item: T) => string,
): Record<string, number> {
  const counts: Record<string, number> = {};
  for (const item of items) {
    const key = providerKey(item);
    counts[key] = (counts[key] || 0) + 1;
  }
  return counts;
}

export function calculatePercentage(used: number, total: number) {
  if (!total || total <= 0) return 0;
  if (!used || used < 0) return 100;
  if (used >= total) return 0;
  return Math.round(((total - used) / total) * 100);
}

export function getRemainingPercentage(entry: QuotaEntry) {
  if (entry.unlimited || entry.total <= 0) return 100;
  return calculatePercentage(entry.used, entry.total);
}

const AUXILIARY_QUOTA_KEY = /weekly|review/i;

export function quotaKeyAffectsRouting(providerType: string, key: string) {
  const normalizedKey = key.trim().toLowerCase();
  const normalizedType = providerType.trim().toLowerCase();
  if (AUXILIARY_QUOTA_KEY.test(normalizedKey)) return false;
  if (normalizedType === "codex") return normalizedKey === "session";
  return true;
}

export function isConnectionAtZero(quota?: CredentialQuota, providerType = quota?.provider_type || "") {
  return quotaEntries(quota).some((entry) => {
    if (!quotaKeyAffectsRouting(providerType, entry.key)) return false;
    if (entry.unlimited || entry.total <= 0) return false;
    return entry.remaining <= 0;
  });
}

export function formatResetTime(date?: string) {
  if (!date) return "-";
  const resetDate = new Date(date);
  if (Number.isNaN(resetDate.getTime())) return "-";
  const diffMs = resetDate.getTime() - Date.now();
  if (diffMs <= 0) return "-";

  const totalMinutes = Math.ceil(diffMs / 60000);
  if (totalMinutes < 60) return `${totalMinutes}m`;

  const totalHours = Math.floor(totalMinutes / 60);
  const remainingMinutes = totalMinutes % 60;
  if (totalHours < 24) return `${totalHours}h ${remainingMinutes}m`;

  const days = Math.floor(totalHours / 24);
  const hours = totalHours % 24;
  return `${days}d ${hours}h ${remainingMinutes}m`;
}

export function quotaEntries(quota?: CredentialQuota) {
  return Object.entries(quota?.quotas || {}).map(([key, entry]) => ({
    key,
    ...entry,
    name: entry.name || key,
    remaining: getRemainingPercentage(entry),
  }));
}

export function earliestResetAt(quota?: CredentialQuota) {
  const times = quotaEntries(quota)
    .map((entry) => (entry.reset_at ? new Date(entry.reset_at).getTime() : Number.POSITIVE_INFINITY))
    .filter(Number.isFinite);
  return times.length > 0 ? Math.min(...times) : Number.POSITIVE_INFINITY;
}

export function isConnectionDepleted(quota?: CredentialQuota) {
  return quotaEntries(quota).some((entry) => {
    if (entry.unlimited || entry.total <= 0) return false;
    return entry.remaining <= DEPLETED_QUOTA_THRESHOLD;
  });
}

export function getQuotaVisibilityKey(entry: { key?: string; name?: string }) {
  return String(entry.key || entry.name || "").trim();
}

export type QuotaVisibility = Record<string, { hidden?: string[] }>;

export function filterQuotasByVisibility(
  providerType: string,
  entries: ReturnType<typeof quotaEntries>,
  visibility: QuotaVisibility,
) {
  const hidden = new Set(visibility[providerType]?.hidden || []);
  if (hidden.size === 0) return entries;
  return entries.filter((entry) => !hidden.has(getQuotaVisibilityKey(entry)));
}

export function getHiddenQuotaRows(
  providerType: string,
  entries: ReturnType<typeof quotaEntries>,
  visibility: QuotaVisibility,
) {
  const hidden = new Set(visibility[providerType]?.hidden || []);
  if (hidden.size === 0) return [];
  return entries.filter((entry) => hidden.has(getQuotaVisibilityKey(entry)));
}

export function getColorTone(remaining: number) {
  if (remaining > 70) return "success" as const;
  if (remaining >= 30) return "warning" as const;
  return "danger" as const;
}

export function formatQuotaName(name: string) {
  return name
    .trim()
    .replace(/_/g, " ")
    .replace(/\b\w/g, (char) => char.toUpperCase());
}

export function getColorEmoji(remaining: number) {
  if (remaining > 70) return "🟢";
  if (remaining >= 30) return "🟡";
  return "🔴";
}

const compactNumber = new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 });

export function formatProxyUsageLabel(usage?: { requests: number; promptTokens: number; completionTokens: number }) {
  if (!usage) return null;
  const tokens = (usage.promptTokens || 0) + (usage.completionTokens || 0);
  return `${compactNumber.format(usage.requests || 0)} req · ${compactNumber.format(tokens)} tok`;
}

export async function runWithConcurrency<T, R>(
  items: T[],
  worker: (item: T) => Promise<R>,
  concurrency = QUOTA_REFRESH_CONCURRENCY,
): Promise<R[]> {
  if (items.length === 0) return [];
  const results: R[] = new Array(items.length);
  let nextIndex = 0;
  async function runWorker() {
    while (true) {
      const index = nextIndex;
      nextIndex += 1;
      if (index >= items.length) return;
      results[index] = await worker(items[index]);
    }
  }
  const workers = Array.from({ length: Math.min(concurrency, items.length) }, () => runWorker());
  await Promise.all(workers);
  return results;
}
