import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { isAutoApplyTool, type CLITool } from "../../cli-tools/constants";
import { isLocalDashboardHost } from "../../devDefaults";
import type { ModelOption } from "../../lib/modelOptions";
import { Badge, Button, Field, Select } from "../ui";
import {
  applyCLIToolConfig,
  fetchCLIToolStatus,
  resetCLIToolConfig,
  type CLIToolStatus,
} from "./api";
import { buildManualConfigs } from "./manualConfigs";
import { ManualConfigModal } from "./ManualConfigModal";
import { CLIApplyScriptBlock } from "./CLIApplyScriptBlock";
import { ClaudeCodeOptions, type TierOverrides } from "./ClaudeCodeOptions";
import { CoworkPluginPicker } from "./CoworkPluginPicker";
import { INSTALL_GUIDES } from "./installGuides";

type Props = {
  tool: CLITool;
  secret: string;
  apiKey: string;
  model: string;
  baseUrl: string;
  /** Every base URL this dashboard can hand out — used to detect "Other endpoint". */
  knownBaseUrls?: string[];
  /** Virtual models/combos available for the extra-model and subagent pickers. */
  modelOptions?: ModelOption[];
  onApiKeyChange: (value: string) => void;
  hideScript?: boolean;
};

/** Tools whose config can hold more than one model at a time. */
const MULTI_MODEL_TOOLS = new Set(["opencode", "droid", "copilot", "openclaw", "cowork"]);
/** Tools with a distinct subagent/explorer model slot. */
const SUBAGENT_TOOLS = new Set(["claude", "codex", "opencode"]);

type ConfigState = "connected" | "other" | "not_configured";

function sameEndpoint(a: string, b: string): boolean {
  const strip = (value: string) => value.trim().replace(/\/+$/, "").replace(/\/v1$/, "").toLowerCase();
  return Boolean(a) && Boolean(b) && strip(a) === strip(b);
}

/**
 * Three-way status like 9router's matchKnownEndpoint: a tool pointed at an endpoint
 * this dashboard never hands out is "Other", not "Connected" — otherwise a stale
 * config silently reads as healthy.
 */
export function configState(status: CLIToolStatus | null, knownBaseUrls: string[]): ConfigState | null {
  if (!status?.installed) return null;
  const endpoint = status.endpoint ?? "";
  if (!endpoint) return status.has_tproxy || status.has_9router ? "connected" : "not_configured";
  if (knownBaseUrls.some((known) => sameEndpoint(known, endpoint))) return "connected";
  if (status.has_tproxy || status.has_9router) return "connected";
  return "other";
}

export function supportsAutoApply(toolId: string): boolean {
  return isAutoApplyTool(toolId);
}

