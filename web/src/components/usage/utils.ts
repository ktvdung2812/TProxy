import type { UsageBucketEntry } from "./api";

export const USAGE_PERIODS = [
  { value: "today", label: "Today" },
  { value: "24h", label: "24h" },
  { value: "7d", label: "7D" },
  { value: "30d", label: "30D" },
  { value: "60d", label: "60D" },
] as const;

export type UsageTableView = "model" | "account" | "apiKey" | "endpoint";
export type UsageValueMode = "tokens" | "costs";

export const fmt = (value: number) => new Intl.NumberFormat().format(value || 0);
export const fmtCost = (value: number) => `$${(value || 0).toFixed(2)}`;

export function fmtTime(iso?: string) {
  if (!iso) return "Never";
  const diffMins = Math.floor((Date.now() - new Date(iso).getTime()) / 60000);
  if (diffMins < 1) return "Just now";
  if (diffMins < 60) return `${diffMins}m ago`;
  if (diffMins < 1440) return `${Math.floor(diffMins / 60)}h ago`;
  return new Date(iso).toLocaleDateString();
}

export type UsageRow = UsageBucketEntry & {
  key: string;
  totalTokens: number;
  totalCost: number;
  inputCost: number;
  cachedCost: number;
  outputCost: number;
};

export function enrichUsageRows(data: Record<string, UsageBucketEntry>): UsageRow[] {
  return Object.entries(data).map(([key, item]) => {
    const totalTokens = (item.promptTokens || 0) + (item.completionTokens || 0);
    const totalCost = item.cost || 0;
    const cachedTokens = item.cachedTokens || 0;
    const nonCachedInput = Math.max(0, (item.promptTokens || 0) - cachedTokens);
    const inputCost = totalTokens > 0 ? nonCachedInput * (totalCost / totalTokens) : 0;
    const cachedCost = totalTokens > 0 ? cachedTokens * (totalCost / totalTokens) : 0;
    const outputCost = totalTokens > 0 ? (item.completionTokens || 0) * (totalCost / totalTokens) : 0;
    return { ...item, key, totalTokens, totalCost, inputCost, cachedCost, outputCost };
  });
}

export function sortUsageRows(rows: UsageRow[], sortBy: string, sortOrder: "asc" | "desc") {
  return [...rows].sort((a, b) => {
    let left: string | number = a[sortBy as keyof UsageRow] as string | number;
    let right: string | number = b[sortBy as keyof UsageRow] as string | number;
    if (typeof left === "string") left = left.toLowerCase();
    if (typeof right === "string") right = right.toLowerCase();
    if (left < right) return sortOrder === "asc" ? -1 : 1;
    if (left > right) return sortOrder === "asc" ? 1 : -1;
    return 0;
  });
}

export type UsageGroup = {
  groupKey: string;
  summary: UsageRow;
  items: UsageRow[];
};

function groupKeyFor(item: UsageRow, field: UsageTableView) {
  switch (field) {
    case "account":
      return item.accountName || `Account ${item.connectionId?.slice(0, 8) || "unknown"}`;
    case "apiKey":
      return item.keyName || "Unknown Key";
    case "endpoint":
      return item.endpoint || "Unknown Endpoint";
    case "model":
    default:
      return item.rawModel || item.key;
  }
}

export function groupUsageRows(rows: UsageRow[], field: UsageTableView): UsageGroup[] {
  const groups = new Map<string, UsageGroup>();
  for (const item of rows) {
    const groupKey = groupKeyFor(item, field);
    const existing = groups.get(groupKey) ?? {
      groupKey,
      summary: {
        key: groupKey,
        requests: 0,
        promptTokens: 0,
        completionTokens: 0,
        cachedTokens: 0,
        cost: 0,
        totalTokens: 0,
        totalCost: 0,
        inputCost: 0,
        cachedCost: 0,
        outputCost: 0,
      },
      items: [],
    };
    existing.summary.requests += item.requests || 0;
    existing.summary.promptTokens += item.promptTokens || 0;
    existing.summary.completionTokens += item.completionTokens || 0;
    existing.summary.cachedTokens += item.cachedTokens || 0;
    existing.summary.cost += item.cost || 0;
    existing.summary.totalTokens += item.totalTokens || 0;
    existing.summary.totalCost += item.totalCost || 0;
    existing.summary.inputCost += item.inputCost || 0;
    existing.summary.cachedCost += item.cachedCost || 0;
    existing.summary.outputCost += item.outputCost || 0;
    if (item.lastUsed && (!existing.summary.lastUsed || new Date(item.lastUsed) > new Date(existing.summary.lastUsed))) {
      existing.summary.lastUsed = item.lastUsed;
    }
    existing.items.push(item);
    groups.set(groupKey, existing);
  }
  return Array.from(groups.values());
}
