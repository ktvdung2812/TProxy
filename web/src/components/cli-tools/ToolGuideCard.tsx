import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { Link } from "react-router-dom";
import { Badge, Button, Card, Field, Input, Select } from "../ui";
import type { CLITool, CLIToolGuideStep } from "../../cli-tools/constants";
import { defaultApiKey, isLocalDashboardHost } from "../../devDefaults";
import { fetchResolvableApiKeySecrets } from "../apis/api";
import { fetchAdminSettings } from "../settings/api";
import { getStoredApiKeySecret, getApiKeySecretsVersion, storeApiKeySecret, subscribeApiKeySecrets } from "../../lib/apiKeySecrets";
import {
  needsPublicBaseUrlForCliTools,
  readStoredPublicBaseUrl,
  resolveCliBaseUrl,
  storePublicBaseUrl,
} from "../../lib/publicBaseUrl";
import { ApiKeySelect, type ApiKeyOption } from "./ApiKeySelect";
import { CLIToolApplyPanel } from "./CLIToolApplyPanel";
import { CLIApplyScriptBlock } from "./CLIApplyScriptBlock";
import { buildGuideCommandPreview, buildManualConfigs } from "./manualConfigs";

function stepColumn(step: CLIToolGuideStep): "config" | "commands" {
  if (step.column) return step.column;
  if (step.type === "apiKeySelector" || step.type === "modelSelector") return "config";
  const title = step.title.toLowerCase().trim();
  if (title === "base url" || title === "api key" || title === "model" || title === "default model" || title === "select model" || title === "virtual model") {
    return "config";
  }
  return "commands";
}

function isConfigStep(step: CLIToolGuideStep): boolean {
  return stepColumn(step) === "config";
}

type ModelOption = {
  value: string;
  label: string;
  group: string;
};

type Props = {
  tool: CLITool;
  models: ModelOption[];
  apiKeys: ApiKeyOption[];
  secret: string;
};

