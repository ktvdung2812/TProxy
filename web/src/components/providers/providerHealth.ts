import type { Tone } from "../page-kit";
import type { NinerouterPreset } from "./api";
import { getProviderTypeInfo } from "./catalog";
import { isOnCooldown, type Credential, type Provider } from "./types";

/* ------------------------------------------------------------
   Health buckets shared by the providers list and detail pages.
   A provider's colour answers "can it serve traffic right now?";
   an account's colour answers "will the router pick it?".
   ------------------------------------------------------------ */

export type AccountState = "ready" | "cooldown" | "degraded" | "attention" | "off" | "unknown";

export const ACCOUNT_STATE_TONE: Record<AccountState, Tone> = {
  ready: "success",
  cooldown: "warning",
  degraded: "warning",
  attention: "danger",
  off: "muted",
  unknown: "neutral",
};

const FAILING_STATUSES = new Set(["auth_required", "error", "unhealthy", "down", "failed"]);

export function accountState(credential: Credential): AccountState {
  if (!credential.enabled) return "off";
  if (isOnCooldown(credential.cooldown_until) || credential.status === "cooldown") return "cooldown";
  if (credential.status && FAILING_STATUSES.has(credential.status)) return "attention";
  if (credential.status === "degraded") return "degraded";
  if (credential.status === "healthy") return "ready";
  return "unknown";
}

export type AccountCounts = Record<AccountState, number> & { total: number; on: number };

export function countAccounts(credentials: Credential[] = []): AccountCounts {
  const counts: AccountCounts = { total: credentials.length, on: 0, ready: 0, cooldown: 0, degraded: 0, attention: 0, off: 0, unknown: 0 };
  for (const credential of credentials) {
    const state = accountState(credential);
    counts[state] += 1;
    if (credential.enabled) counts.on += 1;
  }
  return counts;
}

/**
 * - healthy: serving normally (a single account in cooldown is still healthy —
 *   cooldowns clear on their own and are counted separately)
 * - degraded: serving, but the upstream reports problems or some accounts fail
 * - attention: cannot serve until someone acts (sign-in, failing or no usable account)
 * - idle: enabled but no account connected yet
 * - off: provider disabled
 */
export type ProviderHealth = "healthy" | "degraded" | "attention" | "idle" | "off" | "unknown";

export const PROVIDER_HEALTH_TONE: Record<ProviderHealth, Tone> = {
  healthy: "success",
  degraded: "warning",
  attention: "danger",
  idle: "neutral",
  off: "muted",
  unknown: "neutral",
};

export function providerHealth(provider: Provider, credentials: Credential[] = [], noAuth = false): ProviderHealth {
  if (!provider.Enabled) return "off";
  const status = provider.Status?.trim().toLowerCase() || "";
  if (FAILING_STATUSES.has(status)) return "attention";
  if (credentials.length === 0) {
    if (!noAuth) return "idle";
    return status === "degraded" ? "degraded" : status === "healthy" ? "healthy" : "unknown";
  }
  const counts = countAccounts(credentials);
  if (counts.on === 0) return "attention";
  const usable = counts.ready + counts.degraded + counts.unknown;
  if (usable === 0 && counts.cooldown === 0) return "attention";
  if (status === "degraded" || counts.attention > 0 || counts.degraded > 0) return "degraded";
  if (status === "healthy" || counts.ready > 0) return "healthy";
  return "unknown";
}

/** Buckets used by the list filters: "attention" groups degraded and blocked providers. */
export type ProviderHealthFilter = "all" | "healthy" | "attention" | "off" | "cooldown";

export function matchesHealthFilter(filter: ProviderHealthFilter, health: ProviderHealth, counts: AccountCounts) {
  switch (filter) {
    case "all":
      return true;
    case "healthy":
      return health === "healthy";
    case "attention":
      return health === "degraded" || health === "attention";
    case "off":
      return health === "off";
    case "cooldown":
      return counts.cooldown > 0;
  }
}

/* ------------------------------------------------------------
   Provider kind — the list page's main facet.
   ------------------------------------------------------------ */
export type ProviderKind = "oauth" | "apikey" | "custom" | "other";

const CUSTOM_TYPES = new Set(["openai-compatible", "anthropic-compatible"]);

export function providerKind(provider: Provider, presets: NinerouterPreset[]): ProviderKind {
  const preset = presets.find((item) => item.id === provider.ID);
  if (preset) {
    if (["oauth", "webCookie", "free"].includes(preset.category) || preset.auth_type === "oauth") return "oauth";
    if (["image", "video", "tavily", "elevenlabs", "plugin-http"].includes(preset.type)) return "other";
    return "apikey";
  }
  if (CUSTOM_TYPES.has(provider.Type)) return "custom";
  const section = getProviderTypeInfo(provider.Type).listSection;
  if (section === "oauth") return "oauth";
  if (section === "media" || section === "plugin") return "other";
  return "apikey";
}

export const PROVIDER_KINDS: ProviderKind[] = ["oauth", "apikey", "custom", "other"];

export const PROVIDER_KIND_ICON: Record<ProviderKind, string> = {
  oauth: "login",
  apikey: "key",
  custom: "extension",
  other: "perm_media",
};

/** Next cooldown to expire among these credentials, or null. */
export function nextCooldown(credentials: Credential[]): string | null {
  let best: number | null = null;
  let value: string | null = null;
  for (const credential of credentials) {
    if (!credential.enabled || !isOnCooldown(credential.cooldown_until)) continue;
    const time = Date.parse(credential.cooldown_until!);
    if (best === null || time < best) {
      best = time;
      value = credential.cooldown_until!;
    }
  }
  return value;
}

/** "4m", "3h", "2d" — compact relative age for "checked …" labels. */
export function relativeAge(value?: string, now = Date.now()): string | null {
  if (!value) return null;
  const time = Date.parse(value);
  if (Number.isNaN(time)) return null;
  const minutes = Math.max(0, Math.round((now - time) / 60000));
  if (minutes < 1) return "<1m";
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.round(minutes / 60);
  if (hours < 48) return `${hours}h`;
  return `${Math.round(hours / 24)}d`;
}

/** "12m", "1h 5m" — time left until an ISO timestamp, or null when past. */
export function timeLeft(value?: string, now = Date.now()): string | null {
  if (!value) return null;
  const time = Date.parse(value);
  if (Number.isNaN(time) || time <= now) return null;
  const minutes = Math.ceil((time - now) / 60000);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours}h ${minutes % 60}m`;
  return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}
