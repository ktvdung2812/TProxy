import { useEffect, useMemo, useState, useSyncExternalStore, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { isAutoApplyTool, type CLITool, type CLIToolGuideStep } from "../../cli-tools/constants";
import { isLocalDashboardHost } from "../../devDefaults";
import { getApiKeySecretsVersion, getStoredApiKeySecret, storeApiKeySecret, subscribeApiKeySecrets } from "../../lib/apiKeySecrets";
import type { ModelOption } from "../../lib/modelOptions";
import { buildCliTunnelBaseUrl } from "../../lib/cliBaseUrl";
import { needsPublicBaseUrlForCliTools } from "../../lib/publicBaseUrl";
import { fetchResolvableApiKeySecrets } from "../apis/api";
import { Button, cn, Input, Select } from "../ui";
import { applyCLIToolConfig, type CLIToolStatus } from "./api";
import { ApiKeySelect, type ApiKeyOption } from "./ApiKeySelect";
import { ApplyOptions, EMPTY_APPLY_OPTIONS, hasApplyOptions, type ApplyOptionsState } from "./ApplyOptions";
import { CLIToolApplyPanel, type ApplyMessage } from "./CLIToolApplyPanel";
import { CliBaseUrlPicker } from "./CliBaseUrlPicker";
import { CopyField, maskSecret, Note } from "./CopyBits";
import { ManualConfigModal } from "./ManualConfigModal";
import { buildGuideCommandPreview, buildManualConfigs } from "./manualConfigs";
import { ToolCommandsPanel } from "./ToolCommandsPanel";
import { ToolNotes } from "./ToolNotes";
import type { ConfigState } from "./toolState";
import type { CliGateway } from "./useCliGateway";

function stepColumn(step: CLIToolGuideStep): "config" | "commands" {
  if (step.column) return step.column;
  if (step.type === "apiKeySelector" || step.type === "modelSelector") return "config";
  const title = step.title.toLowerCase().trim();
  if (title === "base url" || title === "api key" || title === "model" || title === "default model" || title === "select model" || title === "virtual model") {
    return "config";
  }
  return "commands";
}

type Props = {
  tool: CLITool;
  models: ModelOption[];
  apiKeys: ApiKeyOption[];
  secret: string;
  gateway: CliGateway;
  status: CLIToolStatus | null;
  statusLoading: boolean;
  configState: ConfigState | null;
  configPath: string;
  onApplied: () => Promise<void> | void;
};

function SetupStep({
  index,
  title,
  description,
  done,
  aside,
  children,
}: {
  index: number;
  title: ReactNode;
  description?: ReactNode;
  done?: boolean;
  aside?: ReactNode;
  children?: ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <li className={cn("ct-step", done && "is-done")}>
      <span className="ct-step-num" aria-label={done ? t("cliTools.steps.done") : undefined}>
        {done ? <span className="material-symbols-outlined" aria-hidden>check</span> : index}
      </span>
      <div className="ct-step-body">
        <div className="ct-step-head">
          <h4>{title}</h4>
          {aside}
        </div>
        {description ? <p className="ct-step-desc">{description}</p> : null}
        {children}
      </div>
    </li>
  );
}

/**
 * The setup flow for one tool: API key → base URL → model (+ options) → apply or
 * paste, with the generated config and scripts alongside.
 */
export function ToolSetup({
  tool,
  models,
  apiKeys,
  secret,
  gateway,
  status,
  statusLoading,
  configState,
  configPath,
  onApplied,
}: Props) {
  const { t } = useTranslation();
  const autoApply = isAutoApplyTool(tool.id);
  const canAutoApply = isLocalDashboardHost() && autoApply;

  // ---- API key: prefer an enabled key whose secret this browser knows. ----
  const enabledKeys = useMemo(() => apiKeys.filter((key) => key.enabled !== false), [apiKeys]);
  const defaultKeyId = useMemo(() => {
    if (enabledKeys.length === 0) return "";
    const firstWithSecret = enabledKeys.find((key) => getStoredApiKeySecret(key.id));
    return firstWithSecret?.id ?? enabledKeys[0].id;
  }, [enabledKeys]);
  const [apiKey, setApiKey] = useState(() => (defaultKeyId ? getStoredApiKeySecret(defaultKeyId) ?? "" : ""));
  const [selectedKeyId, setSelectedKeyId] = useState(defaultKeyId);
  const secretsVersion = useSyncExternalStore(subscribeApiKeySecrets, getApiKeySecretsVersion, () => 0);

  const resolvedApiKey = useMemo(() => {
    const trimmed = apiKey.trim();
    if (trimmed) return trimmed;
    if (selectedKeyId) return getStoredApiKeySecret(selectedKeyId) ?? "";
    return "";
    // secretsVersion re-resolves when another tab or page stores a secret.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apiKey, selectedKeyId, secretsVersion]);

  useEffect(() => {
    if (!secret) return;
    let cancelled = false;
    void fetchResolvableApiKeySecrets(secret)
      .then(({ secrets }) => {
        if (cancelled) return;
        for (const [id, value] of Object.entries(secrets)) {
          if (!getStoredApiKeySecret(id)) {
            storeApiKeySecret(id, value);
          }
        }
        const activeId = selectedKeyId || defaultKeyId;
        const resolved = activeId ? getStoredApiKeySecret(activeId) ?? "" : "";
        if (resolved) setApiKey(resolved);
      })
      .catch(() => {
        /* secrets endpoint may be unavailable during startup */
      });
    return () => {
      cancelled = true;
    };
  }, [secret, selectedKeyId, defaultKeyId]);

  // ---- Model and auto-apply options ----
  const [model, setModel] = useState(() => models[0]?.value ?? "");
  // On a direct visit the snapshot (and so the model list) can arrive after mount.
  const firstModel = models[0]?.value ?? "";
  useEffect(() => {
    if (!model && firstModel) setModel(firstModel);
  }, [model, firstModel]);
  const [options, setOptions] = useState<ApplyOptionsState>(EMPTY_APPLY_OPTIONS);
  const activePlugins = status?.active_plugins;
  useEffect(() => {
    if (activePlugins) setOptions((prev) => ({ ...prev, activePlugins }));
  }, [activePlugins]);

  // ---- Apply ----
  const [applying, setApplying] = useState(false);
  const [message, setMessage] = useState<ApplyMessage | null>(null);
  const [manualOpen, setManualOpen] = useState(false);
  const isCowork = tool.id === "cowork";
  const baseUrl = gateway.baseUrl;

  // The primary model always leads; extras are registered alongside it.
  const allModels = useMemo(
    () => [model, ...options.extraModels].filter((value, index, list) => value && list.indexOf(value) === index),
    [model, options.extraModels],
  );

  // Cowork wants full MCP entries, not names; resolve the picker's selection back
  // against the catalog the status endpoint returned.
  const selectedCoworkPlugins = useMemo(
    () => (status?.plugins ?? []).filter((plugin) => (options.activePlugins ?? []).includes(plugin.name)),
    [status?.plugins, options.activePlugins],
  );

  const handleApply = async () => {
    setApplying(true);
    setMessage(null);
    try {
      await applyCLIToolConfig(secret, tool.id, {
        baseUrl,
        apiKey: resolvedApiKey.trim(),
        model,
        models: allModels.length > 1 ? allModels : undefined,
        subagentModel: options.subagentModel || undefined,
        env: Object.keys(options.tierOverrides).length > 0 ? options.tierOverrides : undefined,
        plugins: isCowork && options.activePlugins ? selectedCoworkPlugins : undefined,
      });
      setMessage({
        type: "success",
        text: isCowork ? t("cliTools.coworkRestart") : t("cliTools.configApplied"),
      });
      await onApplied();
    } catch (error) {
      setMessage({ type: "error", text: String(error) });
    } finally {
      setApplying(false);
    }
  };

  // ---- Generated output ----
  const manualConfigs = useMemo(() => buildManualConfigs(tool, baseUrl, resolvedApiKey, model), [tool, baseUrl, resolvedApiKey, model]);
  const commandPreview = useMemo(
    () => buildGuideCommandPreview(tool, baseUrl, resolvedApiKey, model),
    [tool, baseUrl, resolvedApiKey, model],
  );
  const defaultCommand = (tool.defaultCommand ?? "")
    .replace(/\{\{baseUrl\}\}/g, baseUrl)
    .replace(/\{\{apiKey\}\}/g, resolvedApiKey)
    .replace(/\{\{model\}\}/g, model || "virtual-model-id");
  const commandSteps = (tool.guideSteps ?? []).filter((step) => stepColumn(step) === "commands");
  const ready = Boolean(model) && Boolean(resolvedApiKey.trim());

  // Cursor-style tools call tproxy from their own cloud, so localhost is useless to them.
  const configuredPublic = gateway.settings.publicBaseUrl || gateway.publicUrlOverride;
  const blocked =
    Boolean(tool.requiresExternalUrl) &&
    Boolean(tool.guideSteps?.length) &&
    !(gateway.kind === "tunnel" && buildCliTunnelBaseUrl(configuredPublic)) &&
    needsPublicBaseUrlForCliTools(configuredPublic);

  const blockedReason = !resolvedApiKey.trim()
    ? t("cliTools.apply.needKey")
    : !model
      ? t("cliTools.apply.needModel")
      : "";

  const publicUrlField = tool.requiresExternalUrl ? (
    <div className="ct-public-url">
      <label className="ct-copy-label" htmlFor={`ct-public-${tool.id}`}>
        {t("cliTools.publicBaseUrl")}
      </label>
      <Input
        id={`ct-public-${tool.id}`}
        value={gateway.publicUrlOverride}
        placeholder="https://your-tunnel.example.com"
        onChange={(event) => gateway.setPublicUrlOverride(event.target.value)}
      />
      <p className="ct-hint">
        {t("cliTools.publicUrl.desc", { name: tool.name })} <Link to="/settings">{t("cliTools.publicUrl.settingsLink")}</Link>
      </p>
    </div>
  ) : null;

  const keyDone = Boolean(resolvedApiKey.trim());
  const modelDone = Boolean(model);
  const applyDone = configState === "connected";
  const doneCount = [keyDone, Boolean(baseUrl), modelDone, autoApply ? applyDone : ready].filter(Boolean).length;

  const steps = blocked ? (
    <SetupStep index={1} title={t("cliTools.publicBaseUrl")} description={t("cliTools.publicBaseUrlDesc")}>
      {publicUrlField}
      <Note tone="warning">
        <p>{t("cliTools.setPublicUrlWarning")}</p>
      </Note>
    </SetupStep>
  ) : (
    <>
      <SetupStep
        index={1}
        done={keyDone}
        title={t("cliTools.steps.key")}
        description={t("cliTools.steps.keyDesc", { name: tool.name })}
        aside={
          <Link to="/apis" className="ct-link">
            {t("cliTools.steps.manageKeys")}
          </Link>
        }
      >
        <ApiKeySelect apiKeys={apiKeys} value={apiKey} onChange={setApiKey} onSelectedIdChange={setSelectedKeyId} />
      </SetupStep>

      <SetupStep
        index={2}
        done={Boolean(baseUrl)}
        title={t("cliTools.steps.baseUrl")}
        description={t("cliTools.steps.baseUrlDesc", { name: tool.name })}
      >
        {publicUrlField}
        <CliBaseUrlPicker
          kind={gateway.kind}
          baseUrl={baseUrl}
          lanUrl={gateway.lanUrl}
          tunnelUrl={gateway.tunnelUrl}
          tailscaleUrl={gateway.tailscaleUrl}
          allowLan={gateway.settings.allowLan}
          lanIPs={gateway.settings.lanIPs}
          selectedLanIP={gateway.effectiveLanIP}
          onKindChange={gateway.setKind}
          onLanIPChange={gateway.setLanIP}
        />
      </SetupStep>

      <SetupStep index={3} done={modelDone} title={t("cliTools.steps.model")} description={t("cliTools.steps.modelDesc")}>
        <Select value={model} onChange={(event) => setModel(event.target.value)} aria-label={t("cliTools.virtualModel")}>
          {models.length === 0 ? (
            <option value="">{t("cliTools.noModelsConfigured")}</option>
          ) : (
            models.map((item) => (
              <option key={item.value} value={item.value}>
                {item.label}
              </option>
            ))
          )}
        </Select>
        {models.length === 0 ? (
          <p className="ct-hint">
            {t("cliTools.steps.noModels")} <Link to="/models">{t("cliTools.steps.addModel")}</Link> ·{" "}
            <Link to="/combos">{t("cliTools.steps.addCombo")}</Link>
          </p>
        ) : null}
        {canAutoApply && hasApplyOptions(tool, models, status) ? (
          <ApplyOptions
            tool={tool}
            secret={secret}
            model={model}
            modelOptions={models}
            status={status}
            value={options}
            onChange={setOptions}
          />
        ) : null}
      </SetupStep>

      {autoApply ? (
        <SetupStep
          index={4}
          done={applyDone}
          title={canAutoApply ? t("cliTools.steps.apply", { name: tool.name }) : t("cliTools.steps.copy", { name: tool.name })}
          description={canAutoApply ? t("cliTools.steps.applyDesc") : t("cliTools.steps.copyDesc")}
        >
          <CLIToolApplyPanel
            tool={tool}
            status={status}
            statusLoading={statusLoading}
            configState={configState}
            configPath={configPath}
            canAutoApply={canAutoApply}
            applying={applying}
            blockedReason={blockedReason}
            message={message}
            hasManualConfig={manualConfigs.length > 0}
            onApply={() => void handleApply()}
            onOpenManual={() => setManualOpen(true)}
          />
        </SetupStep>
      ) : (
        <SetupStep
          index={4}
          done={ready}
          title={t("cliTools.steps.paste", { name: tool.name })}
          description={t("cliTools.steps.pasteDesc", { name: tool.name })}
        >
          <div className="ct-paste">
            <CopyField label={t("cliTools.baseUrl")} value={baseUrl} />
            <CopyField label={t("cliTools.apiKey")} value={resolvedApiKey} display={maskSecret(resolvedApiKey)} placeholder={t("cliTools.steps.noKeyYet")} />
            <CopyField label={t("cliTools.model")} value={model} placeholder={t("cliTools.steps.noModelYet")} />
          </div>
          {manualConfigs.length > 0 ? (
            <div className="ct-apply-actions">
              <Button variant="secondary" size="sm" icon="content_copy" onClick={() => setManualOpen(true)}>
                {t("cliTools.manualConfig")}
              </Button>
            </div>
          ) : null}
        </SetupStep>
      )}
    </>
  );

  return (
    <>
      <ToolNotes tool={tool} includeCloudCheck={blocked} />
      <div className="ct-setup">
        <section className="cp-panel ct-setup-main" aria-labelledby={`ct-setup-${tool.id}`}>
          <header className="ct-panel-head">
            <h3 id={`ct-setup-${tool.id}`}>{t("cliTools.steps.title")}</h3>
            {!blocked ? <span className="ct-progress">{t("cliTools.steps.progress", { done: doneCount, total: 4 })}</span> : null}
          </header>
          <ol className="ct-steps">{steps}</ol>
        </section>
        <section className="cp-panel ct-setup-side" aria-label={t("cliTools.output.title")}>
          <header className="ct-panel-head">
            <h3>{t("cliTools.output.title")}</h3>
          </header>
          <ToolCommandsPanel
            tool={tool}
            steps={commandSteps}
            commandPreview={commandPreview}
            manualConfigs={manualConfigs}
            defaultCommand={defaultCommand}
            ready={ready}
          />
        </section>
      </div>
      <ManualConfigModal
        open={manualOpen}
        onClose={() => setManualOpen(false)}
        title={t("cliTools.manualConfigTitle", { name: tool.name })}
        configs={manualConfigs}
      />
    </>
  );
}
