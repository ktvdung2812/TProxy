import { useCallback, useEffect, useMemo, useState } from "react";
import type { ApiKeyOption } from "../cli-tools/ApiKeySelect";
import { Badge, Button, Card, Field, Input, cn } from "../ui";
import { fetchClaudeMapping, saveClaudeMapping, type ClaudeMappingResponse } from "./api";
import { MappingCodeTab } from "./MappingCodeTab";

type Props = {
  secret: string;
  apiKeys: ApiKeyOption[];
  models: Array<{ ID: string; DisplayName?: string }>;
  providers: Array<{ ID: string; Type: string; Name: string }>;
  onError: (message: string) => void;
  onNotice: (message: string) => void;
};

type MappingTab = "mapping" | "code";

const TIERS = [
  { id: "fable", label: "Claude Fable", hint: "claude-fable, fable" },
  { id: "opus", label: "Claude Opus", hint: "claude-opus, opus, opusplan" },
  { id: "sonnet", label: "Claude Sonnet", hint: "claude-sonnet, sonnet, default" },
  { id: "haiku", label: "Claude Haiku", hint: "claude-haiku, haiku" },
] as const;

export function MappingView({ secret, apiKeys, models, providers, onError, onNotice }: Props) {
  const [activeTab, setActiveTab] = useState<MappingTab>("mapping");
  const [data, setData] = useState<ClaudeMappingResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [overrides, setOverrides] = useState<Record<string, string>>({
    fable: "",
    opus: "",
    sonnet: "",
    haiku: "",
  });
  const [defaultCodexProvider, setDefaultCodexProvider] = useState("codex");

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const response = await fetchClaudeMapping(secret);
      setData(response);
      setOverrides({
        fable: response.overrides?.fable || "",
        opus: response.overrides?.opus || "",
        sonnet: response.overrides?.sonnet || "",
        haiku: response.overrides?.haiku || "",
      });
      setDefaultCodexProvider(response.default_codex_provider || "codex");
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "Failed to load mapping");
    } finally {
      setLoading(false);
    }
  }, [secret, onError]);

  useEffect(() => {
    void load();
  }, [load]);

  const modelOptions = useMemo(
    () => models.map((model) => ({ value: model.ID, label: model.DisplayName || model.ID })),
    [models],
  );

  const codexProviders = useMemo(
    () => providers.filter((provider) => provider.Type === "codex").map((provider) => provider.ID),
    [providers],
  );

  const handleSave = async () => {
    setSaving(true);
    try {
      const response = await saveClaudeMapping(secret, {
        overrides,
        default_codex_provider: defaultCodexProvider,
      });
      setData(response);
      onNotice("Claude routing map saved");
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "Failed to save mapping");
    } finally {
      setSaving(false);
    }
  };

  if (loading && !data) {
    return (
      <section className="section mapping-page">
        <div className="mapping-loading">
          <span className="material-symbols-outlined animate-spin">progress_activity</span>
          Loading mapping…
        </div>
      </section>
    );
  }

  return (
    <section className="section mapping-page">
      <div className="section-head">
        <div>
          <p className="eyebrow">Protocol mapping</p>
          <h2>Claude transparent routing</h2>
          <p>
            Route Claude Code / Cowork token flows to any upstream model without changing model names in the
            client. Placeholders like <code>sonnet</code> or <code>fable</code> are rewritten on{" "}
            <code>/v1/messages</code>; GPT targets bridge through Codex, real Claude models route natively (same as
            the reference <code>proxy</code> project).
          </p>
        </div>
        <div className="mapping-page-actions">
          <div className="usage-segmented">
            <button
              type="button"
              className={activeTab === "mapping" ? "active" : ""}
              onClick={() => setActiveTab("mapping")}
            >
              Mapping
            </button>
            <button
              type="button"
              className={activeTab === "code" ? "active" : ""}
              onClick={() => setActiveTab("code")}
            >
              Generate code
            </button>
          </div>
          <Button variant="outline" size="sm" icon="refresh" disabled={loading} onClick={() => void load()}>
            Refresh
          </Button>
        </div>
      </div>

      {activeTab === "code" ? (
        <MappingCodeTab apiKeys={apiKeys} overrides={overrides} data={data} />
      ) : (
      <div className="mapping-grid">
        <Card pad="md" className="mapping-card">
          <div className="mapping-card-head">
            <span className="material-symbols-outlined">swap_horiz</span>
            <div>
              <strong>Tier routing</strong>
              <p>
                Map each tier to a virtual model ID (for example <code>mapping-fable</code>), a{" "}
                <code>provider:upstream-model</code> selector, or leave blank for built-in defaults. Env vars such as{" "}
                <code>ANTHROPIC_DEFAULT_FABLE_MODEL</code> override config when set on the tproxy process.
              </p>
            </div>
          </div>

          <Field label="Default Codex provider prefix" hint="Used when a target looks like gpt-* without provider prefix.">
            <Input
              list="mapping-codex-providers"
              value={defaultCodexProvider}
              onChange={(event) => setDefaultCodexProvider(event.target.value)}
              placeholder="codex"
            />
            <datalist id="mapping-codex-providers">
              {codexProviders.map((providerId) => (
                <option key={providerId} value={providerId} />
              ))}
            </datalist>
          </Field>

          <div className="mapping-tier-grid">
            {TIERS.map((tier) => (
              <div className="mapping-tier-row" key={tier.id}>
                <div className="mapping-tier-label">
                  <strong>{tier.label}</strong>
                  <span>{tier.hint}</span>
                  {data?.effective_resolved?.[tier.id] ? (
                    <Badge variant="default" size="sm">
                      → {data.effective_resolved[tier.id].resolved}
                    </Badge>
                  ) : data?.effective?.[tier.id] ? (
                    <Badge variant="default" size="sm">
                      → {data.effective[tier.id]}
                    </Badge>
                  ) : null}
                  {data?.effective_resolved?.[tier.id]?.route === "claude-native" ? (
                    <Badge variant="info" size="sm">Claude native</Badge>
                  ) : data?.effective_resolved?.[tier.id]?.route === "virtual-model" ? (
                    <Badge variant="success" size="sm">Virtual model</Badge>
                  ) : data?.effective_resolved?.[tier.id] ? (
                    <Badge variant="primary" size="sm">Codex bridge</Badge>
                  ) : null}
                  {data?.env_defaults?.[tier.id] ? (
                    <Badge variant="default" size="sm">env: {data.env_defaults[tier.id]}</Badge>
                  ) : null}
                </div>
                <Input
                  list={`mapping-models-${tier.id}`}
                  value={overrides[tier.id] || ""}
                  placeholder={data?.defaults?.[tier.id] || "provider:model"}
                  onChange={(event) => setOverrides((current) => ({ ...current, [tier.id]: event.target.value }))}
                />
                <datalist id={`mapping-models-${tier.id}`}>
                  {modelOptions.map((option) => (
                    <option key={option.value} value={option.value}>
                      {option.label}
                    </option>
                  ))}
                </datalist>
              </div>
            ))}
          </div>

          <div className="mapping-actions">
            <Button variant="primary" size="sm" icon="save" disabled={saving} onClick={() => void handleSave()}>
              {saving ? "Saving…" : "Save mapping"}
            </Button>
          </div>
        </Card>

        <Card pad="md" className="mapping-card">
          <div className="mapping-card-head">
            <span className="material-symbols-outlined">list_alt</span>
            <div>
              <strong>Placeholder rewrite</strong>
              <p>Incoming request model IDs rewritten before routing; responses keep the client-facing name.</p>
            </div>
          </div>
          <div className="mapping-placeholder-list">
            {(data?.placeholders || []).map((item) => (
              <div className="mapping-placeholder-row" key={item.name}>
                <code>{item.name}</code>
                <span className="mapping-placeholder-arrow">→</span>
                <code className="mapping-placeholder-target">{item.resolves}</code>
              </div>
            ))}
          </div>
        </Card>

        <Card pad="md" className="mapping-card mapping-card-wide">
          <div className="mapping-card-head">
            <span className="material-symbols-outlined">terminal</span>
            <div>
              <strong>Server env example</strong>
              <p>Real upstream targets on the tproxy process. Dashboard overrides take priority over env.</p>
            </div>
          </div>
          <pre className="mapping-env-example">{`ANTHROPIC_DEFAULT_FABLE_MODEL=mapping-fable
ANTHROPIC_DEFAULT_OPUS_MODEL=mapping-opus
ANTHROPIC_DEFAULT_SONNET_MODEL=mapping-sonnet
ANTHROPIC_DEFAULT_HAIKU_MODEL=mapping-haiku`}</pre>
          <p style={{ margin: "8px 0 0", fontSize: 12, color: "var(--color-text-muted)" }}>
            Claude Code keeps placeholder names (<code>fable</code>, <code>sonnet</code>, …) in{" "}
            <code>~/.claude/settings.json</code>. Use the <strong>Generate code</strong> tab for the client config;
            only the server needs virtual model IDs like <code>mapping-fable</code>.
          </p>
        </Card>

        <Card pad="md" className="mapping-card mapping-card-wide">
          <div className="mapping-card-head">
            <span className="material-symbols-outlined">settings</span>
            <div>
              <strong>Claude Code client example</strong>
              <p>Placeholder tier names — tproxy rewrites on <code>/v1/messages</code>.</p>
            </div>
          </div>
          <pre className="mapping-env-example">{`{
  "env": {
    "ANTHROPIC_BASE_URL": "http://localhost:28121/v1",
    "ANTHROPIC_API_KEY": "your-api-key",
    "ANTHROPIC_MODEL": "fable",
    "ANTHROPIC_DEFAULT_FABLE_MODEL": "fable",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "opus",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "sonnet",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "haiku",
    "CLAUDE_CODE_SUBAGENT_MODEL": "fable",
    "CLAUDE_CODE_AUTO_COMPACT_WINDOW": "1048576",
    "CLAUDE_CODE_MAX_CONTEXT_TOKENS": "1048576"
  }
}`}</pre>
        </Card>

        <Card pad="md" className="mapping-card mapping-card-wide">
          <div className="mapping-card-head">
            <span className="material-symbols-outlined">schema</span>
            <div>
              <strong>Content & protocol mapping</strong>
              <p>Active translation layers ported from the reference proxy project.</p>
            </div>
          </div>
          <div className="mapping-protocol-grid">
            {Object.entries(data?.content_mapping || {}).map(([protocol, entries]) => (
              <div className="mapping-protocol-block" key={protocol}>
                <h3>{protocol}</h3>
                <ul>
                  {Object.entries(entries).map(([key, value]) => (
                    <li key={key}>
                      <span className={cn("mapping-protocol-key")}>{key}</span>
                      <span>{value}</span>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        </Card>
      </div>
      )}
    </section>
  );
}
