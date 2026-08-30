/* Shared types for the providers feature.
   Mirrors the snapshot shapes documented by the tdproxy backend
   (PascalCase for providers/models/routes, snake_case for credentials). */

export type Provider = {
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

export type RouteTarget = {
  ID: string;
  PublicModelID: string;
  ProviderID: string;
  UpstreamModel: string;
  Priority: number;
  Weight: number;
  Enabled: boolean;
};

export type PublicModel = {
  ID: string;
  DisplayName: string;
  Aliases: string[];
  Enabled: boolean;
  Capabilities: string[];
};

export type ModelAlias = {
  alias: string;
  public_model_id: string;
  api_key_id?: string;
  team_id?: string;
  enabled: boolean;
};

export type Credential = {
  id: string;
  label: string;
  email?: string;
  auth_type: string;
  enabled: boolean;
  status?: string;
  priority?: number;
  weight?: number;
  cooldown_until?: string;
  last_error_code?: string;
  last_error?: string;
  proxy_pool_ids?: string[];
  last_used_at?: string;
  last_validated_at?: string;
  consecutive_use_count?: number;
  created_at?: string;
};

/** Sort credentials by first-seen time (oldest first), then id. */
export function compareCredentialsByCreatedAt(left: Credential, right: Credential): number {
  const leftTime = left.created_at ? Date.parse(left.created_at) : Number.NaN;
  const rightTime = right.created_at ? Date.parse(right.created_at) : Number.NaN;
  if (!Number.isNaN(leftTime) && !Number.isNaN(rightTime) && leftTime !== rightTime) {
    return leftTime - rightTime;
  }
  if (!Number.isNaN(leftTime) && Number.isNaN(rightTime)) return -1;
  if (Number.isNaN(leftTime) && !Number.isNaN(rightTime)) return 1;
  return left.id.localeCompare(right.id);
}

/** Router order is represented by descending priority; creation/id is only a
 * deterministic fallback for legacy accounts that all still have priority 0. */
export function compareCredentialsByPriority(left: Credential, right: Credential): number {
  const priorityDifference = (right.priority ?? 0) - (left.priority ?? 0);
  if (priorityDifference !== 0) return priorityDifference;
  return compareCredentialsByCreatedAt(left, right);
}

export function moveCredentialBefore<T extends Pick<Credential, "id">>(items: T[], draggedId: string, targetId: string): T[] {
  if (draggedId === targetId) return items;
  const from = items.findIndex((item) => item.id === draggedId);
  const target = items.findIndex((item) => item.id === targetId);
  if (from < 0 || target < 0) return items;
  const next = [...items];
  const [dragged] = next.splice(from, 1);
  const insertAt = next.findIndex((item) => item.id === targetId);
  next.splice(insertAt < 0 ? target : insertAt, 0, dragged);
  return next;
}

export function formatCredentialAddedAt(value?: string) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const day = String(date.getDate()).padStart(2, "0");
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const year = date.getFullYear();
  const hours = String(date.getHours()).padStart(2, "0");
  const minutes = String(date.getMinutes()).padStart(2, "0");
  const seconds = String(date.getSeconds()).padStart(2, "0");
  return `${day}/${month}/${year} ${hours}:${minutes}:${seconds}`;
}

const SERVICE_PLAN_LABELS: Record<string, string> = {
  plus: "Plus",
  pro: "Pro",
  team: "Teams",
  teams: "Teams",
  free: "Free",
  enterprise: "Enterprise",
  business: "Business",
};

export function formatServicePlanLabel(plan?: string) {
  const raw = plan?.trim();
  if (!raw) return "";
  const key = raw.toLowerCase().replace(/[\s-]+/g, "_");
  if (SERVICE_PLAN_LABELS[key]) return SERVICE_PLAN_LABELS[key];
  if (key.includes("team")) return "Teams";
  if (key.includes("plus")) return "Plus";
  if (key === "pro" || key.endsWith("_pro")) return "Pro";
  return raw
    .replace(/_/g, " ")
    .replace(/\b\w/g, (char) => char.toUpperCase());
}

/** Stable account numbers (#1, #2, …) by first-seen time, not last update. */
export function buildCredentialAccountNumbers(credentials: Credential[] = []): Map<string, number> {
  const ranked = [...credentials].sort(compareCredentialsByCreatedAt);
  const numbers = new Map<string, number>();
  ranked.forEach((credential, index) => numbers.set(credential.id, index + 1));
  return numbers;
}

/** Aggregate connection counts for a provider. */
export type ProviderStats = {
  /** Enabled credentials currently healthy. */
  connected: number;
  /** Enabled credentials (active accounts). */
  active: number;
  /** Disabled credentials. */
  disabled: number;
  error: number;
  cooldown: number;
  total: number;
};

/** Categorise a provider for list grouping. */
export function providerCategory(type: string): "oauth" | "apikey" | "media" | "plugin" {
  if (["claude", "codex", "kimi", "xai", "antigravity"].includes(type)) return "oauth";
  if (["image", "video"].includes(type)) return "media";
  if (type === "plugin-http") return "plugin";
  return "apikey";
}

/** Count credentials by effective status for a provider. */
export function getProviderStats(credentials: Credential[] = []): ProviderStats {
  const stats: ProviderStats = {
    connected: 0,
    active: 0,
    disabled: 0,
    error: 0,
    cooldown: 0,
    total: credentials.length,
  };
  for (const c of credentials) {
    if (!c.enabled) {
      stats.disabled++;
      continue;
    }
    stats.active++;
    if (isOnCooldown(c.cooldown_until)) {
      stats.cooldown++;
      stats.error++;
    } else if (c.status === "healthy") {
      stats.connected++;
    } else if (c.status === "auth_required" || c.status === "cooldown") {
      stats.error++;
    } else if (c.status === "unknown") {
      // unknown is neutral — not counted as connected or error
    }
  }
  return stats;
}

export function isOnCooldown(cooldownUntil?: string): boolean {
  if (!cooldownUntil) return false;
  const target = Date.parse(cooldownUntil);
  if (Number.isNaN(target)) return false;
  return target > Date.now();
}

/** Map a provider/credential status to a short display label. */
export function providerStatusLabel(status?: string, enabled = true): { label: string; tone: "success" | "warning" | "error" | "default" } {
  if (!enabled) return { label: "disabled", tone: "default" };
  if (status === "healthy") return { label: "healthy", tone: "success" };
  if (status === "degraded") return { label: "degraded", tone: "warning" };
  if (status === "auth_required") return { label: "auth required", tone: "error" };
  return { label: "unknown", tone: "default" };
}

export function credentialStatusLabel(credential: Credential): { label: string; tone: "success" | "warning" | "error" | "default" } {
  if (!credential.enabled) return { label: "disabled", tone: "default" };
  if (isOnCooldown(credential.cooldown_until)) return { label: "cooldown", tone: "warning" };
  if (credential.status === "healthy") return { label: "healthy", tone: "success" };
  if (credential.status === "auth_required") return { label: "auth required", tone: "error" };
  if (credential.status === "cooldown") return { label: "cooldown", tone: "warning" };
  return { label: "unknown", tone: "default" };
}
