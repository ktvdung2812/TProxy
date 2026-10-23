import type { CredentialQuota, QuotaEntry } from "./api";

export const REFRESH_INTERVAL_MS = 60000;
// Claude's usage endpoint rate-limits aggressively, so its auto-refresh is slower.
export const CLAUDE_REFRESH_INTERVAL_MS = 180000;
export const QUOTA_REFRESH_CONCURRENCY = 4;
export const DEPLETED_QUOTA_THRESHOLD = 5;
export const LOW_QUOTA_THRESHOLD = 30;
export const AUTO_REFRESH_STORAGE_KEY = "tproxy.quotaAutoRefresh";
export const QUOTA_VISIBILITY_KEY = "tproxy.quotaVisibility";
export const QUOTA_VIEW_MODE_KEY = "tproxy.quotaViewMode";
export const QUOTA_GUIDE_DISMISSED_KEY = "tproxy.quotaGuideDismissed";

// "active" is no longer offered in the UI but still parsed so old links work.
export const ACCOUNT_FILTERS = ["all", "available", "low", "attention", "inactive", "active"] as const;

export type AccountFilter = (typeof ACCOUNT_FILTERS)[number];

/**
 * What an account looks like to the router right now, from the operator's
 * point of view. "loading" only lasts until the first quota request settles.
 */
export type AccountHealth =
  | "loading"
  | "ready"
  | "low"
  | "exhausted"
  | "cooldown"
  | "paused"
  | "error"
  | "nodata"
  | "off";

export type AccountHealthBucket = "available" | "low" | "attention" | "off";

export function accountHealthBucket(health: AccountHealth): AccountHealthBucket | null {
  switch (health) {
    case "ready":
    case "nodata":
      return "available";
    case "low":
      return "low";
    case "exhausted":
    case "cooldown":
    case "paused":
    case "error":
      return "attention";
    case "off":
      return "off";
    default:
      return null;
  }
}

export function matchesAccountFilter(filter: AccountFilter, health: AccountHealth, enabled: boolean) {
  if (filter === "all") return true;
  if (filter === "active") return enabled;
  if (filter === "inactive") return !enabled;
  return accountHealthBucket(health) === filter;
}

export function getConnectionLabel(credential: { label?: string; email?: string; id: string }) {
  return credential.label?.trim() || credential.email?.trim() || null;
}

export type QuotaAccountSortItem = {
  id: string;
  providerId: string;
  enabled: boolean;
  priority?: number;
  label?: string;
  email?: string;
};

