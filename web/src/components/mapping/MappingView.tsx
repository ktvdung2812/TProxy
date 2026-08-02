import { useCallback, useEffect, useMemo, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import type { ApiKeyOption } from "../cli-tools/ApiKeySelect";
import { useChatModels } from "../chat/useChatModels";
import { Badge, Button, Card, Select, cn } from "../ui";
import {
  fetchClaudeMapping,
  fetchCursorMapping,
  saveClaudeMapping,
  saveCursorMapping,
  type ClaudeMappingResponse,
  type CursorCatalogModel,
  type CursorMappingResponse,
} from "./api";
import type { ModelRecord, RouteRecord } from "../models/types";
import {
  reasoningEffortOptionsForTarget,
  CLAUDE_MAPPING_PLACEHOLDER_NAMES,
  CODEX_MAPPING_PLACEHOLDER_NAMES,
  type ReasoningEffort,
} from "./codegen";
import { MappingCodePanel } from "./MappingCodePanel";
import { ModelTargetCombobox } from "./ModelTargetCombobox";
import { useMappingTargetOptions } from "./modelOptions";
import { formatMappingTargetLabel, parseMappingTab, type MappingClientTab } from "./utils";

type ComboRecord = {
  id: string;
  display_name?: string;
  enabled?: boolean;
};

type Props = {
  secret: string;
  apiKeys: ApiKeyOption[];
  models: ModelRecord[];
  combos: ComboRecord[];
  routesByModel: Record<string, RouteRecord[]>;
  providers: { ID: string; Name?: string; Enabled?: boolean }[];
  credentials: Record<string, { enabled: boolean }[]>;
  onError: (message: string) => void;
  onNotice: (message: string) => void;
};

type ClientTab = MappingClientTab;

const TIERS = [
  {
    id: "default",
    claudeLabel: "Default",
    codexLabel: "Default",
    claudeHint: "default",
    codexHint: "Claude-only placeholder — not used by Codex CLI",
  },
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
    claudeHint: "claude-sonnet, sonnet",
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

function emptyTierRecord(): Record<string, string> {
  return { default: "", fable: "", opus: "", sonnet: "", haiku: "" };
}

function emptyEffortRecord(): Record<string, ReasoningEffort> {
  return { default: "", fable: "", opus: "", sonnet: "", haiku: "" };
}

/** Only keep non-empty mappings — users add pairs on demand. */
function buildCursorOverrideState(overrides: Record<string, string> | undefined): Record<string, string> {
  const next: Record<string, string> = {};
  if (!overrides) return next;
  for (const [source, target] of Object.entries(overrides)) {
    const key = source.trim();
    const value = (target || "").trim();
    if (!key || !value) continue;
    next[key] = value;
  }
  return next;
}

function cursorModelLabel(catalog: CursorCatalogModel[], id: string): string {
  const found = catalog.find((model) => model.id.toLowerCase() === id.toLowerCase());
  if (!found) return id;
  return found.name && found.name !== found.id ? `${found.name} (${found.id})` : found.id;
}

export function MappingView({
  secret,
  apiKeys,
  models,
  combos,
  routesByModel,
  providers,
  credentials,
  onError,
  onNotice,
}: Props) {
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
  const [cursorData, setCursorData] = useState<CursorMappingResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [overrides, setOverrides] = useState<Record<string, string>>(emptyTierRecord);
  const [reasoningEffortOverrides, setReasoningEffortOverrides] =
    useState<Record<string, ReasoningEffort>>(emptyEffortRecord);
  const [cursorOverrides, setCursorOverrides] = useState<Record<string, string>>(() =>
    buildCursorOverrideState(undefined),
  );
  const [addCursorSource, setAddCursorSource] = useState("");
  const [addCursorTarget, setAddCursorTarget] = useState("");
  const [refreshingCursorCatalog, setRefreshingCursorCatalog] = useState(false);

  const applyCursorResponse = useCallback(
    (cursorResponse: CursorMappingResponse, options?: { replaceOverrides?: boolean }) => {
      setCursorData(cursorResponse);
      if (options?.replaceOverrides !== false) {
        setCursorOverrides(buildCursorOverrideState(cursorResponse.overrides));
      }
    },
    [],
  );

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [claudeResponse, cursorResponse] = await Promise.all([
        fetchClaudeMapping(secret),
        // Backend auto-upgrades from static→live; no need for ?refresh=1 on every page load.
        fetchCursorMapping(secret),
      ]);
      setData(claudeResponse);
      setOverrides({
        default: claudeResponse.overrides?.default || "",
        fable: claudeResponse.overrides?.fable || "",
        opus: claudeResponse.overrides?.opus || "",
        sonnet: claudeResponse.overrides?.sonnet || "",
        haiku: claudeResponse.overrides?.haiku || "",
      });
      setReasoningEffortOverrides({
        default: claudeResponse.reasoning_effort_overrides?.default || "",
        fable: claudeResponse.reasoning_effort_overrides?.fable || "",
        opus: claudeResponse.reasoning_effort_overrides?.opus || "",
        sonnet: claudeResponse.reasoning_effort_overrides?.sonnet || "",
        haiku: claudeResponse.reasoning_effort_overrides?.haiku || "",
      });
      applyCursorResponse(cursorResponse);
      setAddCursorSource("");
      setAddCursorTarget("");

      // If we still only got the thin static fallback, force a live rediscover once.
      const catalogLen = cursorResponse.cursor_models?.length || cursorResponse.catalog_count || 0;
      if (catalogLen > 0 && catalogLen <= 20 && cursorResponse.catalog_source !== "discovery") {
        setRefreshingCursorCatalog(true);
        try {
          const live = await fetchCursorMapping(secret, { refresh: true });
          applyCursorResponse(live);
        } catch {
          /* keep static catalog */
        } finally {
          setRefreshingCursorCatalog(false);
        }
      }
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "Failed to load mapping");
    } finally {
      setLoading(false);
    }
  }, [secret, onError, applyCursorResponse]);

  const refreshCursorCatalog = useCallback(async () => {
    setRefreshingCursorCatalog(true);
    try {
      const cursorResponse = await fetchCursorMapping(secret, { refresh: true });
      // Keep unsaved local rows; only refresh the Cursor model catalog.
      applyCursorResponse(cursorResponse, { replaceOverrides: false });
      const count = cursorResponse.cursor_models?.length || cursorResponse.catalog_count || 0;
      onNotice(
        count > 0
          ? `Loaded ${count} Cursor models (${cursorResponse.catalog_source || "catalog"})`
          : "Cursor model catalog refreshed",
      );
      if (cursorResponse.discovery_error && count <= 20) {
        onError(cursorResponse.discovery_error);
      }
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "Failed to refresh Cursor models");
    } finally {
      setRefreshingCursorCatalog(false);
    }
  }, [secret, applyCursorResponse, onNotice, onError]);

  useEffect(() => {
    void load();
  }, [load]);

  const chatSnapshot = useMemo(
    () => ({
      providers: providers.map((provider) => ({
        ID: provider.ID,
        Name: provider.Name || provider.ID,
        Enabled: provider.Enabled !== false,
      })),
      credentials,
      models: models.map((model) => ({
        ID: model.ID,
        DisplayName: model.DisplayName,
        Enabled: model.Enabled,
        Capabilities: model.Capabilities,
      })),
      combos: combos.map((combo) => ({
        id: combo.id,
        display_name: combo.display_name || combo.id,
        enabled: combo.enabled !== false,
        capabilities: [],
      })),
    }),
    [providers, credentials, models, combos],
  );
  const { models: discoveredModels } = useChatModels(secret, chatSnapshot);

  const modelOptions = useMappingTargetOptions(models, combos, routesByModel, discoveredModels);

  const formatTargetLabel = useCallback(
    (target: string) => formatMappingTargetLabel(target, models, routesByModel),
    [models, routesByModel],
  );

  const isClaude = activeTab === "claude";
  const isCodex = activeTab === "codex";
  const isCursor = activeTab === "cursor";

  const filteredPlaceholders = useMemo(() => {
    if (isCursor) {
      return (cursorData?.placeholders || []).filter((item) => item.resolves);
    }
    const items = data?.placeholders || [];
    const filter = isClaude ? isClaudePlaceholder : isCodexPlaceholder;
    const order = isClaude ? CLAUDE_MAPPING_PLACEHOLDER_NAMES : CODEX_MAPPING_PLACEHOLDER_NAMES;
    const rank = new Map<string, number>(order.map((name, index) => [name, index]));
    return items
      .filter((item) => filter(item.name))
      .sort((left, right) => (rank.get(left.name) ?? 0) - (rank.get(right.name) ?? 0));
  }, [activeTab, data?.placeholders, cursorData?.placeholders, isClaude, isCursor]);

  const visibleTiers = useMemo(
    () => (isClaude ? TIERS : TIERS.filter((tier) => tier.id !== "sonnet" && tier.id !== "default")),
    [isClaude],
  );

  const cursorCatalog = useMemo((): CursorCatalogModel[] => {
    const catalog = cursorData?.cursor_models || [];
    if (catalog.length > 0) return catalog;
    // Fallback labels for mapped sources if catalog is empty.
    return Object.keys(cursorOverrides)
      .sort()
      .map((id) => ({ id, name: id }));
  }, [cursorData?.cursor_models, cursorOverrides]);

  const cursorCatalogById = useMemo(() => {
    const map = new Map<string, CursorCatalogModel>();
    for (const model of cursorCatalog) {
      map.set(model.id.toLowerCase(), model);
    }
    return map;
  }, [cursorCatalog]);

  const mappedCursorRows = useMemo(() => {
    return Object.entries(cursorOverrides)
      .filter(([, target]) => target.trim())
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([source, target]) => ({
        source,
        target,
        label: cursorModelLabel(cursorCatalog, source),
      }));
  }, [cursorOverrides, cursorCatalog]);

  const availableCursorSources = useMemo(() => {
    const mapped = new Set(Object.keys(cursorOverrides).map((id) => id.toLowerCase()));
    return cursorCatalog.filter((model) => !mapped.has(model.id.toLowerCase()));
  }, [cursorCatalog, cursorOverrides]);

  const contentMapping = isCursor ? cursorData?.content_mapping : data?.content_mapping;

  const handleSaveClaude = async () => {
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

  const handleSaveCursor = async () => {
    setSaving(true);
    try {
      // Include the pending form row so Save works without a separate Add click.
      const merged: Record<string, string> = { ...cursorOverrides };
      const pendingSource = addCursorSource.trim();
      const pendingTarget = addCursorTarget.trim();
      if (pendingSource && pendingTarget) {
        merged[pendingSource] = pendingTarget;
      } else if (pendingSource || pendingTarget) {
        throw new Error("Select both a Cursor model and a proxy model before saving");
      }

      const payload = buildCursorOverrideState(merged);
      const previouslySaved = Object.keys(buildCursorOverrideState(cursorData?.overrides)).length > 0;
      if (Object.keys(payload).length === 0 && !previouslySaved) {
        throw new Error("Add at least one Cursor → proxy mapping before saving");
      }

      const response = await saveCursorMapping(secret, { overrides: payload });
      const saved = buildCursorOverrideState(response.overrides ?? payload);
      if (Object.keys(saved).length === 0 && Object.keys(payload).length > 0) {
        // Keep local rows if the server unexpectedly returned an empty map.
        setCursorOverrides(payload);
        throw new Error("Server accepted the request but returned no mappings — check model IDs and try again");
      }
      setCursorData(response);
      setCursorOverrides(saved);
      setAddCursorSource("");
      setAddCursorTarget("");
      onNotice(
        Object.keys(saved).length === 0
          ? "Cleared all Cursor model mappings"
          : `Saved ${Object.keys(saved).length} Cursor model mapping${Object.keys(saved).length === 1 ? "" : "s"}`,
      );
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "Failed to save Cursor mapping");
    } finally {
      setSaving(false);
    }
  };

  const addCursorMapping = () => {
    const source = addCursorSource.trim();
    const target = addCursorTarget.trim();
    if (!source || !target) {
      onError("Select both a Cursor model and a proxy model before adding");
      return;
    }
    if (cursorOverrides[source]?.trim()) {
      onError(`Cursor model ${source} is already mapped — change or remove it first`);
      return;
    }
    setCursorOverrides((current) => ({ ...current, [source]: target }));
    setAddCursorSource("");
    setAddCursorTarget("");
  };

  const removeCursorMapping = (id: string) => {
    setCursorOverrides((current) => {
      const next = { ...current };
      delete next[id];
      return next;
    });
  };

  if (loading && !data && !cursorData) {
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
            ) : isCodex ? (
              <>
                Route Codex CLI without changing model names in the client. GPT codenames like{" "}
                <code>gpt-sol</code> or <code>gpt-terra</code> rewrite on <code>/v1/chat/completions</code> and{" "}
                <code>/v1/responses</code>.
              </>
            ) : (
              <>
                Map Cursor model IDs (e.g. <code>claude-4.5-sonnet</code>) to tproxy virtual models. Add only the pairs
                you need — rewrites apply on <code>/v1/chat/completions</code>. Requires a public/tunnel URL.
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
            <button
              type="button"
              className={activeTab === "cursor" ? "active" : ""}
              onClick={() => setActiveTab("cursor")}
            >
              Cursor
            </button>
          </div>
          <Button variant="outline" size="sm" icon="refresh" disabled={loading} onClick={() => void load()}>
            Refresh
          </Button>
        </div>
      </div>

      <div className="mapping-grid">
        {isCursor ? (
          <Card pad="md" className="mapping-card mapping-card-wide">
            <div className="mapping-card-head">
              <span className="material-symbols-outlined">swap_horiz</span>
              <div>
                <strong>Cursor → proxy model map</strong>
                <p>
                  Full Cursor model list comes from the same discovery as{" "}
                  <code>/dashboard/providers/cursor</code>. Pick a Cursor model, pick a tproxy virtual model (or{" "}
                  <code>provider:upstream-model</code>), then Add — only map what you need.
                </p>
                <div className="mapping-cursor-catalog-meta">
                  <Badge variant="default" size="sm">
                    {cursorCatalog.length} Cursor models
                  </Badge>
                  {cursorData?.catalog_source ? (
                    <Badge variant={cursorData.catalog_source === "discovery" ? "success" : "info"} size="sm">
                      {cursorData.catalog_source === "discovery"
                        ? "Live discovery"
                        : cursorData.catalog_source === "mixed"
                          ? "Discovery + static"
                          : "Static fallback"}
                    </Badge>
                  ) : null}
                  {cursorData?.provider_id ? (
                    <Badge variant="default" size="sm">
                      provider: {cursorData.provider_id}
                    </Badge>
                  ) : null}
                  <Button
                    variant="ghost"
                    size="sm"
                    icon={refreshingCursorCatalog ? "progress_activity" : "sync"}
                    disabled={refreshingCursorCatalog || loading}
                    onClick={() => void refreshCursorCatalog()}
                  >
                    {refreshingCursorCatalog ? "Discovering…" : "Discover models"}
                  </Button>
                </div>
                {cursorData?.discovery_error ? (
                  <p className="mapping-cursor-discovery-error">{cursorData.discovery_error}</p>
                ) : null}
              </div>
            </div>

            <div className="mapping-cursor-add-form">
              <div className="mapping-cursor-add-fields">
                <div className="mapping-cursor-field">
                  <label htmlFor="cursor-source-model">
                    Cursor model ({availableCursorSources.length} available)
                  </label>
                  <Select
                    id="cursor-source-model"
                    value={addCursorSource}
                    onChange={(event) => setAddCursorSource(event.target.value)}
                    aria-label="Cursor model"
                  >
                    <option value="">
                      {cursorCatalog.length === 0
                        ? "No Cursor models — connect provider or Discover"
                        : "Select Cursor model…"}
                    </option>
                    {availableCursorSources.map((model) => (
                      <option key={model.id} value={model.id}>
                        {model.name && model.name !== model.id ? `${model.name} (${model.id})` : model.id}
                      </option>
                    ))}
                  </Select>
                </div>
                <span className="mapping-placeholder-arrow mapping-cursor-arrow" aria-hidden>
                  →
                </span>
                <div className="mapping-cursor-field mapping-cursor-field-target">
                  <label htmlFor="cursor-target-model">Proxy model</label>
                  <ModelTargetCombobox
                    aria-label="Proxy model"
                    value={addCursorTarget}
                    placeholder="virtual-model or provider:model"
                    options={modelOptions}
                    onChange={setAddCursorTarget}
                  />
                </div>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  icon="add"
                  disabled={!addCursorSource || !addCursorTarget.trim()}
                  onClick={addCursorMapping}
                >
                  Add
                </Button>
              </div>
              {cursorCatalog.length > 0 && availableCursorSources.length === 0 ? (
                <p className="mapping-placeholder-empty">All catalog Cursor models are already mapped.</p>
              ) : null}
            </div>

            <div className="mapping-tier-grid mapping-cursor-rows">
              {mappedCursorRows.length === 0 ? (
                <p className="mapping-placeholder-empty">No mappings yet. Add a Cursor model → proxy model pair above.</p>
              ) : (
                mappedCursorRows.map((row) => (
                  <div className="mapping-tier-row mapping-cursor-row" key={row.source}>
                    <div className="mapping-tier-label">
                      <strong>{cursorCatalogById.get(row.source.toLowerCase())?.name || row.source}</strong>
                      <span>
                        <code>{row.source}</code>
                      </span>
                      {cursorData?.effective?.[row.source] ? (
                        <Badge variant="default" size="sm">
                          → {formatTargetLabel(cursorData.effective[row.source])}
                        </Badge>
                      ) : null}
                    </div>
                    <div className="mapping-tier-controls">
                      <ModelTargetCombobox
                        aria-label={`${row.source} proxy model`}
                        value={row.target}
                        placeholder="virtual-model or provider:model"
                        options={modelOptions}
                        onChange={(next) =>
                          setCursorOverrides((current) => ({ ...current, [row.source]: next }))
                        }
                      />
                      <Button
                        variant="ghost"
                        size="sm"
                        icon="close"
                        aria-label={`Remove ${row.source}`}
                        onClick={() => removeCursorMapping(row.source)}
                      />
                    </div>
                  </div>
                ))
              )}
            </div>

            <div className="mapping-actions">
              <Button
                type="button"
                variant="primary"
                size="sm"
                icon="save"
                disabled={saving}
                onClick={() => void handleSaveCursor()}
              >
                {saving ? "Saving…" : "Save Cursor mapping"}
              </Button>
            </div>
          </Card>
        ) : (
          <Card pad="md" className="mapping-card">
            <div className="mapping-card-head">
              <span className="material-symbols-outlined">swap_horiz</span>
              <div>
                <strong>Tier routing</strong>
                <p>
                  {isClaude ? (
                    <>
                      Map each Claude tier placeholder to a virtual model ID or a <code>provider:upstream-model</code>{" "}
                      selector. Env vars such as <code>ANTHROPIC_DEFAULT_FABLE_MODEL</code> override config when set on
                      the tproxy process.
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
              <Button variant="primary" size="sm" icon="save" disabled={saving} onClick={() => void handleSaveClaude()}>
                {saving ? "Saving…" : "Save mapping"}
              </Button>
            </div>
          </Card>
        )}

        <Card pad="md" className="mapping-card">
          <div className="mapping-card-head">
            <span className="material-symbols-outlined">list_alt</span>
            <div>
              <strong>Placeholder rewrite</strong>
              <p>
                {isClaude
                  ? "Claude client model IDs rewritten before routing."
                  : isCodex
                    ? "Codex / OpenAI client model IDs rewritten before routing."
                    : "Cursor custom model IDs rewritten before routing."}
              </p>
            </div>
          </div>
          <div className="mapping-placeholder-list">
            {filteredPlaceholders.length === 0 ? (
              <p className="mapping-placeholder-empty">
                {isCursor ? "No Cursor aliases configured yet." : "No placeholders configured yet."}
              </p>
            ) : (
              filteredPlaceholders.map((item) => (
                <div className="mapping-placeholder-row" key={item.name}>
                  <code>{item.name}</code>
                  <span className="mapping-placeholder-arrow">→</span>
                  <code className="mapping-placeholder-target">
                    {item.resolves ? formatTargetLabel(item.resolves) : "—"}
                  </code>
                </div>
              ))
            )}
          </div>
        </Card>

        <MappingCodePanel
          client={activeTab}
          apiKeys={apiKeys}
          overrides={isCursor ? cursorOverrides : overrides}
          data={data}
        />

        <Card pad="md" className="mapping-card mapping-card-wide">
          <div className="mapping-card-head">
            <span className="material-symbols-outlined">schema</span>
            <div>
              <strong>Content & protocol mapping</strong>
              <p>Active translation layers ported from the reference proxy project.</p>
            </div>
          </div>
          <div className="mapping-protocol-grid">
            {Object.entries(contentMapping || {}).map(([protocol, entries]) => (
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
