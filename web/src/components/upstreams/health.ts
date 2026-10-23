import type { Tone } from "../page-kit";
import { isOnCooldown } from "../providers/types";

/* ============================================================
   Provider / account health as the operator sees it. Shared by
   the Health overview and the Overview "needs attention" panel,
   so both pages always agree on what is broken.
   ============================================================ */

/** Provider as returned by /api/admin/snapshot (PascalCase). */
export type SnapshotProvider = {
  ID: string;
  Type: string;
  Name: string;
  BaseURL: string;
  Enabled: boolean;
  Status?: string;
  LastError?: string;
  LastChecked?: string;
  OAuth?: Record<string, unknown> | null;
  ProxyPoolIDs?: string[];
};

export type SnapshotCredential = {
  id: string;
  label: string;
  email?: string;
  auth_type: string;
  enabled: boolean;
  status?: string;
  cooldown_until?: string;
  last_error_code?: string;
  last_error?: string;
  proxy_pool_ids?: string[];
};

export type SnapshotRoute = {
  ID: string;
  ProviderID: string;
  UpstreamModel: string;
  Priority: number;
  Weight?: number;
  Enabled: boolean;
};

export type ProviderHealth = "healthy" | "degraded" | "auth_required" | "disabled" | "unchecked";

export const PROVIDER_HEALTH_ORDER: ProviderHealth[] = ["auth_required", "degraded", "unchecked", "healthy", "disabled"];

export const PROVIDER_HEALTH_TONE: Record<ProviderHealth, Tone> = {
  healthy: "success",
  degraded: "warning",
  auth_required: "danger",
  disabled: "muted",
  unchecked: "neutral",
};

export function providerHealth(provider: Pick<SnapshotProvider, "Enabled" | "Status">): ProviderHealth {
  if (!provider.Enabled) return "disabled";
  const status = (provider.Status || "").trim().toLowerCase();
  if (status === "healthy") return "healthy";
  if (status === "auth_required" || status === "unauthorized") return "auth_required";
  if (!status || status === "unknown") return "unchecked";
  // degraded, error, rate_limited… anything the last check flagged.
  return "degraded";
}

export function parseProviderHealthFilter(raw: string | null): ProviderHealth | "all" {
  const value = raw?.trim().toLowerCase() || "";
  return (PROVIDER_HEALTH_ORDER as string[]).includes(value) ? (value as ProviderHealth) : "all";
}

export type CredentialIssue = "auth" | "cooldown" | "error";

/** Why an enabled account cannot serve right now, or null when it can. */
export function credentialIssue(credential: SnapshotCredential): CredentialIssue | null {
  if (!credential.enabled) return null;
  const status = (credential.status || "").toLowerCase();
  // A stale "cooldown" status with an expired timer is not a problem any more.
  if (isOnCooldown(credential.cooldown_until) || (status === "cooldown" && !credential.cooldown_until)) return "cooldown";
  if (status === "auth_required" || credential.last_error_code === "unauthorized") return "auth";
  if (status === "degraded" || status === "error" || (credential.last_error && status !== "healthy" && status !== "cooldown")) return "error";
  return null;
}

export function credentialLabel(credential: SnapshotCredential) {
  return credential.label?.trim() || credential.email?.trim() || credential.id;
}

/** "4m ago" / "4 phút trước" — Intl keeps it localised without extra keys. */
export function formatAgo(value: string | undefined, language: string, now = Date.now()): string | null {
  if (!value) return null;
  const time = Date.parse(value);
  if (Number.isNaN(time) || time <= 0) return null;
  return formatRelative(time - now, language);
}

/** "in 12m" / "sau 12 phút nữa". */
export function formatIn(value: string | undefined, language: string, now = Date.now()): string | null {
  if (!value) return null;
  const time = Date.parse(value);
  if (Number.isNaN(time)) return null;
  return formatRelative(time - now, language);
}

function formatRelative(diffMs: number, language: string) {
  const format = new Intl.RelativeTimeFormat(language, { style: "narrow", numeric: "auto" });
  const minutes = Math.round(diffMs / 60000);
  if (Math.abs(minutes) < 1) return format.format(0, "second");
  if (Math.abs(minutes) < 60) return format.format(minutes, "minute");
  const hours = Math.round(minutes / 60);
  if (Math.abs(hours) < 24) return format.format(hours, "hour");
  return format.format(Math.round(hours / 24), "day");
}

export function compactNumber(value: number) {
  return new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 }).format(value || 0);
}

export function formatUsd(value: number, maximumFractionDigits = 2) {
  return new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", maximumFractionDigits }).format(value || 0);
}
