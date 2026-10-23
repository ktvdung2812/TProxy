/* Pure helpers for the Logs view (time/latency formatting, status buckets, percentiles). */
import type { TFunction } from "i18next";
import type { RequestLog } from "../../hooks/useRequestLogStream";
import type { Tone } from "../page-kit";

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

/** Localised relative time ("5m ago" / "5 phút trước"); falls back to the date after a week. */
export function fmtAgo(iso: string | undefined, t: TFunction, now = Date.now()): string {
  if (!iso) return "—";
  const diffMs = now - new Date(iso).getTime();
  if (Number.isNaN(diffMs)) return "—";
  const sec = Math.max(0, Math.floor(diffMs / 1000));
  if (sec < 5) return t("logs.ago.now");
  if (sec < 60) return t("logs.ago.seconds", { count: sec });
  const min = Math.floor(sec / 60);
  if (min < 60) return t("logs.ago.minutes", { count: min });
  const hr = Math.floor(min / 60);
  if (hr < 24) return t("logs.ago.hours", { count: hr });
  const day = Math.floor(hr / 24);
  if (day < 7) return t("logs.ago.days", { count: day });
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
  if (ms < 1000) return `${Math.round(ms)} ms`;
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

/** Page-kit tone for an HTTP status: client errors warn, server errors are danger. */
export function statusTone(status?: number): Tone {
  if (!status) return "neutral";
  if (status >= 200 && status < 300) return "success";
  if (status >= 300 && status < 400) return "info";
  if (status >= 400 && status < 500) return "warning";
  return "danger";
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

/** A failed request: any 4xx/5xx, rate limits included. */
export function isErrorLog(item: Pick<RequestLog, "status">): boolean {
  return item.status >= 400;
}

export function isRateLimitedLog(item: Pick<RequestLog, "status" | "error_code">): boolean {
  return item.status === 429 || /rate.?limit/i.test(item.error_code || "");
}

/** Protocol facet key; requests without one (admin API, dashboard assets) share "other". */
export function protocolKey(item: Pick<RequestLog, "protocol">): string {
  return item.protocol?.trim().toLowerCase() || "other";
}

/** Percentile (p50/p95/...) of a numeric array; returns 0 for empty input. */
export function percentile(values: number[], p: number): number {
  if (values.length === 0) return 0;
  const sorted = [...values].sort((a, b) => a - b);
  if (sorted.length === 1) return sorted[0];
  const idx = Math.min(sorted.length - 1, Math.max(0, Math.ceil((p / 100) * sorted.length) - 1));
  return sorted[idx];
}

export function average(values: number[]): number {
  if (values.length === 0) return 0;
  return values.reduce((sum, value) => sum + value, 0) / values.length;
}

/** Case-insensitive substring test for filter inputs. */
export function matchesQuery(haystack: string | undefined | null, query: string): boolean {
  if (!query) return true;
  if (!haystack) return false;
  return haystack.toLowerCase().includes(query.trim().toLowerCase());
}

export type RequestSortKey = "created_at" | "status" | "latency_ms" | "path";

export function sortRequestLogs(logs: RequestLog[], key: RequestSortKey, order: SortOrder): RequestLog[] {
  const next = [...logs];
  next.sort((a, b) => {
    let cmp = 0;
    switch (key) {
      case "status":
        cmp = (a.status ?? 0) - (b.status ?? 0);
        break;
      case "latency_ms":
        cmp = (a.latency_ms ?? 0) - (b.latency_ms ?? 0);
        break;
      case "path":
        cmp = `${a.method} ${a.path}`.localeCompare(`${b.method} ${b.path}`);
        break;
      case "created_at":
      default:
        cmp = new Date(a.created_at).getTime() - new Date(b.created_at).getTime();
        break;
    }
    return order === "asc" ? cmp : -cmp;
  });
  return next;
}

/** Stable identity for a log row; request ids repeat across retry attempts. */
export function requestLogKey(item: RequestLog): string {
  return `${item.request_id}-${item.attempt ?? 0}-${item.created_at}`;
}

export function modelOf(item: RequestLog): string {
  const value = item.public_model_id || item.metadata?.model;
  return value ? String(value) : "";
}

export function providerOf(item: RequestLog): string {
  const value = item.provider_id || item.metadata?.provider;
  return value ? String(value) : "";
}
