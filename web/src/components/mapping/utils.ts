import { resolveCanonicalUpstreamModel } from "../models/utils";
import type { ModelRecord, RouteRecord } from "../models/types";

function upstreamLeafName(upstream: string): string {
  const trimmed = upstream.trim();
  if (!trimmed) return trimmed;
  const slash = trimmed.lastIndexOf("/");
  if (slash >= 0 && slash < trimmed.length - 1) {
    return trimmed.slice(slash + 1);
  }
  return trimmed;
}

function parseProviderSelector(target: string): string | null {
  const trimmed = target.trim();
  if (!trimmed) return null;
  if (trimmed.includes("::")) {
    const parts = trimmed.split("::");
    return parts.slice(1).join("::").trim() || null;
  }
  const colon = trimmed.indexOf(":");
  if (colon > 0 && !trimmed.includes("://")) {
    return trimmed.slice(colon + 1).trim() || null;
  }
  const slash = trimmed.indexOf("/");
  if (slash > 0 && !trimmed.includes("://")) {
    return trimmed.slice(slash + 1).trim() || null;
  }
  return null;
}

function routesToForm(routes: RouteRecord[]) {
  return routes.map((route) => ({
    id: route.ID,
    provider: route.ProviderID,
    upstream_model: route.UpstreamModel,
    priority: route.Priority,
    weight: route.Weight && route.Weight > 0 ? route.Weight : 1,
    enabled: route.Enabled,
  }));
}

/** Show upstream model name for mapping targets (hide provider / virtual-model prefix). */
export function formatMappingTargetLabel(
  target: string,
  models: ModelRecord[],
  routesByModel: Record<string, RouteRecord[]>,
): string {
  const trimmed = target.trim();
  if (!trimmed) return trimmed;

  const selectorModel = parseProviderSelector(trimmed);
  if (selectorModel) {
    return upstreamLeafName(selectorModel);
  }

  const model = models.find((entry) => entry.ID.toLowerCase() === trimmed.toLowerCase());
  if (model) {
    return upstreamLeafName(resolveCanonicalUpstreamModel(model, routesToForm(routesByModel[model.ID] || [])));
  }

  if (trimmed.toLowerCase().startsWith("codex-")) {
    return trimmed.slice("codex-".length);
  }

  return trimmed;
}