export function CLIToolApplyPanel({
  tool,
  secret,
  apiKey,
  model,
  baseUrl,
  knownBaseUrls = [],
  modelOptions = [],
  onApiKeyChange,
  hideScript = false,
}: Props) {
  const { t } = useTranslation();
  const isLocal = isLocalDashboardHost();
  const canAutoApply = isLocal && supportsAutoApply(tool.id);
  const [status, setStatus] = useState<CLIToolStatus | null>(null);
  const [loading, setLoading] = useState(false);
  const [applying, setApplying] = useState(false);
  const [resetting, setResetting] = useState(false);
  const [message, setMessage] = useState<{ type: "success" | "error"; text: string } | null>(null);
  const [manualOpen, setManualOpen] = useState(false);
  const [showInstall, setShowInstall] = useState(false);
  const [extraModels, setExtraModels] = useState<string[]>([]);
  const [subagentModel, setSubagentModel] = useState("");
  const [tierOverrides, setTierOverrides] = useState<TierOverrides>({});
  const [activePlugins, setActivePlugins] = useState<string[] | null>(null);

  const supportsMultiModel = MULTI_MODEL_TOOLS.has(tool.id);
  const supportsSubagent = SUBAGENT_TOOLS.has(tool.id);
  const isCowork = tool.id === "cowork";
  const installGuide = INSTALL_GUIDES[tool.id];

  const loadStatus = useCallback(async () => {
    if (!supportsAutoApply(tool.id)) return;
    setLoading(true);
    try {
      const next = await fetchCLIToolStatus(secret, tool.id);
      setStatus(next);
      if (next.active_plugins) setActivePlugins(next.active_plugins);
    } catch (error) {
      setStatus({ installed: false, has_tproxy: false, has_9router: false, message: String(error) });
    } finally {
      setLoading(false);
    }
  }, [secret, tool.id]);

  useEffect(() => {
    void loadStatus();
  }, [loadStatus]);

  const state = configState(status, knownBaseUrls);
  const manualConfigs = useMemo(
    () => buildManualConfigs(tool, baseUrl, apiKey, model),
    [tool, baseUrl, apiKey, model],
  );

  // The primary model always leads; extras are registered alongside it.
  const allModels = useMemo(
    () => [model, ...extraModels].filter((value, index, list) => value && list.indexOf(value) === index),
    [model, extraModels],
  );

  const availableExtras = useMemo(
    () => modelOptions.filter((option) => option.value !== model && !extraModels.includes(option.value)),
    [modelOptions, model, extraModels],
  );

  // Cowork wants full MCP entries, not names; resolve the picker's selection back
  // against the catalog the status endpoint returned.
  const selectedCoworkPlugins = useMemo(
    () => (status?.plugins ?? []).filter((plugin) => (activePlugins ?? []).includes(plugin.name)),
    [status?.plugins, activePlugins],
  );

  const handleApply = async () => {
    setApplying(true);
    setMessage(null);
    try {
      await applyCLIToolConfig(secret, tool.id, {
        baseUrl,
        apiKey: apiKey.trim(),
        model,
        models: allModels.length > 1 ? allModels : undefined,
        subagentModel: subagentModel || undefined,
        env: Object.keys(tierOverrides).length > 0 ? tierOverrides : undefined,
        plugins: isCowork && activePlugins ? selectedCoworkPlugins : undefined,
      });
      setMessage({
        type: "success",
        text: isCowork ? t("cliTools.coworkRestart") : t("cliTools.configApplied"),
      });
      await loadStatus();
    } catch (error) {
      setMessage({ type: "error", text: String(error) });
    } finally {
      setApplying(false);
    }
  };

  const handleReset = async () => {
    setResetting(true);
    setMessage(null);
    try {
      await resetCLIToolConfig(secret, tool.id);
      setMessage({ type: "success", text: "tproxy settings removed from local config." });
      await loadStatus();
    } catch (error) {
      setMessage({ type: "error", text: String(error) });
    } finally {
      setResetting(false);
    }
  };

  if (!supportsAutoApply(tool.id)) {
    return null;
  }

  const renderBadge = () => {
    if (state === "connected") return <Badge variant="success">{t("cliTools.connected")}</Badge>;
    if (state === "other") return <Badge variant="default">{t("cliTools.statusOther")}</Badge>;
    if (state === "not_configured") return <Badge variant="warning">{t("cliTools.notConfigured")}</Badge>;
    return null;
  };

  return (
    <div className="cli-tool-apply-panel">
      <div className="cli-tool-apply-head">
        <div>
          <p className="cli-tool-apply-title">Configuration</p>
          <p className="cli-tool-apply-desc">
            {canAutoApply ? t("cliTools.autoApplyDirect") : t("cliTools.autoApplyHint")}
          </p>
        </div>
        {renderBadge()}
      </div>

      {loading ? (
        <p className="cli-tool-hint">Checking local installation…</p>
      ) : status && !status.installed ? (
        <div className="cli-tool-note cli-tool-note-warning">
          <span className="material-symbols-outlined">warning</span>
          <div>
            <p>
              {tool.name} not detected locally. You can still use{" "}
              <button type="button" className="cli-tool-inline-link" onClick={() => setManualOpen(true)}>
                manual configuration
              </button>{" "}
              if tproxy runs on another host.
            </p>
            {installGuide ? (
              <Button variant="outline" size="sm" onClick={() => setShowInstall((prev) => !prev)}>
                {showInstall ? t("cliTools.hideInstallGuide") : t("cliTools.installGuide")}
              </Button>
            ) : null}
          </div>
        </div>
      ) : null}

      {showInstall && installGuide ? (
        <div className="cli-tool-install-guide">
          <p className="cli-tool-step-title">{t("cliTools.installGuide")}</p>
          {installGuide.steps.map((step) => (
            <p key={step} className="cli-tool-step-desc">
              {step}
            </p>
          ))}
          {installGuide.command ? (
            <pre>
              <code>{installGuide.command}</code>
            </pre>
          ) : null}
          {installGuide.docsUrl ? (
            <p className="cli-tool-hint">
              <a href={installGuide.docsUrl} target="_blank" rel="noreferrer">
                Documentation
              </a>
            </p>
          ) : null}
        </div>
      ) : null}

      {state === "other" && status?.endpoint ? (
        <div className="cli-tool-note cli-tool-note-info">
          <span className="material-symbols-outlined">info</span>
          <p>
            {t("cliTools.statusOtherHint")} <code>{status.endpoint}</code>
          </p>
        </div>
      ) : null}

      {status?.config_path || status?.settings_path ? (
        <p className="cli-tool-hint">
          Config path: <code>{status.config_path || status.settings_path}</code>
        </p>
      ) : tool.settingsFile ? (
        <p className="cli-tool-hint">
          Config file: <code>{tool.settingsFile}</code>
        </p>
      ) : null}

      {canAutoApply && supportsMultiModel && modelOptions.length > 0 ? (
        <div className="cli-tool-field-stack">
          <Field label={t("cliTools.extraModels")} hint={t("cliTools.extraModelsDesc")}>
            <Select
              value=""
              onChange={(event) => {
                const next = event.target.value;
                if (next) setExtraModels((prev) => [...prev, next]);
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
          {extraModels.length > 0 ? (
            <ul className="cli-tool-model-list">
              {extraModels.map((entry) => (
                <li key={entry}>
                  <code>{entry}</code>
                  <button
                    type="button"
                    className="cli-tool-inline-link"
                    onClick={() => setExtraModels((prev) => prev.filter((item) => item !== entry))}
                  >
                    {t("cliTools.removeModel")}
                  </button>
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}

      {canAutoApply && supportsSubagent && modelOptions.length > 0 ? (
        <Field label={t("cliTools.subagentModel")} hint={t("cliTools.subagentModelDesc")}>
          <Select value={subagentModel} onChange={(event) => setSubagentModel(event.target.value)}>
            <option value="">{model || "—"}</option>
            {modelOptions.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </Select>
        </Field>
      ) : null}

      {canAutoApply && tool.id === "claude" ? (
        <ClaudeCodeOptions
          modelOptions={modelOptions}
          overrides={tierOverrides}
          onChange={setTierOverrides}
          secret={secret}
        />
      ) : null}

      {canAutoApply && isCowork ? (
        <CoworkPluginPicker
          catalog={status?.plugins ?? []}
          active={activePlugins}
          onChange={setActivePlugins}
        />
      ) : null}

      <div className="cli-tool-apply-actions">
        {canAutoApply ? (
          <>
            <Button
              variant="primary"
              size="sm"
              icon="check"
              disabled={applying || !model || !apiKey.trim()}
              onClick={() => void handleApply()}
            >
              {applying ? t("cliTools.applying") : t("cliTools.applyToLocal")}
            </Button>
            <Button
              variant="outline"
              size="sm"
              icon="restart_alt"
              disabled={resetting || !(status?.has_tproxy || status?.has_9router)}
              onClick={() => void handleReset()}
            >
              {resetting ? t("cliTools.resetting") : t("common.reset")}
            </Button>
          </>
        ) : null}
        <Button variant="secondary" size="sm" icon="content_copy" onClick={() => setManualOpen(true)}>
          Manual config
        </Button>
      </div>

      {!hideScript ? (
        <CLIApplyScriptBlock configs={manualConfigs} disabled={!model || !apiKey.trim()} />
      ) : null}

      {!canAutoApply ? (
        <div className="cli-tool-note cli-tool-note-info">
          <span className="material-symbols-outlined">info</span>
          <p>
            Open <strong>Manual config</strong> to copy file contents, then paste them into the paths shown above on the
            machine where {tool.name} runs. Create API keys in <Link to="/apis">APIs</Link>.
          </p>
        </div>
      ) : null}

      {message ? (
        <div className={`cli-tool-note cli-tool-note-${message.type === "success" ? "info" : "error"}`}>
          <span className="material-symbols-outlined">{message.type === "success" ? "check_circle" : "error"}</span>
          <p>{message.text}</p>
        </div>
      ) : null}

      <ManualConfigModal
        open={manualOpen}
        onClose={() => setManualOpen(false)}
        title={`${tool.name} — manual configuration`}
        configs={manualConfigs}
      />
    </div>
  );
}
