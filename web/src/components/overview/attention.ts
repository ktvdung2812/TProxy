import type { TFunction } from "i18next";
import type { Tone } from "../page-kit";
import { maskAccountText } from "../../lib/accountMask";
import { poolHealth, type PoolLike } from "../proxy-pools/utils";
import {
  credentialIssue,
  credentialLabel,
  formatIn,
  providerHealth,
  type SnapshotCredential,
  type SnapshotProvider,
  type SnapshotRoute,
} from "../upstreams/health";

/* ============================================================
   "Needs attention" — everything in the snapshot that keeps
   traffic from flowing, each with the page that fixes it.
   ============================================================ */

export type AttentionKind = "provider" | "account" | "pool" | "model";

export type AttentionItem = {
  key: string;
  tone: Tone;
  icon: string;
  kind: AttentionKind;
  title: string;
  detail?: string;
  /** Route that fixes the problem. */
  to: string;
  action: string;
  providerId?: string;
  providerType?: string;
};

export type AttentionSource = {
  providers: SnapshotProvider[];
  credentials: Record<string, SnapshotCredential[]>;
  proxyPools: PoolLike[];
  models: Array<{ ID: string; DisplayName?: string; Enabled: boolean }>;
  routes: Record<string, SnapshotRoute[]>;
};

const TONE_RANK: Partial<Record<Tone, number>> = { danger: 0, warning: 1, info: 2, neutral: 2, muted: 3 };

const providerPath = (id: string) => `/providers/${encodeURIComponent(id)}`;

export function buildAttention(source: AttentionSource, t: TFunction, options: { masked: boolean; language: string }): AttentionItem[] {
  const items: AttentionItem[] = [];
  const providersById = new Map(source.providers.map((provider) => [provider.ID, provider]));

  const routedTo: Record<string, number> = {};
  for (const list of Object.values(source.routes || {})) {
    for (const route of list || []) {
      if (route.Enabled) routedTo[route.ProviderID] = (routedTo[route.ProviderID] || 0) + 1;
    }
  }

  for (const provider of source.providers) {
    const name = provider.Name || provider.ID;
    const health = providerHealth(provider);
    const base = { providerId: provider.ID, providerType: provider.Type, kind: "provider" as const };
    if (health === "auth_required") {
      const affected = (source.credentials[provider.ID] || []).filter((item) => item.enabled).length;
      items.push({
        ...base,
        key: `provider-${provider.ID}`,
        tone: "danger",
        icon: "key_off",
        title: t("overview.attention.providerAuth", { name }),
        detail: joinDetail(provider.LastError, affected > 0 ? t("overview.attention.accountsAffected", { count: affected }) : undefined),
        to: providerPath(provider.ID),
        action: t("overview.attention.signIn"),
      });
    } else if (health === "degraded") {
      items.push({
        ...base,
        key: `provider-${provider.ID}`,
        tone: "warning",
        icon: "warning",
        title: t("overview.attention.providerDegraded", { name }),
        detail: provider.LastError || t("overview.attention.providerDegradedHint"),
        to: "/upstreams?status=degraded",
        action: t("overview.attention.checkHealth"),
      });
    } else if (health === "unchecked") {
      items.push({
        ...base,
        key: `provider-${provider.ID}`,
        tone: "info",
        icon: "help",
        title: t("overview.attention.providerUnchecked", { name }),
        detail: t("overview.attention.providerUncheckedHint"),
        to: "/upstreams?status=unchecked",
        action: t("overview.attention.checkHealth"),
      });
    } else if (health === "disabled") {
      const routes = routedTo[provider.ID] || 0;
      items.push({
        ...base,
        key: `provider-${provider.ID}`,
        tone: "muted",
        icon: "power_settings_new",
        title: t("overview.attention.providerDisabled", { name }),
        detail: routes > 0 ? t("overview.attention.disabledRoutes", { count: routes }) : t("overview.attention.disabledHint"),
        to: providerPath(provider.ID),
        action: t("overview.attention.openProvider"),
      });
    }
  }

  for (const [providerId, list] of Object.entries(source.credentials || {})) {
    const provider = providersById.get(providerId);
    // Accounts of a disabled provider never serve, and a provider that needs a
    // new sign-in is already listed once above.
    if (!provider || !provider.Enabled) continue;
    const providerName = provider.Name || provider.ID;
    const providerAuth = providerHealth(provider) === "auth_required";
    for (const credential of list || []) {
      const issue = credentialIssue(credential);
      if (!issue || (issue === "auth" && providerAuth)) continue;
      const account = maskAccountText(credentialLabel(credential), options.masked);
      const base = { providerId, providerType: provider.Type, kind: "account" as const, key: `account-${credential.id}` };
      if (issue === "auth") {
        items.push({
          ...base,
          tone: "danger",
          icon: "key_off",
          title: t("overview.attention.accountAuth", { account }),
          detail: joinDetail(providerName, credential.last_error),
          to: providerPath(providerId),
          action: t("overview.attention.signIn"),
        });
      } else if (issue === "cooldown") {
        const back = formatIn(credential.cooldown_until, options.language);
        items.push({
          ...base,
          tone: "warning",
          icon: "hourglass_top",
          title: t("overview.attention.accountCooldown", { account }),
          detail: joinDetail(providerName, back ? t("overview.attention.backIn", { time: back }) : undefined, credential.last_error),
          to: "/quota?status=attention",
          action: t("overview.attention.clearCooldown"),
        });
      } else {
        items.push({
          ...base,
          tone: "warning",
          icon: "error",
          title: t("overview.attention.accountError", { account }),
          detail: joinDetail(providerName, credential.last_error),
          to: providerPath(providerId),
          action: t("overview.attention.openProvider"),
        });
      }
    }
  }

  for (const pool of source.proxyPools || []) {
    if (poolHealth(pool) !== "failing") continue;
    const inUse = (pool.usage_count || 0) > 0;
    items.push({
      key: `pool-${pool.id}`,
      kind: "pool",
      tone: inUse ? "danger" : "warning",
      icon: "lan",
      title: t("overview.attention.poolFailing", { name: pool.name || pool.id }),
      detail: joinDetail(pool.last_error, inUse ? t("overview.attention.poolInUse", { count: pool.usage_count }) : t("overview.attention.poolUnused")),
      to: "/proxy-pools?status=failing",
      action: t("overview.attention.testPool"),
    });
  }

  for (const model of source.models || []) {
    if (!model.Enabled) continue;
    const working = (source.routes?.[model.ID] || []).some((route) => route.Enabled && providersById.get(route.ProviderID)?.Enabled);
    if (working) continue;
    items.push({
      key: `model-${model.ID}`,
      kind: "model",
      tone: "warning",
      icon: "alt_route",
      title: t("overview.attention.modelNoRoute", { name: model.DisplayName || model.ID }),
      detail: t("overview.attention.modelNoRouteHint"),
      to: "/models",
      action: t("overview.attention.addRoute"),
    });
  }

  return items
    .map((item, index) => ({ item, index }))
    .sort((a, b) => (TONE_RANK[a.item.tone] ?? 2) - (TONE_RANK[b.item.tone] ?? 2) || a.index - b.index)
    .map(({ item }) => item);
}

function joinDetail(...parts: Array<string | undefined>) {
  const text = parts.map((part) => part?.trim()).filter(Boolean).join(" · ");
  return text || undefined;
}
