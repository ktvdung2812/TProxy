import type { DiscoveredModel } from "../providers/api";
import type { ModelFormData, ModelRecord, ProviderOption, RouteFormData, RouteRecord } from "./types";

export const MODEL_ID_REGEX = /^[a-zA-Z0-9_.-]+$/;

export const CAPABILITY_OPTIONS = ["text", "tools", "vision", "reasoning", "embedding"] as const;

export function emptyModelForm(): ModelFormData {
  return {
    id: "",
    display_name: "",
    aliases: "",
    enabled: true,
    rewrite_response_model: true,
    capabilities: ["text", "tools"],
    routes: [],
  };
}

export function modelToForm(model: ModelRecord, routes: RouteRecord[]): ModelFormData {
  return {
    id: model.ID,
    display_name: model.DisplayName || "",
    aliases: (model.Aliases || []).join(", "),
    enabled: model.Enabled,
    rewrite_response_model: model.RewriteResponseModel !== false,
    capabilities: model.Capabilities?.length ? [...model.Capabilities] : ["text", "tools"],
    routes: sortRoutes(routes).map((route) => ({
      id: route.ID,
      provider: route.ProviderID,
      upstream_model: route.UpstreamModel,
      priority: route.Priority,
      weight: route.Weight && route.Weight > 0 ? route.Weight : 1,
      enabled: route.Enabled,
    })),
  };
}

export function sortRoutes(routes: RouteRecord[]) {
  return [...routes].sort((left, right) => right.Priority - left.Priority || left.ProviderID.localeCompare(right.ProviderID));
}

export function sortRouteForms(routes: RouteFormData[]) {
  return [...routes].sort((left, right) => right.priority - left.priority || left.provider.localeCompare(right.provider));
}

export function reorderRoutePriorities(routes: RouteFormData[]) {
  return routes.map((route, index) => ({
    ...route,
    priority: defaultRoutePriority(index),
  }));
}

export function accountHealthVariant(count: number): "success" | "warning" | "error" | "default" {
  if (count <= 0) return "error";
  if (count === 1) return "warning";
  return "success";
}

export function accountHealthLabel(count: number) {
  if (count <= 0) return "0 accounts";
  if (count === 1) return "1 account";
  return `${count} accounts`;
}

export function defaultRoutePriority(index: number) {
  return Math.max(10, 100 - index * 10);
}

