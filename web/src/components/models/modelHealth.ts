import type { DiscoveredModel } from "../providers/api";
import type { FailoverState } from "./failoverApi";
import type { ModelRecord, ProviderOption, RouteRecord } from "./types";
import { buildModelCardRoutes, providerDisplayLabel, sortRoutes } from "./utils";

/* ============================================================
   Derived health for the Provider Priority Manager page: one view per
   public model with its fallback chain, so cards, rows and stats all
   agree on what "serving" or "broken" means.
   ============================================================ */

export type ModelHealth = "serving" | "degraded" | "attention" | "disabled";

/** Why a step in the chain is or is not taking traffic. */
export type ChainStepState =
  | "ok"
  | "suggested"
  | "off"
  | "paused"
  | "probing"
  | "failing"
  | "provider-off"
  | "unhealthy";

export type ChainStep = {
  key: string;
  providerId: string;
  providerLabel: string;
  providerType: string;
  upstreamModel: string;
  /** 1-based position among routes that can take traffic; 0 when skipped. */
  rank: number;
  saved: boolean;
  enabled: boolean;
  accounts: number;
  state: ChainStepState;
  providerStatus?: string;
  failover?: FailoverState;
};

export type ModelView = {
  model: ModelRecord;
  health: ModelHealth;
  steps: ChainStep[];
  /** Saved routes whose provider has no account: kept, but skipped by the router. */
  hiddenCount: number;
  suggestedCount: number;
  /** Saved routes switched off by the operator or paused by failover. */
  offCount: number;
  usableCount: number;
  providerIds: string[];
};

const UNHEALTHY_STATUSES = new Set(["auth_required", "degraded", "error", "unhealthy", "failed", "expired"]);

function stepState(
  route: { enabled: boolean; saved: boolean },
  provider: ProviderOption | undefined,
  failover: FailoverState | undefined,
): ChainStepState {
  if (!route.saved) return "suggested";
  if (!route.enabled) return "off";
  if (provider && provider.enabled === false) return "provider-off";
  if (failover?.state === "open") return "paused";
  if (failover?.state === "half_open") return "probing";
  if (provider?.status && UNHEALTHY_STATUSES.has(provider.status)) return "unhealthy";
  if (failover && failover.failures > 0) return "failing";
  return "ok";
}

/** A step that can actually serve a request right now. */
export function stepServes(step: ChainStep) {
  return step.state === "ok" || step.state === "failing" || step.state === "probing" || step.state === "unhealthy";
}

export function buildModelView(
  model: ModelRecord,
  routes: RouteRecord[],
  providers: ProviderOption[],
  credentialCounts: Record<string, number>,
  modelsByProvider: Record<string, DiscoveredModel[]>,
  discoveryReady: boolean,
  failoverByKey: Record<string, FailoverState>,
): ModelView {
  const providerById = new Map(providers.map((provider) => [provider.id, provider]));
  // Until discovery settles, show the saved chain only, so suggested routes do
  // not pop in and reshuffle the card a second later.
  const cardRoutes = discoveryReady
    ? buildModelCardRoutes(model, sortRoutes(routes), providers, modelsByProvider, credentialCounts)
    : sortRoutes(routes).map((route) => ({
        key: route.ID,
        provider: route.ProviderID,
        providerLabel: providerDisplayLabel(providers, route.ProviderID),
        upstreamModel: route.UpstreamModel,
        enabled: route.Enabled,
        priority: route.Priority,
        saved: true,
        enabledPosition: 0,
      }));

  let rank = 0;
  let hiddenCount = 0;
  const steps: ChainStep[] = [];
  for (const route of cardRoutes) {
    const accounts = credentialCounts[route.provider] ?? 0;
    // Providers with no accounts cannot serve this model; the router skips them,
    // so they are summarised rather than shown as chain steps.
    if (accounts <= 0) {
      if (route.saved) hiddenCount += 1;
      continue;
    }
    const provider = providerById.get(route.provider);
    const failover = failoverByKey[`${model.ID}::${route.provider}`];
    const state = stepState(route, provider, failover);
    const step: ChainStep = {
      key: route.key,
      providerId: route.provider,
      providerLabel: route.providerLabel,
      providerType: provider?.type || route.provider,
      upstreamModel: route.upstreamModel,
      rank: 0,
      saved: route.saved,
      enabled: route.enabled,
      accounts,
      state,
      providerStatus: provider?.status,
      failover,
    };
    if (route.saved && stepServes(step)) {
      rank += 1;
      step.rank = rank;
    }
    steps.push(step);
  }

  const saved = steps.filter((step) => step.saved);
  const usableCount = saved.filter(stepServes).length;
  const offCount = saved.filter((step) => step.state === "off" || step.state === "paused" || step.state === "provider-off").length;
  // A few failures below the trip threshold are shown on the step but do not
  // mark the whole model as degraded; a paused or unhealthy provider does.
  const troubled = saved.some(
    (step) => step.state === "paused" || step.state === "probing" || step.state === "unhealthy" || step.state === "provider-off",
  );

  let health: ModelHealth;
  if (!model.Enabled) health = "disabled";
  else if (usableCount === 0) health = "attention";
  else if (troubled) health = "degraded";
  else health = "serving";

  return {
    model,
    health,
    steps,
    hiddenCount,
    suggestedCount: steps.filter((step) => !step.saved).length,
    offCount,
    usableCount,
    providerIds: [...new Set(saved.map((step) => step.providerId))],
  };
}

export const HEALTH_RANK: Record<ModelHealth, number> = { attention: 0, degraded: 1, serving: 2, disabled: 3 };

export function failoverKeyMap(states: FailoverState[]) {
  const map: Record<string, FailoverState> = {};
  for (const state of states) map[`${state.model_id}::${state.provider_id}`] = state;
  return map;
}

/** Short countdown like "2m" for a paused provider's next retry. */
export function formatRetryIn(seconds: number) {
  if (seconds <= 0) return "";
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  return `${Math.round(minutes / 60)}h`;
}
