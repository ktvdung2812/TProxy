export type ModelOption = {
  value: string;
  label: string;
  group: "models" | "combos" | "suggested" | "providers";
};

type VirtualModel = {
  ID: string;
  DisplayName?: string;
  Enabled?: boolean;
};

type Combo = {
  id: string;
  display_name?: string;
  enabled?: boolean;
};

type SuggestedSlot = {
  id: string;
  name: string;
  alias: string;
};

const GROUP_LABELS: Record<ModelOption["group"], string> = {
  models: "Virtual models",
  combos: "Combos",
  suggested: "Suggested",
  providers: "Provider models",
};

export function modelOptionGroupLabel(group: ModelOption["group"]): string {
  return GROUP_LABELS[group];
}

export function buildModelOptions(
  models: VirtualModel[],
  combos: Combo[],
  toolDefaults?: SuggestedSlot[],
): ModelOption[] {
  const options: ModelOption[] = [];
  const seen = new Set<string>();

  for (const model of models) {
    if (model.Enabled === false) continue;
    if (seen.has(model.ID)) continue;
    seen.add(model.ID);
    options.push({
      value: model.ID,
      label: model.DisplayName ? `${model.DisplayName} (${model.ID})` : model.ID,
      group: "models",
    });
  }

  for (const combo of combos) {
    if (combo.enabled === false) continue;
    if (seen.has(combo.id)) continue;
    seen.add(combo.id);
    options.push({
      value: combo.id,
      label: combo.display_name ? `${combo.display_name} (${combo.id})` : combo.id,
      group: "combos",
    });
  }

  for (const slot of toolDefaults ?? []) {
    if (seen.has(slot.alias) || seen.has(slot.id)) continue;
    seen.add(slot.alias);
    options.push({
      value: slot.alias,
      label: `${slot.name} (suggested)`,
      group: "suggested",
    });
  }

  return options;
}
