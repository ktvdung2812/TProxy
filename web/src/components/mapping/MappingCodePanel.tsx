import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { ApiKeySelect, type ApiKeyOption } from "../cli-tools/ApiKeySelect";
import { gatewayBaseUrl } from "../apis/utils";
import { Button, Card, Field, Input, Select } from "../ui";
import type { ClaudeMappingResponse } from "./api";
import {
  buildBashExports,
  buildClaudeSettings,
  buildCodexConfig,
  buildCursorSetupGuide,
  buildPowerShellExports,
  DEFAULT_CLAUDE_PRIMARY_MODEL,
  CODEX_MAPPING_TIERS,
  GPT_CLIENT_TIER_PLACEHOLDERS,
  MAPPING_TIERS,
  type GptCodenameTier,
  type MappingTier,
  resolveTierTargets,
} from "./codegen";

type Client = "claude" | "codex" | "cursor";

type Props = {
  client: Client;
  apiKeys: ApiKeyOption[];
  overrides: Record<string, string>;
  data: ClaudeMappingResponse | null;
};

type ClaudeFormat = "claude-settings" | "shell";
type CodexFormat = "codex-settings";

const PRIMARY_MODEL_OPTIONS = MAPPING_TIERS.filter((tier) => tier !== "default").map((tier) => ({
  value: tier,
  label: tier,
}));
const GPT_MODEL_OPTIONS = (Object.keys(GPT_CLIENT_TIER_PLACEHOLDERS) as GptCodenameTier[]).map((tier) => ({
  value: tier,
  label: GPT_CLIENT_TIER_PLACEHOLDERS[tier] || tier,
}));

const CLAUDE_FORMAT_HINTS: Record<ClaudeFormat, string> = {
  "claude-settings":
    "Paste into ~/.claude/settings.json. Client keeps placeholder tier names (fable, sonnet, …); tproxy maps them on /v1/messages.",
  shell: "One-shot Claude Code env exports for this machine.",
};

const CODEX_FORMAT_HINT: Record<CodexFormat, string> = {
  "codex-settings":
    "Paste into ~/.codex/config.toml. Client keeps GPT codenames (gpt-sol, gpt-terra, gpt-luna); tproxy maps them on /v1/chat/completions.",
};

