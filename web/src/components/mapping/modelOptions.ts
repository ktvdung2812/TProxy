import { useMemo } from "react";
import type { ChatModelOption } from "../chat/types";
import { buildModelOptions, type ModelOption } from "../../lib/modelOptions";
import type { ModelRecord, RouteRecord } from "../models/types";

type Combo = {
  id: string;
  display_name?: string;
  enabled?: boolean;
};

function providerSelectorValue(providerId: string, upstreamModel: string): string {
  return `${providerId}:${upstreamModel}`;
}

export function buildMappingTargetOptions(
  models: ModelRecord[],
  combos: Combo[],
  routesByModel: Record<string, RouteRecord[]>,
  discoveredModels: ChatModelOption[] = [],
): ModelOption[] {
  const options = buildModelOptions(models, combos);
  const seen = new Set(options.map((option) => option.value));

  for (const model of models) {
    if (model.Enabled === false) continue;
    for (const route of routesByModel[model.ID] || []) {
      if (route.Enabled === false || !route.ProviderID || !route.UpstreamModel) continue;
      const value = providerSelectorValue(route.ProviderID, route.UpstreamModel);
      if (seen.has(value)) continue;
      seen.add(value);
      options.push({
        value,
        label: `${route.ProviderID} / ${route.UpstreamModel} (${value})`,
        group: "providers",
      });
    }
  }

  for (const item of discoveredModels) {
    const value = item.requestModel || item.id;
    if (!value || seen.has(value)) continue;
    seen.add(value);
    const label = item.group && item.group !== "Virtual models" && item.group !== "Combos"
      ? `${item.group} / ${item.name} (${value})`
      : `${item.name} (${value})`;
    options.push({
      value,
      label,
      group: "providers",
    });
  }

  return options;
}

export function useMappingTargetOptions(
  models: ModelRecord[],
  combos: Combo[],
  routesByModel: Record<string, RouteRecord[]>,
  discoveredModels: ChatModelOption[],
) {
  return useMemo(
    () => buildMappingTargetOptions(models, combos, routesByModel, discoveredModels),
    [models, combos, routesByModel, discoveredModels],
  );
}
