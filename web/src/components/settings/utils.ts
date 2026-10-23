import type { AdminSettings } from "./api";

export type SettingsSectionId = "rotation" | "access" | "password" | "backup" | "features" | "retention";

export const SETTINGS_SECTIONS: Array<{ id: SettingsSectionId; icon: string; readOnly?: boolean }> = [
  { id: "rotation", icon: "sync_alt" },
  { id: "access", icon: "lan" },
  { id: "password", icon: "lock" },
  { id: "backup", icon: "backup" },
  { id: "features", icon: "tune", readOnly: true },
  { id: "retention", icon: "schedule", readOnly: true },
];

/** Icons for the global strategies in GLOBAL_ROTATION_STRATEGIES (providers/api.ts). */
export const STRATEGY_ICON: Record<string, string> = {
  "fill-first": "vertical_align_top",
  "round-robin": "autorenew",
  "weighted-round-robin": "balance",
  "expiring-first": "hourglass_bottom",
};

export const RETENTION_KEYS: Array<keyof AdminSettings["retention"]> = [
  "usage_events",
  "audit_events",
  "media_jobs",
  "oauth_sessions",
  "cleanup_interval",
];

/**
 * Turns a Go duration such as "720h" or "36h0m0s" into whole days/hours so
 * operators do not have to divide by 24 in their head. Returns null when the
 * value is not a plain hour count of at least a day.
 */
export function humanizeDuration(value?: string): { days: number; hours: number } | null {
  if (!value) return null;
  const match = /^(\d+)h(?:0m(?:0s)?)?$/.exec(value.trim());
  if (!match) return null;
  const total = Number(match[1]);
  if (!Number.isFinite(total) || total < 24) return null;
  return { days: Math.floor(total / 24), hours: total % 24 };
}

export function isLoopbackHost(host?: string) {
  return !host || host === "127.0.0.1" || host === "localhost" || host === "::1";
}

export function looksLikeHttpUrl(value: string) {
  try {
    const url = new URL(value);
    return url.protocol === "http:" || url.protocol === "https:";
  } catch {
    return false;
  }
}

export function downloadBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  anchor.click();
  URL.revokeObjectURL(url);
}
