import type { ModelOption } from "../../lib/modelOptions";
import type { ComboModelOption, ComboPreset, ComboRecord } from "./types";

/* ============================================================
   Derived health for the Combos page: one view per combo with
   its resolved steps, so cards, rows and stats agree on what
   "ready" or "broken" means.
   ============================================================ */

export type ComboHealth = "ready" | "partial" | "broken" | "paused";

/**
 * ok: an enabled public model, combo or known provider model.
 * direct: a provider:model selector we cannot verify before discovery ends.
 * off: points at a model or combo that is turned off.
 * missing: points at an ID that no longer exists.
 */
export type ComboStepState = "ok" | "direct" | "off" | "missing";

export type ComboStep = {
  key: string;
  id: string;
  label: string;
  /** Secondary text: the raw ID when the label is a display name. */
  detail: string;
  kind: "model" | "combo" | "provider" | "unknown";
  state: ComboStepState;
};

export type ComboClient = ComboPreset["client"];

export type ComboView = {
  combo: ComboRecord;
  name: string;
  client: ComboClient;
  health: ComboHealth;
  steps: ComboStep[];
  /** Steps that can take a request right now. */
  usableCount: number;
  /** 1-based position of the first usable step, 0 when none. */
  firstUsable: number;
};

export const HEALTH_RANK: Record<ComboHealth, number> = { broken: 0, partial: 1, ready: 2, paused: 3 };

export function comboClient(combo: ComboRecord): ComboClient {
  const client = typeof combo.policy?.client === "string" ? combo.policy.client : "";
  return client === "claude-code" || client === "cowork" ? client : "general";
}

/** Strips the trailing " (id)" that option labels carry, so cards show the name once. */
function labelName(label: string, id: string) {
  const suffix = ` (${id})`;
  return label.endsWith(suffix) ? label.slice(0, -suffix.length) : label;
}

export function buildComboViews(
  combos: ComboRecord[],
  models: ComboModelOption[],
  modelOptions: ModelOption[],
): ComboView[] {
  const modelById = new Map(models.map((model) => [model.id, model]));
  const comboById = new Map(combos.map((combo) => [combo.id, combo]));
  const optionByValue = new Map(modelOptions.map((option) => [option.value, option]));

  const resolveStep = (id: string, index: number): ComboStep => {
    const key = `${index}-${id}`;
    const model = modelById.get(id);
    if (model) {
      const name = labelName(model.label, id);
      return { key, id, label: name, detail: name === id ? "" : id, kind: "model", state: model.enabled ? "ok" : "off" };
    }
    const nested = comboById.get(id);
    if (nested) {
      const name = nested.display_name || id;
      return { key, id, label: name, detail: name === id ? "" : id, kind: "combo", state: nested.enabled ? "ok" : "off" };
    }
    const option = optionByValue.get(id);
    if (option) {
      const name = labelName(option.label, id);
      return { key, id, label: name, detail: name === id ? "" : id, kind: option.group === "combos" ? "combo" : "provider", state: "ok" };
    }
    if (id.includes(":")) return { key, id, label: id, detail: "", kind: "provider", state: "direct" };
    return { key, id, label: id, detail: "", kind: "unknown", state: "missing" };
  };

  return combos.map((combo) => {
    const steps = (combo.items || []).map((item, index) => resolveStep(item.public_model_id, index));
    const usable = steps.filter((step) => step.state === "ok" || step.state === "direct");
    let health: ComboHealth;
    if (!combo.enabled) health = "paused";
    else if (usable.length === 0) health = "broken";
    else if (usable.length < steps.length) health = "partial";
    else health = "ready";
    return {
      combo,
      name: combo.display_name || combo.id,
      client: comboClient(combo),
      health,
      steps,
      usableCount: usable.length,
      firstUsable: usable.length ? steps.indexOf(usable[0]) + 1 : 0,
    };
  });
}
