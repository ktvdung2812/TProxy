/* Pure helpers for the Logs view (time/latency formatting, status buckets, percentiles). */

/** Format an ISO timestamp as a relative "Just now / 5m ago / 3h ago" string. */
export function fmtTimeAgo(iso?: string): string {
  if (!iso) return "—";
  const diffMs = Date.now() - new Date(iso).getTime();
  if (Number.isNaN(diffMs)) return "—";
  const sec = Math.floor(diffMs / 1000);
  if (sec < 5) return "Just now";
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const day = Math.floor(hr / 24);
  if (day < 7) return `${day}d ago`;
  return new Date(iso).toLocaleDateString();
}

/** Format an ISO timestamp as a compact local clock (HH:MM:SS). */
export function fmtClock(iso?: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleTimeString(undefined, { hour12: false });
}

/** Format a full absolute timestamp used in the row tooltip. */
export function fmtAbsolute(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleString();
}

/** Human-readable latency: "45 ms" or "1.2s". */
export function fmtLatency(ms?: number): string {
  if (ms == null || Number.isNaN(ms)) return "—";
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(ms % 1000 === 0 ? 0 : 1)}s`;
}

export type SortOrder = "asc" | "desc";

export type StatusVariant = "success" | "info" | "warning" | "error" | "default";

/** Map an HTTP status code to a Badge variant. */
export function statusVariant(status?: number): StatusVariant {
  if (!status) return "default";
  if (status >= 200 && status < 300) return "success";
  if (status >= 300 && status < 400) return "info";
  if (status >= 400 && status < 500) return "warning";
  if (status >= 500) return "error";
  return "default";
}

/** Bucket label for status filter: "all" | "2xx" | "3xx" | "4xx" | "5xx" | "other". */
export function statusBucket(status?: number): "2xx" | "3xx" | "4xx" | "5xx" | "other" {
  if (!status) return "other";
  if (status >= 200 && status < 300) return "2xx";
  if (status >= 300 && status < 400) return "3xx";
  if (status >= 400 && status < 500) return "4xx";
  if (status >= 500) return "5xx";
  return "other";
}

/** Percentile (p50/p95/...) of a numeric array; returns 0 for empty input. */
export function percentile(values: number[], p: number): number {
  if (values.length === 0) return 0;
  const sorted = [...values].sort((a, b) => a - b);
  if (sorted.length === 1) return sorted[0];
  const idx = Math.min(sorted.length - 1, Math.max(0, Math.ceil((p / 100) * sorted.length) - 1));
  return sorted[idx];
}

/** Case-insensitive substring test for filter inputs. */
export function matchesQuery(haystack: string | undefined | null, query: string): boolean {
  if (!query) return true;
  if (!haystack) return false;
  return haystack.toLowerCase().includes(query.trim().toLowerCase());
}