export function MappingCodePanel({ client, apiKeys, overrides, data }: Props) {
  const { t } = useTranslation();
  const [baseUrl, setBaseUrl] = useState(() => gatewayBaseUrl());
  const [apiKey, setApiKey] = useState("");
  const [primaryModel, setPrimaryModel] = useState<MappingTier>(DEFAULT_CLAUDE_PRIMARY_MODEL);
  const [gptModel, setGptModel] = useState<GptCodenameTier>("fable");
  const [claudeFormat, setClaudeFormat] = useState<ClaudeFormat>("claude-settings");
  const [shell, setShell] = useState<"bash" | "powershell">("bash");
  const [copied, setCopied] = useState(false);

  const serverTargets = useMemo(() => resolveTierTargets(overrides, data), [overrides, data]);
  const visibleTiers = client === "claude" ? MAPPING_TIERS : client === "codex" ? CODEX_MAPPING_TIERS : [];
  const cursorTargets = useMemo(() => {
    if (client !== "cursor") return [] as Array<{ id: string; target: string }>;
    return Object.entries(overrides)
      .filter(([, target]) => target.trim())
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([id, target]) => ({ id, target: target.trim() }));
  }, [client, overrides]);

  const { content, filename, language, hint } = useMemo(() => {
    if (client === "cursor") {
      return {
        content: buildCursorSetupGuide(baseUrl, apiKey, overrides),
        filename: "cursor-setup.txt",
        language: "text",
        hint: "Use a publicly reachable base URL (tunnel). Add each custom model ID in Cursor Settings → Models.",
      };
    }

    if (client === "codex") {
      return {
        content: buildCodexConfig(baseUrl, apiKey, gptModel),
        filename: "~/.codex/config.toml",
        language: "toml",
        hint: CODEX_FORMAT_HINT["codex-settings"],
      };
    }

    if (claudeFormat === "claude-settings") {
      return {
        content: buildClaudeSettings(baseUrl, apiKey, primaryModel),
        filename: "~/.claude/settings.json",
        language: "json",
        hint: CLAUDE_FORMAT_HINTS["claude-settings"],
      };
    }

    return shell === "bash"
      ? {
          content: buildBashExports(baseUrl, apiKey, primaryModel),
          filename: "claude-code.sh",
          language: "bash",
          hint: CLAUDE_FORMAT_HINTS.shell,
        }
      : {
          content: buildPowerShellExports(baseUrl, apiKey, primaryModel),
          filename: "claude-code.ps1",
          language: "powershell",
          hint: CLAUDE_FORMAT_HINTS.shell,
        };
  }, [client, claudeFormat, shell, baseUrl, apiKey, primaryModel, gptModel, overrides]);

  const copyContent = async () => {
    try {
      await navigator.clipboard.writeText(content);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      /* clipboard may be unavailable */
    }
  };

  const title = client === "claude" ? "Claude Code" : client === "codex" ? "Codex CLI" : "Cursor IDE";
  const description =
    client === "claude"
      ? "Generate client config that keeps placeholder tier names — tproxy rewrites them on /v1/messages."
      : client === "codex"
        ? "Generate Codex config with GPT codenames like gpt-sol — tproxy rewrites them on /v1/chat/completions."
        : "Generate Cursor setup notes. Add the listed custom model IDs in Settings → Models; tproxy rewrites them on /v1/chat/completions.";

  return (
    <Card pad="md" className="mapping-card mapping-card-wide mapping-code-panel">
      <div className="mapping-card-head">
        <span className="material-symbols-outlined">code</span>
        <div>
          <strong>Generate code · {title}</strong>
          <p>{description}</p>
        </div>
      </div>

      <div className="mapping-code-form">
        <Field
          label={t("mapping.gatewayBaseUrl")}
          hint={
            client === "claude"
              ? "Anthropic-compatible endpoint, usually your tproxy origin with /v1."
              : client === "cursor"
                ? "Public OpenAI-compatible URL (tunnel / cloud). Cursor does not support localhost."
                : "OpenAI-compatible endpoint, usually your tproxy origin with /v1."
          }
        >
          <Input
            value={baseUrl}
            onChange={(event) => setBaseUrl(event.target.value)}
            placeholder="http://localhost:28120/v1"
          />
        </Field>
        <ApiKeySelect
          apiKeys={apiKeys}
          value={apiKey}
          onChange={setApiKey}
          emptyMessage="Create an API key on the APIs page to generate client config."
          missingSecretMessage="Reveal or paste the key secret in APIs — it is stored locally in this browser."
        />
        {client === "claude" ? (
          <Field
            label="Primary model"
            hint="ANTHROPIC_MODEL and CLAUDE_CODE_SUBAGENT_MODEL — placeholder name, not the upstream target."
          >
            <Select value={primaryModel} onChange={(event) => setPrimaryModel(event.target.value as MappingTier)}>
              {PRIMARY_MODEL_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </Select>
          </Field>
        ) : client === "codex" ? (
          <Field
            label="Primary model"
            hint="Codex model field — GPT codename placeholder mapped to the configured tier target."
          >
            <Select value={gptModel} onChange={(event) => setGptModel(event.target.value as GptCodenameTier)}>
              {GPT_MODEL_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </Select>
          </Field>
        ) : null}
      </div>

      {client === "cursor" ? (
        <div className="mapping-code-targets">
          <span className="mapping-code-targets-label">Cursor custom model targets</span>
          <div className="mapping-code-targets-grid">
            {cursorTargets.map((row) => (
              <div key={row.id} className="mapping-code-target-chip">
                <code>{row.id}</code>
                <span className="mapping-placeholder-arrow">→</span>
                <code>{row.target || "—"}</code>
              </div>
            ))}
          </div>
        </div>
      ) : (
        <div className="mapping-code-targets">
          <span className="mapping-code-targets-label">Server-side tier targets</span>
          <div className="mapping-code-targets-grid">
            {visibleTiers.map((tier) => (
              <div key={tier} className="mapping-code-target-chip">
                <code>{tier}</code>
                <span className="mapping-placeholder-arrow">→</span>
                <code>{serverTargets[tier] || "—"}</code>
              </div>
            ))}
          </div>
        </div>
      )}

      {client === "claude" ? (
        <div className="mapping-code-toolbar">
          <div className="usage-segmented">
            <button
              type="button"
              className={claudeFormat === "claude-settings" ? "active" : ""}
              onClick={() => setClaudeFormat("claude-settings")}
            >
              settings.json
            </button>
            <button
              type="button"
              className={claudeFormat === "shell" ? "active" : ""}
              onClick={() => setClaudeFormat("shell")}
            >
              Shell exports
            </button>
          </div>
          {claudeFormat === "shell" ? (
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
      ) : null}

      <p className="mapping-code-format-hint">{hint}</p>

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
  );
}
