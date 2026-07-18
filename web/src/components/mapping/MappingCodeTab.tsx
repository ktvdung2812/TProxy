import { useMemo, useState } from "react";
import { ApiKeySelect, type ApiKeyOption } from "../cli-tools/ApiKeySelect";
import { gatewayBaseUrl } from "../apis/utils";
import { Button, Card, Field, Input, Select } from "../ui";
import type { ClaudeMappingResponse } from "./api";
import {
  buildBashExports,
  buildClaudeSettings,
  buildDockerEnv,
  buildPowerShellExports,
  buildServerEnv,
  DEFAULT_CLAUDE_PRIMARY_MODEL,
  MAPPING_TIERS,
  type MappingTier,
  resolveTierTargets,
} from "./codegen";

type Props = {
  apiKeys: ApiKeyOption[];
  overrides: Record<string, string>;
  data: ClaudeMappingResponse | null;
};

type CodeFormat = "server-env" | "claude-settings" | "shell" | "docker";

const FORMATS: { id: CodeFormat; label: string; hint: string }[] = [
  {
    id: "claude-settings",
    label: "Claude Code",
    hint: "Paste into ~/.claude/settings.json. Client keeps placeholder tier names (fable, sonnet, …); tproxy maps them on the server.",
  },
  {
    id: "server-env",
    label: "Server env",
    hint: "Set on the tproxy process only — real upstream targets (virtual models, codex:gpt-5.4, …).",
  },
  {
    id: "shell",
    label: "Shell exports",
    hint: "One-shot Claude Code env exports for this machine.",
  },
  {
    id: "docker",
    label: "Docker Compose",
    hint: "environment: block for a tproxy container (server-side mapping).",
  },
];

const PRIMARY_MODEL_OPTIONS = MAPPING_TIERS.map((tier) => ({ value: tier, label: tier }));

export function MappingCodeTab({ apiKeys, overrides, data }: Props) {
  const [baseUrl, setBaseUrl] = useState(() => gatewayBaseUrl());
  const [apiKey, setApiKey] = useState("");
  const [primaryModel, setPrimaryModel] = useState<MappingTier>(DEFAULT_CLAUDE_PRIMARY_MODEL);
  const [format, setFormat] = useState<CodeFormat>("claude-settings");
  const [shell, setShell] = useState<"bash" | "powershell">("bash");
  const [copied, setCopied] = useState(false);

  const serverTargets = useMemo(() => resolveTierTargets(overrides, data), [overrides, data]);

  const { content, filename, language } = useMemo(() => {
    switch (format) {
      case "server-env":
        return {
          content: buildServerEnv(serverTargets),
          filename: "tproxy.env",
          language: "env",
        };
      case "claude-settings":
        return {
          content: buildClaudeSettings(baseUrl, apiKey, primaryModel),
          filename: "~/.claude/settings.json",
          language: "json",
        };
      case "shell":
        return shell === "bash"
          ? {
              content: buildBashExports(baseUrl, apiKey, primaryModel),
              filename: "claude-code.sh",
              language: "bash",
            }
          : {
              content: buildPowerShellExports(baseUrl, apiKey, primaryModel),
              filename: "claude-code.ps1",
              language: "powershell",
            };
      case "docker":
        return {
          content: buildDockerEnv(serverTargets),
          filename: "docker-compose snippet",
          language: "yaml",
        };
      default: {
        const neverFormat: never = format;
        return { content: neverFormat, filename: "", language: "" };
      }
    }
  }, [format, shell, baseUrl, apiKey, primaryModel, serverTargets]);

  const activeFormat = FORMATS.find((item) => item.id === format);

  const copyContent = async () => {
    try {
      await navigator.clipboard.writeText(content);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      /* clipboard may be unavailable */
    }
  };

  return (
    <div className="mapping-code-tab">
      <Card pad="md" className="mapping-card mapping-card-wide">
        <div className="mapping-card-head">
          <span className="material-symbols-outlined">code</span>
          <div>
            <strong>Generate configuration</strong>
            <p>
              Claude Code uses placeholder tier names in <code>settings.json</code>. Configure real upstream targets on
              the Mapping tab or via server env — tproxy rewrites <code>fable</code> / <code>sonnet</code> on{" "}
              <code>/v1/messages</code>.
            </p>
          </div>
        </div>

        <div className="mapping-code-form">
          <Field label="Gateway base URL" hint="Anthropic-compatible endpoint, usually your tproxy origin with /v1.">
            <Input
              value={baseUrl}
              onChange={(event) => setBaseUrl(event.target.value)}
              placeholder="http://localhost:28121/v1"
            />
          </Field>
          <ApiKeySelect
            apiKeys={apiKeys}
            value={apiKey}
            onChange={setApiKey}
            emptyMessage="Create an API key on the APIs page to generate client config."
            missingSecretMessage="Reveal or paste the key secret in APIs — it is stored locally in this browser."
          />
          {(format === "claude-settings" || format === "shell") && (
            <Field
              label="Primary model"
              hint="ANTHROPIC_MODEL and CLAUDE_CODE_SUBAGENT_MODEL — still a placeholder name, not the upstream target."
            >
              <Select value={primaryModel} onChange={(event) => setPrimaryModel(event.target.value as MappingTier)}>
                {PRIMARY_MODEL_OPTIONS.map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </Select>
            </Field>
          )}
        </div>

        <div className="mapping-code-targets">
          <span className="mapping-code-targets-label">Server-side tier targets (Mapping tab)</span>
          <div className="mapping-code-targets-grid">
            {MAPPING_TIERS.map((tier) => (
              <div key={tier} className="mapping-code-target-chip">
                <code>{tier}</code>
                <span className="mapping-placeholder-arrow">→</span>
                <code>{serverTargets[tier] || "—"}</code>
              </div>
            ))}
          </div>
        </div>
      </Card>

      <Card pad="md" className="mapping-card mapping-card-wide">
        <div className="mapping-code-toolbar">
          <div className="usage-segmented">
            {FORMATS.map((item) => (
              <button
                key={item.id}
                type="button"
                className={format === item.id ? "active" : ""}
                onClick={() => setFormat(item.id)}
              >
                {item.label}
              </button>
            ))}
          </div>
          {format === "shell" ? (
            <div className="usage-segmented">
              <button
                type="button"
                className={shell === "bash" ? "active" : ""}
                onClick={() => setShell("bash")}
              >
                Bash
              </button>
              <button
                type="button"
                className={shell === "powershell" ? "active" : ""}
                onClick={() => setShell("powershell")}
              >
                PowerShell
              </button>
            </div>
          ) : null}
        </div>

        {activeFormat ? <p className="mapping-code-format-hint">{activeFormat.hint}</p> : null}

        <div className="cli-tool-codeblock">
          <div className="cli-tool-codeblock-head">
            <span>
              {filename} · {language}
            </span>
            <Button variant="ghost" size="sm" icon={copied ? "check" : "content_copy"} onClick={() => void copyContent()}>
              {copied ? "Copied" : "Copy"}
            </Button>
          </div>
          <pre>
            <code>{content}</code>
          </pre>
        </div>
      </Card>
    </div>
  );
}
