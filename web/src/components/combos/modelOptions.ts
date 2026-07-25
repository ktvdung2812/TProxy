import { useMemo } from "react";
import type { ChatModelOption } from "../chat/types";
import { modelOptionGroupLabel, type ModelOption } from "../../lib/modelOptions";
import { buildMappingTargetOptions } from "../mapping/modelOptions";
import type { ModelRecord, RouteRecord } from "../models/types";

type Combo = {
  id: string;
  display_name?: string;
  enabled?: boolean;
};

export function buildComboStepOptions(
  models: ModelRecord[],
  combos: Combo[],
  routesByModel: Record<string, RouteRecord[]>,
  discoveredModels: ChatModelOption[] = [],
  excludeComboId = "",
): ModelOption[] {
  const options = buildMappingTargetOptions(models, combos, routesByModel, discoveredModels);
  if (!excludeComboId) return options;
  return options.filter((option) => option.value !== excludeComboId);
}

export { modelOptionGroupLabel };

export function useComboStepOptions(
  models: ModelRecord[],
  combos: Combo[],
  routesByModel: Record<string, RouteRecord[]>,
  discoveredModels: ChatModelOption[],
  excludeComboId = "",
) {
  return useMemo(
    () => buildComboStepOptions(models, combos, routesByModel, discoveredModels, excludeComboId),
    [models, combos, routesByModel, discoveredModels, excludeComboId],
  );
}