export function ToolGuideCard({ tool, models, apiKeys, secret }: Props) {
  const enabledKeys = useMemo(() => apiKeys.filter((key) => key.enabled !== false), [apiKeys]);
  const defaultKeyId = useMemo(() => {
    if (enabledKeys.length === 0) return "";
    const firstWithSecret = enabledKeys.find((key) => getStoredApiKeySecret(key.id));
    return firstWithSecret?.id ?? enabledKeys[0].id;
  }, [enabledKeys]);
  const [apiKey, setApiKey] = useState(() => (defaultKeyId ? getStoredApiKeySecret(defaultKeyId) ?? "" : ""));
  const [selectedKeyId, setSelectedKeyId] = useState(defaultKeyId);
  const [model, setModel] = useState(() => models[0]?.value ?? "");
  const [copiedField, setCopiedField] = useState<string | null>(null);
  const [serverPublicBaseUrl, setServerPublicBaseUrl] = useState("");
  const [publicUrlOverride, setPublicUrlOverride] = useState(() => readStoredPublicBaseUrl());
  const secretsVersion = useSyncExternalStore(subscribeApiKeySecrets, getApiKeySecretsVersion, () => 0);

  const baseUrl = useMemo(
    () => resolveCliBaseUrl(serverPublicBaseUrl || publicUrlOverride),
    [serverPublicBaseUrl, publicUrlOverride],
  );

  const resolvedApiKey = useMemo(() => {
    const trimmed = apiKey.trim();
    if (trimmed) return trimmed;
    if (selectedKeyId) return getStoredApiKeySecret(selectedKeyId) ?? "";
    return "";
  }, [apiKey, selectedKeyId, secretsVersion]);

  useEffect(() => {
    if (typeof window === "undefined" || !isLocalDashboardHost()) return;
    if (!getStoredApiKeySecret("local")) {
      storeApiKeySecret("local", defaultApiKey());
    }
  }, []);

  useEffect(() => {
    if (!secret) return;
    let cancelled = false;
    void fetchAdminSettings(secret)
      .then((settings) => {
        if (!cancelled) setServerPublicBaseUrl(settings.public_base_url || "");
      })
      .catch(() => {
        /* settings endpoint may be unavailable during startup */
      });
    return () => {
      cancelled = true;
    };
  }, [secret]);

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

  const replaceVars = (text: string) =>
    text
      .replace(/\{\{baseUrl\}\}/g, baseUrl)
      .replace(/\{\{apiKey\}\}/g, resolvedApiKey)
      .replace(/\{\{model\}\}/g, model || "virtual-model-id");

  const copyText = async (text: string, field: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopiedField(field);
      window.setTimeout(() => setCopiedField(null), 2000);
    } catch {
      /* clipboard may be unavailable */
    }
  };

  const canShowGuide = () => {
    if (!tool.requiresExternalUrl) return true;
    return !needsPublicBaseUrlForCliTools(serverPublicBaseUrl || publicUrlOverride);
  };

  const renderPublicUrlField = () => {
    if (!tool.requiresExternalUrl) return null;
    return (
      <div className="cli-tool-step">
        <span className="cli-tool-step-num" style={{ backgroundColor: tool.color }}>
          ★
        </span>
        <div className="cli-tool-step-body">
          <p className="cli-tool-step-title">Public base URL</p>
          <p className="cli-tool-step-desc">
            Cursor gọi API qua server của họ, nên cần URL public trỏ về tproxy. Cấu hình tại{" "}
            <Link to="/settings">Settings → Gateway</Link> hoặc nhập tạm bên dưới.
          </p>
          <div className="cli-tool-kv">
            <Input
              value={publicUrlOverride}
              placeholder="https://your-tunnel.example.com"
              onChange={(event) => {
                const next = event.target.value;
                setPublicUrlOverride(next);
                storePublicBaseUrl(next);
              }}
            />
          </div>
          <p className="cli-tool-hint">
            Effective endpoint: <code>{baseUrl}</code>
          </p>
        </div>
      </div>
    );
  };

  const renderNotes = (includeCloudCheck = false) => {
    if (!tool.notes?.length) return null;
    return (
      <div className="cli-tool-notes">
        {tool.notes.map((note, index) => {
          if (note.type === "cloudCheck" && !includeCloudCheck) return null;
          const icon = note.type === "warning" || note.type === "cloudCheck" ? "warning" : note.type === "error" ? "error" : "info";
          return (
            <div key={index} className={`cli-tool-note cli-tool-note-${note.type === "cloudCheck" ? "warning" : note.type}`}>
              <span className="material-symbols-outlined">{icon}</span>
              <p>{note.text}</p>
            </div>
          );
        })}
      </div>
    );
  };

  const renderMitmMappings = () => {
    if (!tool.defaultModels?.length) return null;
    return (
      <div className="cli-tool-mitm-mappings">
        <p className="cli-tool-step-title">Model slots</p>
        <p className="cli-tool-step-desc">Map each intercepted model ID to a tproxy virtual model when MITM is enabled.</p>
        <div className="cli-tool-mapping-table">
          {tool.defaultModels.map((slot) => (
            <div className="cli-tool-mapping-row" key={slot.id}>
              <code>{slot.id}</code>
              <span className="material-symbols-outlined">arrow_forward</span>
              <span>{slot.name}</span>
            </div>
          ))}
        </div>
      </div>
    );
  };

  const renderApiKeySelector = () => (
    <ApiKeySelect
      apiKeys={apiKeys}
      value={apiKey}
      onChange={setApiKey}
      onSelectedIdChange={setSelectedKeyId}
    />
  );

  const renderModelSelector = () => (
    <div className="cli-tool-field-stack">
      <Field label="Virtual model">
        <Select value={model} onChange={(event) => setModel(event.target.value)}>
          {models.length === 0 ? (
            <option value="">No models configured</option>
          ) : (
            models.map((item) => (
              <option key={item.value} value={item.value}>
                {item.label}
              </option>
            ))
          )}
        </Select>
      </Field>
      {models.length === 0 ? (
        <p className="cli-tool-hint">
          Add a <Link to="/models">model</Link> or <Link to="/combos">combo</Link> first.
        </p>
      ) : null}
    </div>
  );

  const manualConfigs = useMemo(
    () => buildManualConfigs(tool, baseUrl, resolvedApiKey, model),
    [tool, baseUrl, resolvedApiKey, model],
  );

  const commandPreview = useMemo(
    () => buildGuideCommandPreview(tool, baseUrl, resolvedApiKey, model),
    [tool, baseUrl, resolvedApiKey, model],
  );

  const renderStep = (item: CLIToolGuideStep) => (
    <div key={item.step} className="cli-tool-step">
      <span className="cli-tool-step-num" style={{ backgroundColor: tool.color }}>
        {item.step}
      </span>
      <div className="cli-tool-step-body">
        <p className="cli-tool-step-title">{item.title}</p>
        {item.desc ? <p className="cli-tool-step-desc">{item.desc}</p> : null}
        {item.type === "apiKeySelector" ? renderApiKeySelector() : null}
        {item.type === "modelSelector" ? renderModelSelector() : null}
        {item.value ? (
          <div className="cli-tool-kv">
            <code>{replaceVars(item.value)}</code>
            {item.copyable ? (
              <Button
                variant="outline"
                size="sm"
                className="btn-icon-only"
                icon={copiedField === `step-${item.step}` ? "check" : "content_copy"}
                aria-label={copiedField === `step-${item.step}` ? "Copied" : "Copy"}
                title={copiedField === `step-${item.step}` ? "Copied" : "Copy"}
                onClick={() => void copyText(replaceVars(item.value ?? ""), `step-${item.step}`)}
              />
            ) : null}
          </div>
        ) : null}
      </div>
    </div>
  );

  const renderCodeBlock = () => {
    if (!commandPreview) return null;
    const language = tool.codeBlock?.language ?? "bash";
    return (
      <div className="cli-tool-codeblock">
        <div className="cli-tool-codeblock-head">
          <span>{language}</span>
          <Button
            variant="ghost"
            size="sm"
            className="btn-icon-only"
            icon={copiedField === "codeblock" ? "check" : "content_copy"}
            aria-label={copiedField === "codeblock" ? "Copied" : "Copy"}
            title={copiedField === "codeblock" ? "Copied" : "Copy"}
            onClick={() => void copyText(commandPreview, "codeblock")}
          />
        </div>
        <pre>
          <code>{commandPreview}</code>
        </pre>
      </div>
    );
  };

  const renderDefaultConfigFields = () => (
    <div className="cli-tool-steps">
      <div className="cli-tool-step">
        <span className="cli-tool-step-num" style={{ backgroundColor: tool.color }}>1</span>
        <div className="cli-tool-step-body">
          <p className="cli-tool-step-title">API Key</p>
          {renderApiKeySelector()}
        </div>
      </div>
      <div className="cli-tool-step">
        <span className="cli-tool-step-num" style={{ backgroundColor: tool.color }}>2</span>
        <div className="cli-tool-step-body">
          <p className="cli-tool-step-title">Base URL</p>
          <div className="cli-tool-kv">
            <code>{baseUrl}</code>
            <Button
              variant="outline"
              size="sm"
              className="btn-icon-only"
              icon={copiedField === "base" ? "check" : "content_copy"}
              aria-label={copiedField === "base" ? "Copied" : "Copy"}
              title={copiedField === "base" ? "Copied" : "Copy"}
              onClick={() => void copyText(baseUrl, "base")}
            />
          </div>
        </div>
      </div>
      <div className="cli-tool-step">
        <span className="cli-tool-step-num" style={{ backgroundColor: tool.color }}>3</span>
        <div className="cli-tool-step-body">
          <p className="cli-tool-step-title">Model</p>
          {renderModelSelector()}
        </div>
      </div>
    </div>
  );

  const renderApplyPanel = () => (
    <CLIToolApplyPanel
      tool={tool}
      secret={secret}
      apiKey={resolvedApiKey}
      model={model}
      baseUrl={baseUrl}
      onApiKeyChange={setApiKey}
      hideScript
    />
  );

  const renderCommandsColumn = (commandSteps: CLIToolGuideStep[]) => (
    <>
      <p className="cli-tool-col-title">Commands</p>
      {commandSteps.length > 0 ? <div className="cli-tool-steps">{commandSteps.map(renderStep)}</div> : null}
      {renderCodeBlock()}
      {manualConfigs.length > 0 ? (
        <CLIApplyScriptBlock configs={manualConfigs} disabled={!model || !resolvedApiKey.trim()} />
      ) : null}
      {tool.defaultCommand ? (
        <div className="cli-tool-kv">
          <code>{replaceVars(tool.defaultCommand)}</code>
          <Button
            variant="outline"
            size="sm"
            className="btn-icon-only"
            icon={copiedField === "default-cmd" ? "check" : "content_copy"}
            aria-label={copiedField === "default-cmd" ? "Copied" : "Copy"}
            title={copiedField === "default-cmd" ? "Copied" : "Copy"}
            onClick={() => void copyText(replaceVars(tool.defaultCommand ?? ""), "default-cmd")}
          />
        </div>
      ) : null}
    </>
  );

  const renderSplitGuide = () => {
    const steps = tool.guideSteps ?? [];
    const configSteps = steps.filter(isConfigStep);
    const commandSteps = steps.filter((step) => !isConfigStep(step));

    return (
      <div className="cli-tool-guide-columns">
        <div className="cli-tool-guide-col cli-tool-guide-col-config">
          <p className="cli-tool-col-title">Configuration</p>
          {renderNotes()}
          {renderPublicUrlField()}
          <div className="cli-tool-steps">{configSteps.map(renderStep)}</div>
          {renderApplyPanel()}
        </div>
        <div className="cli-tool-guide-col cli-tool-guide-col-commands">
          {renderCommandsColumn(commandSteps)}
        </div>
      </div>
    );
  };

  const renderGuideSteps = () => {
    if (tool.configType === "mitm") {
      return (
        <div className="cli-tool-guide-columns">
          <div className="cli-tool-guide-col cli-tool-guide-col-config">
            <p className="cli-tool-col-title">Configuration</p>
            {renderNotes()}
            {renderMitmMappings()}
            {renderModelSelector()}
          </div>
          <div className="cli-tool-guide-col cli-tool-guide-col-commands">
            <p className="cli-tool-col-title">Commands</p>
            {tool.mitmDomain ? (
              <div className="cli-tool-kv">
                <code>{tool.mitmDomain}</code>
                <Button
                  variant="outline"
                  size="sm"
                  className="btn-icon-only"
                  icon={copiedField === "mitm-domain" ? "check" : "content_copy"}
                  aria-label={copiedField === "mitm-domain" ? "Copied" : "Copy domain"}
                  title={copiedField === "mitm-domain" ? "Copied" : "Copy domain"}
                  onClick={() => void copyText(tool.mitmDomain ?? "", "mitm-domain")}
                />
              </div>
            ) : null}
            <p className="cli-tool-hint">Enable MITM in your proxy tool and route traffic through the configured domain.</p>
          </div>
        </div>
      );
    }

    if (!tool.guideSteps?.length) {
      return (
        <div className="cli-tool-guide-columns">
          <div className="cli-tool-guide-col cli-tool-guide-col-config">
            <p className="cli-tool-col-title">Configuration</p>
            {renderNotes()}
            {renderDefaultConfigFields()}
            {renderApplyPanel()}
          </div>
          <div className="cli-tool-guide-col cli-tool-guide-col-commands">
            {renderCommandsColumn([])}
          </div>
        </div>
      );
    }

    if (!canShowGuide()) {
      return (
        <div className="cli-tool-guide-columns">
          <div className="cli-tool-guide-col cli-tool-guide-col-config">
            <p className="cli-tool-col-title">Configuration</p>
            {renderNotes(true)}
            <div className="cli-tool-note cli-tool-note-warning">
              <span className="material-symbols-outlined">warning</span>
              <p>
                Set a public base URL in <Link to="/settings">Settings → Gateway</Link> or enter one above. Cursor cannot reach localhost.
              </p>
            </div>
          </div>
          <div className="cli-tool-guide-col cli-tool-guide-col-commands">
            {renderCommandsColumn(tool.guideSteps.filter((step) => !isConfigStep(step)))}
          </div>
        </div>
      );
    }

    return renderSplitGuide();
  };

  return (
    <Card pad="md" className="cli-tool-guide">
      <div className="cli-tool-guide-head">
        <span className="cli-tool-icon cli-tool-icon-lg" style={{ color: tool.color }}>
          <span className="material-symbols-outlined">{tool.icon}</span>
        </span>
        <div>
          <h3>{tool.name}</h3>
          <p>{tool.description}</p>
        </div>
        <Badge size="sm">{tool.configType}</Badge>
      </div>
      <div className="cli-tool-guide-body">{renderGuideSteps()}</div>
      {tool.settingsFile ? (
        <p className="cli-tool-hint">
          Config file: <code>{tool.settingsFile}</code>
        </p>
      ) : null}
      {tool.docsUrl ? (
        <p className="cli-tool-hint">
          Docs:{" "}
          <a href={tool.docsUrl} target="_blank" rel="noreferrer">
            {tool.docsUrl}
          </a>
        </p>
      ) : null}
    </Card>
  );
}

export function buildModelOptions(
  models: { ID: string; DisplayName?: string; Enabled?: boolean }[],
  combos: { id: string; display_name?: string; enabled?: boolean }[],
  toolDefaults?: { id: string; name: string; alias: string }[],
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
