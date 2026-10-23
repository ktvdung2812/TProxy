import type { Tone } from "../page-kit";
import type { Credential } from "../providers/types";
import type { OperationsAccount, OperationsAlert, OperationsMetric } from "./api";

export type AlertSeverity = "critical" | "warning";
export type OperationsSort = "attention" | "busiest" | "slowest" | "name";

/** Thresholds mirror the backend alert rule (20+ attempts, 20%+ upstream errors). */
export const ALERT_MIN_ATTEMPTS = 20;
export const ERROR_RATE_ALERT = 0.2;
export const ERROR_RATE_WARN = 0.05;
export const POLL_INTERVAL_MS = 15_000;
/** Quota observations older than this are flagged as stale, as before. */
export const QUOTA_STALE_MS = 300_000;

// Critical: requests that hit this resource fail now. Warning: traffic still
// flows (failover, other accounts) but capacity or reliability is degraded.
const SEVERITY: Record<string, AlertSeverity> = {
  route_unavailable: "critical",
  authentication_failed: "critical",
  high_error_rate: "warning",
  quota_exhausted: "warning",
  account_expired: "warning",
};

export function alertSeverity(kind: string): AlertSeverity {
  return SEVERITY[kind] ?? "warning";
}

export const SEVERITY_TONE: Record<AlertSeverity, Tone> = { critical: "danger", warning: "warning" };

export type CredentialRef = { providerId: string; providerType: string; label: string };
export type ProviderRef = { id: string; type: string; name: string };

export function buildCredentialIndex(credentials: Record<string, Credential[]>, providers: ProviderRef[]) {
  const typeById = new Map(providers.map((provider) => [provider.id, provider.type]));
  const index = new Map<string, CredentialRef>();
  for (const [providerId, items] of Object.entries(credentials)) {
    for (const credential of items || []) {
      index.set(credential.id, {
        providerId,
        providerType: typeById.get(providerId) || providerId,
        label: credential.label || credential.email || credential.id,
      });
    }
  }
  return index;
}

/** high_error_rate alerts name "provider / model / credential"; the rest name one id. */
export function parseErrorRateResource(resourceId: string) {
  const [providerId = "", modelId = "", credentialId = ""] = resourceId.split(" / ");
  return { providerId, modelId, credentialId };
}

export type AlertSubject = {
  providerId: string | null;
  credentialId: string | null;
  modelId: string | null;
};

export function alertSubject(alert: OperationsAlert, index: Map<string, CredentialRef>): AlertSubject {
  if (alert.kind === "high_error_rate") {
    const parsed = parseErrorRateResource(alert.resource_id);
    return { providerId: parsed.providerId || null, credentialId: parsed.credentialId || null, modelId: parsed.modelId || null };
  }
  if (alert.kind === "route_unavailable") return { providerId: null, credentialId: null, modelId: alert.resource_id };
  return { providerId: index.get(alert.resource_id)?.providerId ?? null, credentialId: alert.resource_id, modelId: null };
}

export type AlertFix = { path: string; labelKey: string; icon: string };

/** Where an operator goes to resolve each alert kind. */
export function alertFix(alert: OperationsAlert, subject: AlertSubject, index: Map<string, CredentialRef>): AlertFix {
  const providerPath = subject.providerId ? `/providers/${encodeURIComponent(subject.providerId)}` : "/providers";
  switch (alert.kind) {
    case "quota_exhausted": {
      const type = subject.credentialId ? index.get(subject.credentialId)?.providerType : undefined;
      return { path: type ? `/quota?provider=${encodeURIComponent(type)}` : "/quota", labelKey: "operations.fix.quota", icon: "data_usage" };
    }
    case "route_unavailable":
      return { path: "/models", labelKey: "operations.fix.routes", icon: "route" };
    case "authentication_failed":
      return { path: providerPath, labelKey: "operations.fix.reauth", icon: "key" };
    case "account_expired":
      return { path: providerPath, labelKey: "operations.fix.account", icon: "manage_accounts" };
    default:
      return { path: providerPath, labelKey: "operations.fix.provider", icon: "dns" };
  }
}

export const errorRate = (metric: Pick<OperationsMetric, "requests" | "errors">) =>
  metric.requests > 0 ? metric.errors / metric.requests : 0;

export function errorTone(rate: number, attempts = ALERT_MIN_ATTEMPTS): Tone {
  if (rate >= ERROR_RATE_ALERT && attempts >= ALERT_MIN_ATTEMPTS) return "danger";
  if (rate >= ERROR_RATE_WARN) return "warning";
  return "success";
}

export function quotaTone(remaining: number): Tone {
  if (remaining <= 0.05) return "danger";
  if (remaining < 0.3) return "warning";
  return "success";
}

export function isQuotaStale(account: OperationsAccount, now = Date.now()) {
  const at = Date.parse(account.quota_at);
  return Number.isFinite(at) && now - at > QUOTA_STALE_MS;
}

/** Row stripe for the live account list: errors and empty quota first, then queueing. */
export function accountTone(account: OperationsAccount): Tone | null {
  const quota = account.quota_remaining;
  if (account.error_rate >= ERROR_RATE_ALERT || (quota !== undefined && quota <= 0.05)) return "danger";
  if (account.waiting > 0 || account.error_rate >= ERROR_RATE_WARN || (quota !== undefined && quota < 0.3)) return "warning";
  return null;
}

export const fmtNumber = (value: number) => (value || 0).toLocaleString(undefined, { maximumFractionDigits: 1 });
export const fmtPercent = (ratio: number) => `${(ratio * 100).toLocaleString(undefined, { maximumFractionDigits: 1 })}%`;
export const fmtMs = (value: number | null | undefined) =>
  value === null || value === undefined ? "—" : `${(value || 0).toLocaleString(undefined, { maximumFractionDigits: 0 })} ms`;

/** "42m", "5h 3m", "2d 4h" — compact age for alert chips. */
export function fmtAge(fromIso: string, now = Date.now()) {
  const minutes = Math.max(0, Math.floor((now - Date.parse(fromIso)) / 60_000));
  if (!Number.isFinite(minutes)) return "—";
  if (minutes < 1) return "<1m";
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ${minutes % 60}m`;
  return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}

export function fmtClock(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : date.toLocaleTimeString();
}
