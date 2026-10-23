import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import type { CLITool } from "../../cli-tools/constants";
import type { ModelOption } from "../../lib/modelOptions";
import { cn, Field, Select } from "../ui";
import { IconAction } from "../page-kit";
import type { CLIToolStatus } from "./api";
import { ClaudeCodeOptions, type TierOverrides } from "./ClaudeCodeOptions";
import { CoworkPluginPicker } from "./CoworkPluginPicker";

/** Tools whose config can hold more than one model at a time. */
const MULTI_MODEL_TOOLS = new Set(["opencode", "droid", "copilot", "openclaw", "cowork"]);
/** Tools with a distinct subagent/explorer model slot. */
const SUBAGENT_TOOLS = new Set(["claude", "codex", "opencode"]);

export type ApplyOptionsState = {
  extraModels: string[];
  subagentModel: string;
  tierOverrides: TierOverrides;
  /** null = not chosen yet; apply then falls back to the server defaults. */
  activePlugins: string[] | null;
};

export const EMPTY_APPLY_OPTIONS: ApplyOptionsState = {
  extraModels: [],
  subagentModel: "",
  tierOverrides: {},
  activePlugins: null,
};

/** True when the tool has any option worth showing on this host. */
export function hasApplyOptions(tool: CLITool, modelOptions: ModelOption[], status: CLIToolStatus | null): boolean {
  const withModels = modelOptions.length > 0 && (MULTI_MODEL_TOOLS.has(tool.id) || SUBAGENT_TOOLS.has(tool.id));
  return withModels || tool.id === "claude" || (tool.id === "cowork" && (status?.plugins?.length ?? 0) > 0);
}

type Props = {
  tool: CLITool;
  secret: string;
  model: string;
  modelOptions: ModelOption[];
  status: CLIToolStatus | null;
  value: ApplyOptionsState;
  onChange: (next: ApplyOptionsState) => void;
};

/**
 * Extra settings only the auto-apply writer understands: additional models,
 * the subagent slot, Claude tier overrides and Cowork's MCP servers. Collapsed
 * by default — the defaults are right for most setups.
 */
export function ApplyOptions({ tool, secret, model, modelOptions, status, value, onChange }: Props) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const supportsMultiModel = MULTI_MODEL_TOOLS.has(tool.id) && modelOptions.length > 0;
  const supportsSubagent = SUBAGENT_TOOLS.has(tool.id) && modelOptions.length > 0;
  const set = (patch: Partial<ApplyOptionsState>) => onChange({ ...value, ...patch });

  const availableExtras = useMemo(
    () => modelOptions.filter((option) => option.value !== model && !value.extraModels.includes(option.value)),
    [modelOptions, model, value.extraModels],
  );

  const changed =
    value.extraModels.length + (value.subagentModel ? 1 : 0) + Object.keys(value.tierOverrides).length;

  return (
    <div className={cn("ct-options", open && "is-open")}>
      <button type="button" className="ct-options-toggle" aria-expanded={open} onClick={() => setOpen((prev) => !prev)}>
        <span className="material-symbols-outlined" aria-hidden>{open ? "expand_less" : "expand_more"}</span>
        <span>{t("cliTools.options.title")}</span>
        {changed > 0 ? <span className="cp-badge-count">{changed}</span> : null}
        <small>{t("cliTools.options.hint")}</small>
      </button>

      {open ? (
        <div className="ct-options-body">
          {supportsMultiModel ? (
            <div className="ct-option-group">
              <Field label={t("cliTools.extraModels")} hint={t("cliTools.extraModelsDesc")}>
                <Select
                  value=""
                  onChange={(event) => {
                    const next = event.target.value;
                    if (next) set({ extraModels: [...value.extraModels, next] });
                  }}
                >
                  <option value="">{t("cliTools.addModel")}…</option>
                  {availableExtras.map((option) => (
                    <option key={option.value} value={option.value}>
                      {option.label}
                    </option>
                  ))}
                </Select>
              </Field>
              {value.extraModels.length > 0 ? (
                <ul className="ct-chip-list">
                  {value.extraModels.map((entry) => (
                    <li key={entry}>
                      <code>{entry}</code>
                      <IconAction
                        icon="close"
                        label={t("cliTools.removeModelNamed", { model: entry })}
                        onClick={() => set({ extraModels: value.extraModels.filter((item) => item !== entry) })}
                      />
                    </li>
                  ))}
                </ul>
              ) : null}
            </div>
          ) : null}

          {supportsSubagent ? (
            <Field label={t("cliTools.subagentModel")} hint={t("cliTools.subagentModelDesc")}>
              <Select value={value.subagentModel} onChange={(event) => set({ subagentModel: event.target.value })}>
                <option value="">{model || "—"}</option>
                {modelOptions.map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </Select>
            </Field>
          ) : null}

          {tool.id === "claude" ? (
            <ClaudeCodeOptions
              modelOptions={modelOptions}
              overrides={value.tierOverrides}
              onChange={(tierOverrides) => set({ tierOverrides })}
              secret={secret}
            />
          ) : null}

          {tool.id === "cowork" ? (
            <CoworkPluginPicker
              catalog={status?.plugins ?? []}
              active={value.activePlugins}
              onChange={(activePlugins) => set({ activePlugins })}
            />
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
