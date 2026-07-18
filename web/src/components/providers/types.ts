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
  last_error?: string;
  proxy_pool_ids?: string[];
  last_used_at?: string;
  consecutive_use_count?: number;
};

/** Aggregate connection counts for a provider. */
export type ProviderStats = {
  connected: number;
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
  const stats: ProviderStats = { connected: 0, error: 0, cooldown: 0, total: credentials.length };
  for (const c of credentials) {
    if (!c.enabled) continue;
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
