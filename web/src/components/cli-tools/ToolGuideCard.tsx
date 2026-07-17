import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { Badge, Button, Card, Field, Input, Select } from "../ui";
import type { CLITool } from "../../cli-tools/constants";

type ModelOption = {
  value: string;
  label: string;
  group: string;
};

type Props = {
  tool: CLITool;
  models: ModelOption[];
  apiKeyNames: string[];
};

function normalizeBaseUrl(origin: string): string {
  const trimmed = origin.replace(/\/+$/, "");
  return trimmed.endsWith("/v1") ? trimmed : `${trimmed}/v1`;
}

export function ToolGuideCard({ tool, models, apiKeyNames }: Props) {
  const [apiKey, setApiKey] = useState("");
  const [model, setModel] = useState(() => models[0]?.value ?? "");
  const [copiedField, setCopiedField] = useState<string | null>(null);

  const baseUrl = useMemo(() => {
    if (typeof window === "undefined") return "http://localhost:8080/v1";
    return normalizeBaseUrl(window.location.origin);
  }, []);

  const replaceVars = (text: string) =>
    text
      .replace(/\{\{baseUrl\}\}/g, baseUrl)
      .replace(/\{\{apiKey\}\}/g, apiKey.trim() || "your-api-key")
      .replace(/\{\{model\}\}/g, model || "virtual-model-id");

  const copyText = async (text: string, field: string) => {
    try {
      await navigator.clipboard.writeText(replaceVars(text));
      setCopiedField(field);
      window.setTimeout(() => setCopiedField(null), 2000);
    } catch {
      /* clipboard may be unavailable */
    }
  };

  const canShowGuide = () => {
    if (tool.requiresExternalUrl && typeof window !== "undefined") {
      const host = window.location.hostname;
      if (host === "localhost" || host === "127.0.0.1") return false;
    }
    return true;
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
    <div className="cli-tool-field-stack">
      <Input
        value={apiKey}
        onChange={(event) => setApiKey(event.target.value)}
        placeholder="Paste your tproxy API key"
      />
      {apiKeyNames.length > 0 ? (
        <p className="cli-tool-hint">
          Existing keys: {apiKeyNames.join(", ")}. Create or copy a key in{" "}
          <Link to="/apis">APIs</Link>.
        </p>
      ) : (
        <p className="cli-tool-hint">
          No API keys yet. Create one in <Link to="/apis">APIs</Link>.
        </p>
      )}
    </div>
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
          Add a <Link to="/models">virtual model</Link> or <Link to="/combos">combo</Link> first.
        </p>
      ) : null}
    </div>
  );

  const renderGuideSteps = () => {
    if (tool.configType === "mitm") {
      return (
        <div className="cli-tool-steps">
          {renderNotes()}
          {renderMitmMappings()}
          {renderModelSelector()}
        </div>
      );
    }

    if (!tool.guideSteps?.length) {
      return (
        <div className="cli-tool-empty-guide">
          <p>Manual setup: point this tool at tproxy&apos;s OpenAI-compatible endpoint.</p>
          <div className="cli-tool-kv">
            <code>{baseUrl}</code>
            <Button variant="outline" size="sm" icon={copiedField === "base" ? "check" : "content_copy"} onClick={() => void copyText(baseUrl, "base")}>
              Copy
            </Button>
          </div>
        </div>
      );
    }

    if (!canShowGuide()) {
      return (
        <div className="cli-tool-steps">
          {renderNotes(true)}
          <div className="cli-tool-note cli-tool-note-warning">
            <span className="material-symbols-outlined">warning</span>
            <p>This tool needs a publicly reachable tproxy URL. Deploy tproxy behind a tunnel or public host, then reopen this page.</p>
          </div>
        </div>
      );
    }

    return (
      <div className="cli-tool-steps">
        {renderNotes()}
        {tool.guideSteps.map((item) => (
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
                      icon={copiedField === `step-${item.step}` ? "check" : "content_copy"}
                      onClick={() => void copyText(item.value ?? "", `step-${item.step}`)}
                    >
                      Copy
                    </Button>
                  ) : null}
                </div>
              ) : null}
            </div>
          </div>
        ))}
        {tool.codeBlock ? (
          <div className="cli-tool-codeblock">
            <div className="cli-tool-codeblock-head">
              <span>{tool.codeBlock.language}</span>
              <Button
                variant="ghost"
                size="sm"
                icon={copiedField === "codeblock" ? "check" : "content_copy"}
                onClick={() => void copyText(tool.codeBlock?.code ?? "", "codeblock")}
              >
                {copiedField === "codeblock" ? "Copied" : "Copy"}
              </Button>
            </div>
            <pre>
              <code>{replaceVars(tool.codeBlock.code)}</code>
            </pre>
          </div>
        ) : null}
      </div>
    );
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