export function newRouteId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `route-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

export function emptyRoute(provider = "", priority = 100, upstreamModel = "", enabled = true): RouteFormData {
  return {
    id: newRouteId(),
    provider,
    upstream_model: upstreamModel,
    priority,
    weight: 1,
    enabled,
  };
}

/** Ensure every route has a non-empty, unique id before persisting. */
export function ensureUniqueRouteIds(routes: RouteFormData[]): RouteFormData[] {
  const used = new Set<string>();
  return routes.map((route) => {
    let id = route.id.trim() || newRouteId();
    if (used.has(id)) {
      id = newRouteId();
    }
    used.add(id);
    return id === route.id ? route : { ...route, id };
  });
}

export function formToPayload(form: ModelFormData) {
  return {
    id: form.id.trim(),
    display_name: form.display_name.trim() || form.id.trim(),
    aliases: form.aliases
      .split(",")
      .map((alias) => alias.trim())
      .filter(Boolean),
    enabled: form.enabled,
    rewrite_response_model: form.rewrite_response_model,
    capabilities: form.capabilities.length ? form.capabilities : ["text"],
    routes: ensureUniqueRouteIds(form.routes).map((route) => ({
      id: route.id.trim(),
      provider: route.provider.trim(),
      upstream_model: route.upstream_model.trim(),
      priority: Number(route.priority) || 0,
      weight: Number(route.weight) > 0 ? Number(route.weight) : 1,
      enabled: route.enabled,
    })),
  };
}

export function validateModelForm(form: ModelFormData, existingIds: string[], editing: boolean) {
  const id = form.id.trim();
  if (!id) return "Model ID is required";
  if (!MODEL_ID_REGEX.test(id)) return "Only letters, numbers, -, _, and . allowed";
  if (!editing && existingIds.includes(id)) return "This model ID is already in use";
  return "";
}

export function validatePriorityRoutes(routes: RouteFormData[]) {
  if (routes.length === 0) return "Add at least one provider route";
  if (routes.some((route) => !route.provider.trim())) return "Each route needs a provider";
  if (routes.some((route) => !route.upstream_model.trim())) return "Each route needs an upstream model";
  return "";
}

export function validateModelWithRoutes(form: ModelFormData, existingIds: string[], editing: boolean) {
  const metadataError = validateModelForm(form, existingIds, editing);
  if (metadataError) return metadataError;
  return validatePriorityRoutes(form.routes);
}

export function virtualModelIdFromSelection(providerId: string, upstreamModel: string): string {
  const raw = `${providerId}-${upstreamModel}`
    .replace(/[^a-zA-Z0-9_.-]+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "");
  return raw.slice(0, 80) || "model";
}

export function uniqueVirtualModelId(base: string, existingIds: string[]): string {
  if (!existingIds.includes(base)) return base;
  let suffix = 2;
  while (existingIds.includes(`${base}-${suffix}`)) suffix += 1;
  return `${base}-${suffix}`;
}

export function deriveCapabilities(capabilities?: string[]): string[] {
  const normalized = (capabilities || []).map((item) => item.trim().toLowerCase()).filter(Boolean);
  if (normalized.length > 0) return [...new Set(normalized)];
  return ["text", "tools"];
}

export function buildModelFormFromSelection(input: {
  providerId: string;
  upstreamModel: string;
  modelName?: string;
  capabilities?: string[];
  existingIds: string[];
  credentialCounts?: Record<string, number>;
}): ModelFormData {
  const accountCount = input.credentialCounts?.[input.providerId] ?? 1;
  const baseId = virtualModelIdFromSelection(input.providerId, input.upstreamModel);
  return {
    id: uniqueVirtualModelId(baseId, input.existingIds),
    display_name: input.modelName?.trim() || input.upstreamModel,
    aliases: "",
    enabled: true,
    rewrite_response_model: true,
    capabilities: deriveCapabilities(input.capabilities),
    routes: [emptyRoute(input.providerId, defaultRoutePriority(0), input.upstreamModel, accountCount > 0)],
  };
}

export function isProviderModelMapped(
  models: ModelRecord[],
  routesByModel: Record<string, RouteRecord[]>,
  providerId: string,
  upstreamModel: string,
): boolean {
  return models.some((model) =>
    (routesByModel[model.ID] || []).some(
      (route) => route.ProviderID === providerId && route.UpstreamModel === upstreamModel,
    ),
  );
}

export function looksLikeUpstreamModelID(value: string): boolean {
  const trimmed = value.trim();
  if (!trimmed) return false;
  return /^[a-z0-9][a-z0-9._/-]*$/i.test(trimmed) && !/\s/.test(trimmed);
}

export function resolveCanonicalUpstreamModel(model: ModelRecord, routes: RouteFormData[]): string {
  const sorted = sortRouteForms(routes);
  const enabledUpstream = sorted.find((route) => route.enabled && route.upstream_model.trim())?.upstream_model.trim();
  if (enabledUpstream) return enabledUpstream;

  const anyUpstream = sorted.find((route) => route.upstream_model.trim())?.upstream_model.trim();
  if (anyUpstream) return anyUpstream;

  const display = model.DisplayName?.trim() || "";
  const id = model.ID.trim();

  if (display && looksLikeUpstreamModelID(display)) return display;
  if (id.startsWith("codex-")) return id.slice("codex-".length);

  const alias = (model.Aliases || []).find((item) => looksLikeUpstreamModelID(item));
  if (alias) return alias.trim();

  if (display) return display;
  return id;
}

/** Normalize for comparison; treat `org/model` and `model` as the same leaf id. */
export function modelIdsEquivalent(left: string, right: string): boolean {
  const a = left.trim().toLowerCase();
  const b = right.trim().toLowerCase();
  if (!a || !b) return false;
  if (a === b) return true;
  const leaf = (value: string) => {
    const slash = value.lastIndexOf("/");
    return slash >= 0 ? value.slice(slash + 1) : value;
  };
  const aLeaf = leaf(a);
  const bLeaf = leaf(b);
  if (aLeaf === bLeaf) return true;
  return a.endsWith(`/${b}`) || b.endsWith(`/${a}`);
}

export function findDiscoveredUpstreamModel(
  models: DiscoveredModel[] | undefined,
  upstreamModel: string,
): DiscoveredModel | undefined {
  const needle = upstreamModel.trim();
  if (!needle || !models?.length) return undefined;
  const exact = models.find((item) => item.id.trim().toLowerCase() === needle.toLowerCase());
  if (exact) return exact;
  return models.find((item) => modelIdsEquivalent(item.id, needle));
}

/** Prefer the provider catalog id (e.g. DeepSeek `deepseek-v4-pro` vs OpenRouter `deepseek/deepseek-v4-pro`). */
export function resolveProviderUpstreamModel(
  modelsByProvider: Record<string, DiscoveredModel[]>,
  providerId: string,
  upstreamModel: string,
): string {
  const match = findDiscoveredUpstreamModel(modelsByProvider[providerId], upstreamModel);
  return match?.id?.trim() || upstreamModel.trim();
}

export function providerSupportsUpstreamModel(
  modelsByProvider: Record<string, DiscoveredModel[]>,
  providerId: string,
  upstreamModel: string,
): boolean {
  return Boolean(findDiscoveredUpstreamModel(modelsByProvider[providerId], upstreamModel));
}

export function providersForUpstreamModel(
  providers: ProviderOption[],
  modelsByProvider: Record<string, DiscoveredModel[]>,
  upstreamModel: string,
): ProviderOption[] {
  const normalized = upstreamModel.trim();
  if (!normalized) return providers;
  return providers.filter((provider) => providerSupportsUpstreamModel(modelsByProvider, provider.id, normalized));
}

export function syncRoutesForUpstreamModel(
  routes: RouteFormData[],
  providers: ProviderOption[],
  modelsByProvider: Record<string, DiscoveredModel[]>,
  upstreamModel: string,
  credentialCounts?: Record<string, number>,
): RouteFormData[] {
  const canonical = upstreamModel.trim();
  if (!canonical) return routes;

  const byProvider = new Map(routes.map((route) => [route.provider, route]));
  const supporting = providersForUpstreamModel(providers, modelsByProvider, canonical);
  const merged: RouteFormData[] = routes.map((route) => {
    if (!providerSupportsUpstreamModel(modelsByProvider, route.provider, canonical)) {
      return route;
    }
    const accountCount = credentialCounts?.[route.provider] ?? 0;
    return {
      ...route,
      upstream_model: resolveProviderUpstreamModel(modelsByProvider, route.provider, canonical),
      enabled: accountCount > 0 ? route.enabled : false,
    };
  });

  for (const provider of supporting) {
    if (byProvider.has(provider.id)) continue;
    const accountCount = credentialCounts?.[provider.id] ?? 1;
    merged.push(
      emptyRoute(
        provider.id,
        defaultRoutePriority(merged.length),
        resolveProviderUpstreamModel(modelsByProvider, provider.id, canonical),
        accountCount > 0,
      ),
    );
  }

  return reorderRoutePriorities(merged);
}

export function routeFormsEqual(left: RouteFormData[], right: RouteFormData[]): boolean {
  if (left.length !== right.length) return false;
  return left.every((route, index) => {
    const other = right[index];
    return (
      route.id === other.id &&
      route.provider === other.provider &&
      route.upstream_model === other.upstream_model &&
      route.priority === other.priority &&
      route.weight === other.weight &&
      route.enabled === other.enabled
    );
  });
}

export type ModelCardRoute = {
  key: string;
  provider: string;
  providerLabel: string;
  upstreamModel: string;
  enabled: boolean;
  priority: number;
  saved: boolean;
  enabledPosition: number;
};

export function providerDisplayLabel(providers: ProviderOption[], providerId: string): string {
  const provider = providers.find((item) => item.id === providerId);
  if (!provider) return providerId;
  const match = provider.label.match(/^(.+?)\s*\(/);
  return match?.[1]?.trim() || provider.label;
}

export function buildModelCardRoutes(
  model: ModelRecord,
  savedRoutes: RouteRecord[],
  providers: ProviderOption[],
  modelsByProvider: Record<string, DiscoveredModel[]>,
  credentialCounts?: Record<string, number>,
): ModelCardRoute[] {
  const savedForms = modelToForm(model, savedRoutes).routes;
  const canonicalUpstream = resolveCanonicalUpstreamModel(model, savedForms);
  const synced = syncRoutesForUpstreamModel(savedForms, providers, modelsByProvider, canonicalUpstream, credentialCounts);
  const savedProviders = new Set(savedForms.map((route) => route.provider));
  const enabledSynced = synced.filter((route) => route.enabled);

  return synced.map((route) => {
    const enabledPosition = route.enabled
      ? enabledSynced.findIndex((item) => item.provider === route.provider) + 1
      : 0;
    return {
      key: route.id || `${route.provider}-${route.upstream_model}`,
      provider: route.provider,
      providerLabel: providerDisplayLabel(providers, route.provider),
      upstreamModel: route.upstream_model,
      enabled: route.enabled,
      priority: route.priority,
      saved: savedProviders.has(route.provider),
      enabledPosition,
    };
  });
}

export function routesToFormData(routes: RouteRecord[]): RouteFormData[] {
  return sortRoutes(routes).map((route) => ({
    id: route.ID,
    provider: route.ProviderID,
    upstream_model: route.UpstreamModel,
    priority: route.Priority,
    weight: route.Weight && route.Weight > 0 ? route.Weight : 1,
    enabled: route.Enabled,
  }));
}

export type DiscoveredPpmEntry = {
  upstreamModel: string;
  name: string;
  capabilities: string[];
  providerIds: string[];
};

/** Upstream models exposed by provider accounts that are not yet public PPM models. */
export function collectUnmappedDiscoveredModels(
  models: ModelRecord[],
  routesByModel: Record<string, RouteRecord[]>,
  providers: ProviderOption[],
  modelsByProvider: Record<string, DiscoveredModel[]>,
): DiscoveredPpmEntry[] {
  // Model-level dedup: a model already created in PPM should not appear as "available"
  const mappedModelKeys = new Set<string>();
  for (const model of models) {
    const display = model.DisplayName?.trim().toLowerCase();
    if (display) mappedModelKeys.add(display);
    const id = model.ID.trim().toLowerCase();
    if (id) mappedModelKeys.add(id);
  }

  // Per-provider dedup: only hide a (provider, upstreamModel) pair that is already routed
  const mappedPairs = new Set<string>();
  for (const model of models) {
    for (const route of routesByModel[model.ID] || []) {
      const pair = `${route.ProviderID}::${route.UpstreamModel.trim().toLowerCase()}`;
      if (pair) mappedPairs.add(pair);
    }
  }

  const byUpstream = new Map<string, DiscoveredPpmEntry>();
  for (const provider of providers) {
    for (const item of modelsByProvider[provider.id] || []) {
      const upstreamModel = item.id.trim();
      if (!upstreamModel) continue;
      const key = upstreamModel.toLowerCase();

      // Skip if this upstream model is already a PPM model ID or display name
      if (mappedModelKeys.has(key)) continue;

      // Skip only if this specific (provider, upstreamModel) pair is already routed
      const pairKey = `${provider.id}::${key}`;
      if (mappedPairs.has(pairKey)) continue;

      const existing = byUpstream.get(key);
      if (existing) {
        if (!existing.providerIds.includes(provider.id)) {
          existing.providerIds.push(provider.id);
        }
        if (!existing.name && item.name?.trim()) {
          existing.name = item.name.trim();
        }
        if (existing.capabilities.length === 0 && item.capabilities?.length) {
          existing.capabilities = [...item.capabilities];
        }
        continue;
      }

      byUpstream.set(key, {
        upstreamModel,
        name: item.name?.trim() || upstreamModel,
        capabilities: item.capabilities?.length ? [...item.capabilities] : [],
        providerIds: [provider.id],
      });
    }
  }

  return [...byUpstream.values()].sort((left, right) => {
    const nameCmp = left.name.localeCompare(right.name);
    return nameCmp !== 0 ? nameCmp : left.upstreamModel.localeCompare(right.upstreamModel);
  });
}