export function compareQuotaAccountOrder<T extends QuotaAccountSortItem>(
  a: T,
  b: T,
  options: {
    providerKey: (item: T) => string;
    providerCounts: Record<string, number>;
    providerOrder?: Record<string, string[]>;
    resetAtById?: Record<string, number>;
  },
): number {
  const providerA = options.providerKey(a);
  const providerB = options.providerKey(b);
  if (a.providerId === b.providerId) {
    if (options.resetAtById) {
      const diff = (options.resetAtById[a.id] ?? Number.POSITIVE_INFINITY) - (options.resetAtById[b.id] ?? Number.POSITIVE_INFINITY);
      if (diff !== 0) return diff;
    }
    const providerOrder = options.providerOrder?.[a.providerId];
    if (providerOrder) {
      const leftIndex = providerOrder.indexOf(a.id);
      const rightIndex = providerOrder.indexOf(b.id);
      if (leftIndex >= 0 && rightIndex >= 0 && leftIndex !== rightIndex) return leftIndex - rightIndex;
    }
    const priorityDiff = (b.priority ?? 0) - (a.priority ?? 0);
    if (priorityDiff !== 0) return priorityDiff;
    return (getConnectionLabel(a) || a.id).localeCompare(getConnectionLabel(b) || b.id);
  }
  if (a.enabled !== b.enabled) return a.enabled ? -1 : 1;
  const countDiff = (options.providerCounts[providerB] || 0) - (options.providerCounts[providerA] || 0);
  if (countDiff !== 0) return countDiff;
  if (providerA !== providerB) return providerA.localeCompare(providerB);
  return (getConnectionLabel(a) || a.id).localeCompare(getConnectionLabel(b) || b.id);
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

// Grok's per-product bars are slices of its weekly allowance, already counted
// by the aggregate window.
const GROK_PRODUCT_BREAKDOWN_PREFIX = "product_";

function isGrokQuotaProviderType(providerType: string) {
  return providerType === "xai" || providerType === "grok-cli";
}

// Keep in step with quotaKeyAffectsRouting in internal/providers/quota_state.go:
// the dashboard's depleted badge and the router's skip decision must agree, or
// an account shows as available while requests are being routed away from it.
export function quotaKeyAffectsRouting(providerType: string, key: string) {
  const normalizedKey = key.trim().toLowerCase();
  const normalizedType = providerType.trim().toLowerCase();
  if (normalizedKey.startsWith(GROK_PRODUCT_BREAKDOWN_PREFIX)) return false;
  // Grok has no session window, so its weekly allowance is the real limit
  // rather than an auxiliary one.
  if (isGrokQuotaProviderType(normalizedType)) return normalizedKey !== "review";
  // Devin's daily and weekly windows are both hard account limits — a spent
  // weekly window blocks requests until reset, so it is not display-only.
  if (normalizedType === "devin") return true;
  // OpenCode Go's 5-hour, weekly and monthly windows are all hard dollar caps —
  // a spent window blocks paid models until it resets.
  if (normalizedType === "opencode-go") return true;
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

// Keep in step with quotaKeyAutoDisablesAtZero in internal/providers/quota_state.go.
function quotaKeyAutoDisablesAtZero(providerType: string, key: string) {
  const normalizedKey = key.trim().toLowerCase();
  if (normalizedKey === "session" || normalizedKey === "weekly") return true;
  const normalizedType = providerType.trim().toLowerCase();
  if (normalizedType === "opencode-go" && normalizedKey === "monthly") return true;
  return normalizedType === "devin" && normalizedKey === "daily";
}

const CREDIT_RESCUE_KEYS: Record<string, string[]> = {
  claude: ["extra_usage", "spend"],
  xai: ["prepaid", "on_demand"],
  "grok-cli": ["prepaid", "on_demand"],
};

/**
 * Port of QuotaAtZero in internal/providers/quota_state.go: whether the router
 * considers the whole account spent. A spent session or primary weekly window
 * is enough (unless paid credit keeps it serving); otherwise every routing
 * window has to be empty, so one exhausted Antigravity model does not count.
 */
export function isQuotaExhausted(quota?: CredentialQuota, providerType = quota?.provider_type || "") {
  const entries = quotaEntries(quota);
  if (entries.length === 0) return false;
  const type = providerType.trim().toLowerCase();
  const rescued = (CREDIT_RESCUE_KEYS[type] || []).some((key) => {
    const entry = entries.find((item) => item.key === key);
    return Boolean(entry && !entry.unlimited && entry.total > 0 && entry.remaining > 0);
  });
  let hasRoutingWindow = false;
  let hasAvailableRoutingWindow = false;
  for (const entry of entries) {
    if (entry.unlimited || entry.total <= 0) continue;
    if (quotaKeyAutoDisablesAtZero(type, entry.key) && entry.remaining <= 0 && !rescued) return true;
    if (!quotaKeyAffectsRouting(type, entry.key)) continue;
    hasRoutingWindow = true;
    if (entry.remaining > 0) hasAvailableRoutingWindow = true;
  }
  return hasRoutingWindow && !hasAvailableRoutingWindow;
}

/** Lowest remaining percentage across limited windows, or null when none are limited. */
export function lowestRemaining(entries: Array<{ remaining: number; unlimited?: boolean; total: number }>) {
  let lowest: number | null = null;
  for (const entry of entries) {
    if (entry.unlimited || entry.total <= 0) continue;
    if (lowest === null || entry.remaining < lowest) lowest = entry.remaining;
  }
  return lowest;
}

export function getAccountHealth(input: {
  enabled: boolean;
  quota?: CredentialQuota;
  providerType: string;
  error?: string;
  settled: boolean;
  onCooldown: boolean;
}): AccountHealth {
  if (!input.enabled) return "off";
  if (input.quota?.quota_auto_disabled) return "paused";
  if (input.onCooldown) return "cooldown";
  if (input.error) return "error";
  if (!input.quota) return input.settled ? "nodata" : "loading";
  const entries = quotaEntries(input.quota);
  if (entries.length === 0) return "nodata";
  if (isQuotaExhausted(input.quota, input.providerType)) return "exhausted";
  const lowest = lowestRemaining(entries);
  if (lowest !== null && lowest < LOW_QUOTA_THRESHOLD) return "low";
  return "ready";
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

export function formatResetAbsolute(date?: string) {
  if (!date) return null;
  const resetDate = new Date(date);
  if (Number.isNaN(resetDate.getTime())) return null;

  const hours = String(resetDate.getHours()).padStart(2, "0");
  const minutes = String(resetDate.getMinutes()).padStart(2, "0");
  const day = String(resetDate.getDate()).padStart(2, "0");
  const month = String(resetDate.getMonth() + 1).padStart(2, "0");
  const year = resetDate.getFullYear();
  return `${hours}:${minutes} ${day}/${month}/${year}`;
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

export function findEarliestQuotaReset(
  entries: Array<{ key: string; name: string; reset_at?: string }>,
) {
  let best: { resetAt: string; name: string; time: number } | null = null;
  for (const entry of entries) {
    if (!entry.reset_at) continue;
    const time = new Date(entry.reset_at).getTime();
    if (Number.isNaN(time) || time <= Date.now()) continue;
    if (!best || time < best.time) {
      best = { resetAt: entry.reset_at, name: entry.name || entry.key, time };
    }
  }
  return best;
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

export const QUOTA_URL_PARAMS = {
  provider: "provider",
  status: "status",
  sort: "sort",
} as const;

export type QuotaUrlSort = "default" | "expiring" | "lowest";

export function parseQuotaAccountFilter(raw: string | null): AccountFilter {
  const value = raw?.trim().toLowerCase();
  return (ACCOUNT_FILTERS as readonly string[]).includes(value || "") ? (value as AccountFilter) : "all";
}

export function parseQuotaSort(raw: string | null): QuotaUrlSort {
  const value = raw?.trim().toLowerCase();
  return value === "expiring" || value === "lowest" ? value : "default";
}

export function resolveQuotaProviderFilter(raw: string | null, providerOptions: string[]): string {
  const value = raw?.trim().toLowerCase() || "";
  if (!value || value === "all") return "all";
  if (providerOptions.length === 0) return value;
  return providerOptions.includes(value) ? value : "all";
}

export function patchQuotaSearchParams(
  current: URLSearchParams,
  patch: Partial<{ provider: string; status: AccountFilter; sort: QuotaUrlSort }>,
): URLSearchParams {
  const next = new URLSearchParams(current);
  if (patch.provider !== undefined) {
    const provider = patch.provider.trim().toLowerCase();
    if (!provider || provider === "all") next.delete(QUOTA_URL_PARAMS.provider);
    else next.set(QUOTA_URL_PARAMS.provider, provider);
  }
  if (patch.status !== undefined) {
    if (!patch.status || patch.status === "all") next.delete(QUOTA_URL_PARAMS.status);
    else next.set(QUOTA_URL_PARAMS.status, patch.status);
  }
  if (patch.sort !== undefined) {
    if (patch.sort === "default") next.delete(QUOTA_URL_PARAMS.sort);
    else next.set(QUOTA_URL_PARAMS.sort, patch.sort);
  }
  return next;
}

/**
 * Providers that meter each model separately are shown as a grid of rings
 * rather than a list of bars: the list grows with the model count and puts
 * long model names on the same line as their percentage, where they collide.
 */
export function usesQuotaRingLayout(quotaKey: string) {
  return quotaKey.trim().toLowerCase() === "antigravity";
}

/**
 * Grok meters several products against one weekly pool, so its quota reads as a
 * single segmented bar — the shape x.ai's own usage page uses — rather than one
 * bar per product.
 */
export function usesQuotaStackedLayout(quotaKey: string) {
  const normalized = quotaKey.trim().toLowerCase();
  return normalized === "xai" || normalized === "grok-cli";
}
