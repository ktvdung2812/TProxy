import { useCallback, useEffect, useMemo, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import type { ApiKeyOption } from "../cli-tools/ApiKeySelect";
import { Badge, Button, Card, Select, cn } from "../ui";
import { fetchClaudeMapping, saveClaudeMapping, type ClaudeMappingResponse } from "./api";
import type { ModelRecord, RouteRecord } from "../models/types";
import { reasoningEffortOptionsForTarget, CLAUDE_MAPPING_PLACEHOLDER_NAMES, CODEX_MAPPING_PLACEHOLDER_NAMES, type ReasoningEffort } from "./codegen";
import { MappingCodePanel } from "./MappingCodePanel";
import { ModelTargetCombobox } from "./ModelTargetCombobox";
import { formatMappingTargetLabel, parseMappingTab, type MappingClientTab } from "./utils";

type Props = {
  secret: string;
  apiKeys: ApiKeyOption[];
  models: ModelRecord[];
  routesByModel: Record<string, RouteRecord[]>;
  onError: (message: string) => void;
  onNotice: (message: string) => void;
};

type ClientTab = MappingClientTab;

const TIERS = [
  {
    id: "fable",
    claudeLabel: "Claude Fable",
    codexLabel: "GPT Sol",
    claudeHint: "claude-fable, fable",
    codexHint: "gpt-sol",
  },
  {
    id: "opus",
    claudeLabel: "Claude Opus",
    codexLabel: "GPT Terra",
    claudeHint: "claude-opus, opus, opusplan",
    codexHint: "gpt-terra",
  },
  {
    id: "sonnet",
    claudeLabel: "Claude Sonnet",
    codexLabel: "Sonnet",
    claudeHint: "claude-sonnet, sonnet, default",
    codexHint: "Claude-only placeholder — not used by Codex CLI",
  },
  {
    id: "haiku",
    claudeLabel: "Claude Haiku",
    codexLabel: "GPT Luna",
    claudeHint: "claude-haiku, haiku",
    codexHint: "gpt-luna",
  },
] as const;

const CLAUDE_PLACEHOLDER_SET = new Set<string>(CLAUDE_MAPPING_PLACEHOLDER_NAMES);
const CODEX_PLACEHOLDER_SET = new Set<string>(CODEX_MAPPING_PLACEHOLDER_NAMES);

function isClaudePlaceholder(name: string) {
  return CLAUDE_PLACEHOLDER_SET.has(name.toLowerCase());
}

function isCodexPlaceholder(name: string) {
  return CODEX_PLACEHOLDER_SET.has(name.toLowerCase());
}

export function MappingView({ secret, apiKeys, models, routesByModel, onError, onNotice }: Props) {
  const location = useLocation();
  const navigate = useNavigate();
  const activeTab = useMemo(() => parseMappingTab(location.hash), [location.hash]);
  const setActiveTab = useCallback(
    (tab: ClientTab) => {
      navigate({ pathname: "/mapping", hash: tab }, { replace: true });
    },
    [navigate],
  );

  useEffect(() => {
    if (!location.hash) {
      navigate({ pathname: "/mapping", hash: "claude" }, { replace: true });
    }
  }, [location.hash, navigate]);

  const [data, setData] = useState<ClaudeMappingResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [overrides, setOverrides] = useState<Record<string, string>>({
    fable: "",
    opus: "",
    sonnet: "",
    haiku: "",
  });
  const [reasoningEffortOverrides, setReasoningEffortOverrides] = useState<Record<string, ReasoningEffort>>({
    fable: "",
    opus: "",
    sonnet: "",
    haiku: "",
  });

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
      setReasoningEffortOverrides({
        fable: response.reasoning_effort_overrides?.fable || "",
        opus: response.reasoning_effort_overrides?.opus || "",
        sonnet: response.reasoning_effort_overrides?.sonnet || "",
        haiku: response.reasoning_effort_overrides?.haiku || "",
      });
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

  const formatTargetLabel = useCallback(
    (target: string) => formatMappingTargetLabel(target, models, routesByModel),
    [models, routesByModel],
  );

  const filteredPlaceholders = useMemo(() => {
    const items = data?.placeholders || [];
    const filter = activeTab === "claude" ? isClaudePlaceholder : isCodexPlaceholder;
    const order = activeTab === "claude" ? CLAUDE_MAPPING_PLACEHOLDER_NAMES : CODEX_MAPPING_PLACEHOLDER_NAMES;
    const rank = new Map<string, number>(order.map((name, index) => [name, index]));
    return items
      .filter((item) => filter(item.name))
      .sort((left, right) => (rank.get(left.name) ?? 0) - (rank.get(right.name) ?? 0));
  }, [activeTab, data?.placeholders]);

  const isClaude = activeTab === "claude";
  const visibleTiers = useMemo(
    () => (isClaude ? TIERS : TIERS.filter((tier) => tier.id !== "sonnet")),
    [isClaude],
  );

  const handleSave = async () => {
    setSaving(true);
    try {
      const response = await saveClaudeMapping(secret, {
        overrides,
        reasoning_effort_overrides: reasoningEffortOverrides,
      });
      setData(response);
      onNotice("Routing map saved");
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
          <h2>Transparent model routing</h2>
          <p>
            {isClaude ? (
              <>
                Route Claude Code without changing model names in the client. Placeholders like{" "}
                <code>sonnet</code> or <code>fable</code> rewrite on <code>/v1/messages</code>.
              </>
            ) : (
              <>
                Route Codex CLI without changing model names in the client. GPT codenames like{" "}
                <code>gpt-sol</code> or <code>gpt-terra</code> rewrite on <code>/v1/chat/completions</code> and{" "}
                <code>/v1/responses</code>.
              </>
            )}
          </p>
        </div>
        <div className="mapping-page-actions">
          <div className="usage-segmented">
            <button
              type="button"
              className={activeTab === "claude" ? "active" : ""}
              onClick={() => setActiveTab("claude")}
            >
              Claude
            </button>
            <button
              type="button"
              className={activeTab === "codex" ? "active" : ""}
              onClick={() => setActiveTab("codex")}
            >
              Codex
            </button>
          </div>
          <Button variant="outline" size="sm" icon="refresh" disabled={loading} onClick={() => void load()}>
            Refresh
          </Button>
        </div>
      </div>

      <div className="mapping-grid">
        <Card pad="md" className="mapping-card">
          <div className="mapping-card-head">
            <span className="material-symbols-outlined">swap_horiz</span>
            <div>
              <strong>Tier routing</strong>
              <p>
                {isClaude ? (
                  <>
                    Map each Claude tier placeholder to a virtual model ID or a <code>provider:upstream-model</code>{" "}
                    selector. Env vars such as <code>ANTHROPIC_DEFAULT_FABLE_MODEL</code> override config when set on the
                    tproxy process.
                  </>
                ) : (
                  <>
                    Map each Codex GPT codename (<code>gpt-sol</code>, <code>gpt-terra</code>, <code>gpt-luna</code>) to
                    a virtual model ID or a <code>provider:upstream-model</code> selector. Codex does not use Claude
                    tier names like <code>sonnet</code>.
                  </>
                )}
              </p>
            </div>
          </div>

          <div className="mapping-tier-grid">
            {visibleTiers.map((tier) => (
              <div className="mapping-tier-row" key={tier.id}>
                <div className="mapping-tier-label">
                  <strong>{isClaude ? tier.claudeLabel : tier.codexLabel}</strong>
                  <span>{isClaude ? tier.claudeHint : tier.codexHint}</span>
                  {data?.effective_resolved?.[tier.id] ? (
                    <Badge variant="default" size="sm">
                      → {formatTargetLabel(data.effective_resolved[tier.id].resolved)}
                    </Badge>
                  ) : data?.effective?.[tier.id] ? (
                    <Badge variant="default" size="sm">
                      → {formatTargetLabel(data.effective[tier.id])}
                    </Badge>
                  ) : null}
                  {data?.effective_resolved?.[tier.id]?.route === "claude-native" ? (
                    <Badge variant="info" size="sm">
                      Claude native
                    </Badge>
                  ) : data?.effective_resolved?.[tier.id]?.route === "virtual-model" ? (
                    <Badge variant="success" size="sm">
                      Virtual model
                    </Badge>
                  ) : data?.effective_resolved?.[tier.id] ? (
                    <Badge variant="primary" size="sm">
                      Codex bridge
                    </Badge>
                  ) : null}
                  {data?.effective_reasoning_effort?.[tier.id] ? (
                    <Badge variant="info" size="sm">
                      effort: {data.effective_reasoning_effort[tier.id]}
                    </Badge>
                  ) : null}
                  {data?.env_defaults?.[tier.id] ? (
                    <Badge variant="default" size="sm">
                      env: {data.env_defaults[tier.id]}
                    </Badge>
                  ) : null}
                </div>
                <div className="mapping-tier-controls">
                  <ModelTargetCombobox
                    aria-label={`${isClaude ? tier.claudeLabel : tier.codexLabel} target model`}
                    value={overrides[tier.id] || ""}
                    placeholder={data?.defaults?.[tier.id] || "provider:model"}
                    options={modelOptions}
                    onChange={(next) => setOverrides((current) => ({ ...current, [tier.id]: next }))}
                  />
                  <Select
                    className="mapping-tier-effort"
                    aria-label={`${isClaude ? tier.claudeLabel : tier.codexLabel} reasoning effort`}
                    value={reasoningEffortOverrides[tier.id] || ""}
                    onChange={(event) =>
                      setReasoningEffortOverrides((current) => ({
                        ...current,
                        [tier.id]: event.target.value as ReasoningEffort,
                      }))
                    }
                  >
                    {reasoningEffortOptionsForTarget(
                      overrides[tier.id] || data?.effective_resolved?.[tier.id]?.resolved || "",
                      reasoningEffortOverrides[tier.id] || "",
                    ).map((option) => (
                      <option key={option.value || "default"} value={option.value}>
                        {option.label}
                      </option>
                    ))}
                  </Select>
                </div>
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
              <p>
                {isClaude
                  ? "Claude client model IDs rewritten before routing."
                  : "Codex / OpenAI client model IDs rewritten before routing."}
              </p>
            </div>
          </div>
          <div className="mapping-placeholder-list">
            {filteredPlaceholders.length === 0 ? (
              <p className="mapping-placeholder-empty">No placeholders configured yet.</p>
            ) : (
              filteredPlaceholders.map((item) => (
                <div className="mapping-placeholder-row" key={item.name}>
                  <code>{item.name}</code>
                  <span className="mapping-placeholder-arrow">→</span>
                  <code className="mapping-placeholder-target">{formatTargetLabel(item.resolves)}</code>
                </div>
              ))
            )}
          </div>
        </Card>

        <MappingCodePanel client={activeTab} apiKeys={apiKeys} overrides={overrides} data={data} />

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
    </section>
  );
}
