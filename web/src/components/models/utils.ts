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

export function emptyRoute(provider = "", priority = 100, upstreamModel = ""): RouteFormData {
  return {
    id: "",
    provider,
    upstream_model: upstreamModel,
    priority,
    weight: 1,
    enabled: true,
  };
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
    routes: form.routes.map((route) => ({
      ...(route.id.trim() ? { id: route.id.trim() } : {}),
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
  if (!routes.some((route) => route.enabled)) return "Enable at least one provider route";
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
}): ModelFormData {
  const baseId = virtualModelIdFromSelection(input.providerId, input.upstreamModel);
  return {
    id: uniqueVirtualModelId(baseId, input.existingIds),
    display_name: input.modelName?.trim() || input.upstreamModel,
    aliases: "",
    enabled: true,
    rewrite_response_model: true,
    capabilities: deriveCapabilities(input.capabilities),
    routes: [emptyRoute(input.providerId, defaultRoutePriority(0), input.upstreamModel)],
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

export function providerSupportsUpstreamModel(
  modelsByProvider: Record<string, DiscoveredModel[]>,
  providerId: string,
  upstreamModel: string,
): boolean {
  const normalized = upstreamModel.trim().toLowerCase();
  if (!normalized) return false;
  return (modelsByProvider[providerId] || []).some((item) => item.id.trim().toLowerCase() === normalized);
}

export function providersForUpstreamModel(
  providers: ProviderOption[],
  modelsByProvider: Record<string, DiscoveredModel[]>,
  upstreamModel: string,
): ProviderOption[] {
  const normalized = upstreamModel.trim().toLowerCase();
  if (!normalized) return providers;
  return providers.filter((provider) => providerSupportsUpstreamModel(modelsByProvider, provider.id, normalized));
}

export function syncRoutesForUpstreamModel(
  routes: RouteFormData[],
  providers: ProviderOption[],
  modelsByProvider: Record<string, DiscoveredModel[]>,
  upstreamModel: string,
): RouteFormData[] {
  const canonical = upstreamModel.trim();
  if (!canonical) return routes;

  const byProvider = new Map(routes.map((route) => [route.provider, route]));
  const supporting = providersForUpstreamModel(providers, modelsByProvider, canonical);
  const merged: RouteFormData[] = routes.map((route) => {
    if (!providerSupportsUpstreamModel(modelsByProvider, route.provider, canonical)) {
      return route;
    }
    return { ...route, upstream_model: canonical };
  });

  for (const provider of supporting) {
    if (byProvider.has(provider.id)) continue;
    merged.push(emptyRoute(provider.id, defaultRoutePriority(merged.length), canonical));
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
): ModelCardRoute[] {
  const savedForms = modelToForm(model, savedRoutes).routes;
  const canonicalUpstream = resolveCanonicalUpstreamModel(model, savedForms);
  const synced = syncRoutesForUpstreamModel(savedForms, providers, modelsByProvider, canonicalUpstream);
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
