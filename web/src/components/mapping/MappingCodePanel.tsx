import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { ApiKeySelect, type ApiKeyOption } from "../cli-tools/ApiKeySelect";
import { gatewayBaseUrl } from "../apis/utils";
import { Field, Input, Select } from "../ui";
import { SectionHead, Segmented, ToolButton } from "../page-kit";
import type { ClaudeMappingResponse } from "./api";
import {
  buildBashExports,
  buildClaudeSettings,
  buildCodexAuth,
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
type CodexFormat = "codex-config" | "codex-auth";

const PRIMARY_MODEL_OPTIONS = MAPPING_TIERS.filter((tier) => tier !== "default").map((tier) => ({
  value: tier,
  label: tier,
}));
const GPT_MODEL_OPTIONS = (Object.keys(GPT_CLIENT_TIER_PLACEHOLDERS) as GptCodenameTier[]).map((tier) => ({
  value: tier,
  label: GPT_CLIENT_TIER_PLACEHOLDERS[tier] || tier,
}));

/** Client config generator: keeps placeholder names on the client so tproxy can retarget them server-side. */
export function MappingCodePanel({ client, apiKeys, overrides, data }: Props) {
  const { t } = useTranslation();
  const [baseUrl, setBaseUrl] = useState(() => gatewayBaseUrl());
  const [apiKey, setApiKey] = useState("");
  const [primaryModel, setPrimaryModel] = useState<MappingTier>(DEFAULT_CLAUDE_PRIMARY_MODEL);
  const [gptModel, setGptModel] = useState<GptCodenameTier>("fable");
  const [claudeFormat, setClaudeFormat] = useState<ClaudeFormat>("claude-settings");
  const [codexFormat, setCodexFormat] = useState<CodexFormat>("codex-config");
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
        hint: t("mapping.cursorSetupHint"),
      };
    }

    if (client === "codex") {
      return codexFormat === "codex-auth"
        ? { content: buildCodexAuth(apiKey), filename: "~/.codex/auth.json", language: "json", hint: t("mapping.codexAuthFormatHint") }
        : { content: buildCodexConfig(baseUrl, gptModel), filename: "~/.codex/config.toml", language: "toml", hint: t("mapping.codexSettingsFormatHint") };
    }

    if (claudeFormat === "claude-settings") {
      return {
        content: buildClaudeSettings(baseUrl, apiKey, primaryModel),
        filename: "~/.claude/settings.json",
        language: "json",
        hint: t("mapping.claudeSettingsFormatHint"),
      };
    }

    return shell === "bash"
      ? { content: buildBashExports(baseUrl, apiKey, primaryModel), filename: "claude-code.sh", language: "bash", hint: t("mapping.shellFormatHint") }
      : { content: buildPowerShellExports(baseUrl, apiKey, primaryModel), filename: "claude-code.ps1", language: "powershell", hint: t("mapping.shellFormatHint") };
  }, [client, claudeFormat, codexFormat, shell, baseUrl, apiKey, primaryModel, gptModel, overrides, t]);

  const copyContent = async () => {
    try {
      await navigator.clipboard.writeText(content);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      /* clipboard may be unavailable */
    }
  };

  const title = t(`mapping.codePanelTitle_${client}`);
  const description = client === "claude" ? t("mapping.claudeCodeDesc") : client === "codex" ? t("mapping.codexCodeDesc") : t("mapping.cursorCodeDesc");
  const targets =
    client === "cursor"
      ? cursorTargets.map((row) => ({ key: row.id, source: row.id, target: row.target }))
      : visibleTiers.map((tier) => ({
          key: tier,
          source: client === "codex" ? GPT_CLIENT_TIER_PLACEHOLDERS[tier as GptCodenameTier] || tier : tier,
          target: serverTargets[tier] || "",
        }));

  return (
    <section className="cp-panel mp-panel mp-code">
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>code</span>}
        title={t("mapping.generateCode", { title })}
        subtitle={description}
      />
      <div className="mp-code-body">
        <div className="mp-code-form">
          <Field
            label={t("mapping.gatewayBaseUrl")}
            hint={client === "claude" ? t("mapping.claudeGatewayHint") : client === "cursor" ? t("mapping.cursorGatewayHint") : t("mapping.codexGatewayHint")}
          >
            <Input value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} placeholder="http://localhost:28120/v1" />
          </Field>
          <ApiKeySelect
            apiKeys={apiKeys}
            value={apiKey}
            onChange={setApiKey}
            emptyMessage={t("mapping.createApiKeyHint")}
            missingSecretMessage={t("mapping.revealSecretHint")}
          />
          {client === "claude" ? (
            <Field label={t("mapping.primaryModel")} hint={t("mapping.primaryModelClaudeHint")}>
              <Select value={primaryModel} onChange={(event) => setPrimaryModel(event.target.value as MappingTier)}>
                {PRIMARY_MODEL_OPTIONS.map((option) => (
                  <option key={option.value} value={option.value}>{option.label}</option>
                ))}
              </Select>
            </Field>
          ) : client === "codex" ? (
            <Field label={t("mapping.primaryModel")} hint={t("mapping.primaryModelCodexHint")}>
              <Select value={gptModel} onChange={(event) => setGptModel(event.target.value as GptCodenameTier)}>
                {GPT_MODEL_OPTIONS.map((option) => (
                  <option key={option.value} value={option.value}>{option.label}</option>
                ))}
              </Select>
            </Field>
          ) : null}
        </div>

        <div className="mp-code-targets">
          <span className="mp-kicker">{client === "cursor" ? t("mapping.cursorTargetsLabel") : t("mapping.serverTierTargetsLabel")}</span>
          {targets.length === 0 ? (
            <span className="mp-dim">{t("mapping.noCursorTargets")}</span>
          ) : (
            <div className="mp-chips">
              {targets.map((row) => (
                <span key={row.key} className="mp-chip" title={`${row.source} → ${row.target || "—"}`}>
                  <code>{row.source}</code>
                  <span className="material-symbols-outlined" aria-hidden>arrow_forward</span>
                  <code className="mp-chip-target">{row.target || "—"}</code>
                </span>
              ))}
            </div>
          )}
        </div>

        <div className="mp-code-block">
          <div className="mp-code-head">
            {client === "claude" ? (
              <>
                <Segmented
                  label={t("mapping.formatLabel")}
                  value={claudeFormat}
                  onChange={setClaudeFormat}
                  items={[
                    { value: "claude-settings", label: "settings.json" },
                    { value: "shell", label: t("mapping.shellExports") },
                  ]}
                />
                {claudeFormat === "shell" ? (
                  <Segmented
                    label={t("mapping.shellLabel")}
                    value={shell}
                    onChange={setShell}
                    items={[
                      { value: "bash", label: "Bash" },
                      { value: "powershell", label: "PowerShell" },
                    ]}
                  />
                ) : null}
              </>
            ) : client === "codex" ? (
              <Segmented
                label={t("mapping.formatLabel")}
                value={codexFormat}
                onChange={setCodexFormat}
                items={[
                  { value: "codex-config", label: "config.toml" },
                  { value: "codex-auth", label: "auth.json" },
                ]}
              />
            ) : null}
            <span className="mp-code-file">
              {filename} · {language}
            </span>
            <ToolButton icon={copied ? "check" : "content_copy"} tone={copied ? "success" : undefined} onClick={() => void copyContent()}>
              {copied ? t("mapping.copied") : t("mapping.copy")}
            </ToolButton>
          </div>
          <p className="mp-code-hint">{hint}</p>
          <pre className="mp-pre custom-scrollbar">
            <code>{content}</code>
          </pre>
        </div>
      </div>
    </section>
  );
}
