import type { ComboFormData, ComboModelOption, ComboPreset } from "./types";

export const COMBO_ID_REGEX = /^[a-zA-Z0-9_.-]+$/;

export function emptyComboForm(): ComboFormData {
  return {
    id: "",
    display_name: "",
    enabled: true,
    rewrite_response_model: true,
    items: [],
    policy: {},
  };
}

export function comboToForm(combo: {
  id: string;
  display_name?: string;
  enabled?: boolean;
  rewrite_response_model?: boolean;
  items?: { public_model_id: string; route_target_id?: string }[];
  policy?: Record<string, unknown>;
}): ComboFormData {
  return {
    id: combo.id,
    display_name: combo.display_name || combo.id,
    enabled: combo.enabled !== false,
    rewrite_response_model: combo.rewrite_response_model !== false,
    items: (combo.items || []).map((item) => ({
      public_model_id: item.public_model_id,
      route_target_id: item.route_target_id || "",
    })),
    policy: combo.policy || {},
  };
}

export function formToPayload(form: ComboFormData) {
  return {
    id: form.id.trim(),
    display_name: form.display_name.trim() || form.id.trim(),
    enabled: form.enabled,
    rewrite_response_model: form.rewrite_response_model,
    capabilities: ["text", "tools"],
    items: form.items.map((item) => ({
      public_model_id: item.public_model_id,
      ...(item.route_target_id?.trim() ? { route_target_id: item.route_target_id.trim() } : {}),
    })),
    ...(form.policy && Object.keys(form.policy).length > 0 ? { policy: form.policy } : {}),
  };
}

export function validateComboForm(form: ComboFormData, existingIds: string[], editing: boolean) {
  const id = form.id.trim();
  if (!id) return "Combo ID is required";
  if (!COMBO_ID_REGEX.test(id)) return "Combo ID may only contain letters, numbers, -, _, and .";
  if (!editing && existingIds.includes(id)) return `Combo ID "${id}" already exists`;
  if (!form.display_name.trim()) return "Display name is required";
  if (form.items.length === 0) return "Add at least one virtual model";
  if (form.items.some((item) => !item.public_model_id)) return "Each step needs a virtual model";
  return "";
}

function pickModels(modelIds: string[], available: ComboModelOption[]) {
  const enabled = available.filter((model) => model.enabled);
  const picked: string[] = [];
  for (const id of modelIds) {
    if (enabled.some((model) => model.id === id) && !picked.includes(id)) {
      picked.push(id);
    }
  }
  for (const model of enabled) {
    if (!picked.includes(model.id)) picked.push(model.id);
  }
  return picked.slice(0, 4);
}

export function buildComboPresets(models: ComboModelOption[]): ComboPreset[] {
  const modelIds = models.filter((model) => model.enabled).map((model) => model.id);
  const primary = pickModels(["td-coder-pro", ...modelIds], models);
  const secondary = pickModels(modelIds.filter((id) => id !== primary[0]), models);

  return [
    {
      id: "claude-code-fallback",
      title: "Claude Code fallback",
      description: "Ordered fallback for Claude Code via Anthropic /v1/messages. Use this combo ID as ANTHROPIC_DEFAULT_SONNET_MODEL.",
      client: "claude-code",
      comboId: "claude-code-fallback",
      display_name: "Claude Code Fallback",
      rewrite_response_model: true,
      modelIds: primary,
      policy: { client: "claude-code", protocol: "claude", routing: "ordered-fallback" },
    },
    {
      id: "cowork-fallback",
      title: "Cowork fallback",
      description: "Ordered fallback for Claude Desktop Cowork third-party inference. Add this combo ID to allowed models.",
      client: "cowork",
      comboId: "cowork-fallback",
      display_name: "Cowork Fallback",
      rewrite_response_model: true,
      modelIds: primary.length > 1 ? primary : secondary,
      policy: { client: "cowork", protocol: "claude", routing: "ordered-fallback" },
    },
    {
      id: "general-fallback",
      title: "General fallback",
      description: "Default ordered fallback across your virtual models for any OpenAI-compatible client.",
      client: "general",
      comboId: "general-fallback",
      display_name: "General Fallback",
      rewrite_response_model: true,
      modelIds: primary,
      policy: { routing: "ordered-fallback" },
    },
  ];
}

export function presetToForm(preset: ComboPreset, existingIds: string[]): ComboFormData | null {
  if (preset.modelIds.length === 0) return null;
  let comboId = preset.comboId;
  if (existingIds.includes(comboId)) {
    let suffix = 2;
    while (existingIds.includes(`${comboId}-${suffix}`)) suffix += 1;
    comboId = `${comboId}-${suffix}`;
  }
  return {
    id: comboId,
    display_name: preset.display_name,
    enabled: true,
    rewrite_response_model: preset.rewrite_response_model,
    items: preset.modelIds.map((public_model_id) => ({ public_model_id, route_target_id: "" })),
    policy: preset.policy,
  };
}

export function clientLabel(client: ComboPreset["client"]) {
  switch (client) {
    case "claude-code":
      return "Claude Code";
    case "cowork":
      return "Cowork";
    default:
      return "General";
  }
}
