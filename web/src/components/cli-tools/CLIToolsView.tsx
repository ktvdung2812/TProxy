import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { Button, ConfirmDialog } from "../ui";
import {
  EmptyPanel,
  FilterTabs,
  GuideToggle,
  LegendItem,
  PageGuide,
  PageNote,
  PageStats,
  SearchField,
  SectionHead,
  Segmented,
  SelectField,
  ToneCounts,
  Toolbar,
  ToolbarEnd,
  ToolbarRow,
  ToolButton,
  usePageGuide,
  useStoredState,
} from "../page-kit";
import { CLI_TOOLS, isAutoApplyTool, MITM_TOOLS } from "../../cli-tools/constants";
import { isLocalDashboardHost } from "../../devDefaults";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";
import { fetchCLIToolStatuses, resetCLIToolConfig, type CLIToolStatus } from "./api";
import { Note } from "./CopyBits";
import { ToolCard, ToolListRow, type ToolHandlers, type ToolView } from "./ToolSummaryCard";
import { useToolCopy } from "./toolCopy";
import {
  bucketOf,
  stateRank,
  toolConfigPath,
  toolState,
  TOOL_BUCKET_ICON,
  TOOL_BUCKET_TONE,
  TOOL_BUCKETS,
  type ToolBucket,
} from "./toolState";
import { useCliGateway } from "./useCliGateway";

type Props = {
  secret: string;
};

type Filter = ToolBucket | "all";
type SortMode = "catalog" | "status" | "name";
type ViewMode = "grid" | "list";
const SORT_MODES = ["catalog", "status", "name"] as const;
const VIEW_MODES = ["grid", "list"] as const;

const REGULAR_TOOLS = Object.values(CLI_TOOLS);
const MITM_TOOL_LIST = Object.values(MITM_TOOLS);

export function CLIToolsView({ secret }: Props) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const copyText = useToolCopy();
  const { copy } = useCopyToClipboard();
  const gateway = useCliGateway(secret);
  const guide = usePageGuide("cli-tools");
  const canReset = isLocalDashboardHost();
  const [statuses, setStatuses] = useState<Record<string, CLIToolStatus>>({});
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const [search, setSearch] = useState("");
  const [sortMode, setSortMode] = useStoredState<SortMode>("tproxy.cliTools.sort", "catalog", SORT_MODES);
  const [viewMode, setViewMode] = useStoredState<ViewMode>("tproxy.cliTools.view", "grid", VIEW_MODES);
  const [busyId, setBusyId] = useState("");
  const [resetTarget, setResetTarget] = useState<ToolView | null>(null);
  const [notice, setNotice] = useState<{ tone: "success" | "danger"; text: string } | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setStatuses(await fetchCLIToolStatuses(secret));
      setLoadError("");
    } catch (error) {
      setStatuses({});
      setLoadError(error instanceof Error ? error.message : String(error));
    } finally {
      setLoading(false);
    }
  }, [secret]);

  useEffect(() => {
    void load();
  }, [load]);

  // Eighteen tools: cheap enough to derive on every render.
  const toView = (tool: (typeof REGULAR_TOOLS)[number]): ToolView => {
    const autoApply = isAutoApplyTool(tool.id);
    const status = autoApply ? statuses[tool.id] : undefined;
    return {
      id: tool.id,
      tool,
      description: copyText.description(tool),
      state: toolState(tool, status, gateway.knownBaseUrls, loading),
      status,
      autoApply,
      configPath: tool.configType === "mitm" ? "" : toolConfigPath(tool, status),
      busy: busyId === tool.id,
    };
  };

  const query = search.trim().toLowerCase();
  const matches = (view: ToolView) =>
    !query ||
    [view.tool.name, view.id, view.description, view.configPath, view.status?.endpoint].some((value) =>
      value?.toLowerCase().includes(query),
    );

  const tools = REGULAR_TOOLS.map(toView);
  const searched = tools.filter(matches);
  const searchedMitm = MITM_TOOL_LIST.map(toView).filter(matches);

  const counts: Record<ToolBucket, number> = { connected: 0, setup: 0, missing: 0, manual: 0 };
  for (const view of searched) {
    const bucket = bucketOf(view.state);
    if (bucket) counts[bucket] += 1;
  }

  const visible = filter === "all" ? [...searched] : searched.filter((view) => bucketOf(view.state) === filter);
  if (sortMode === "name") visible.sort((a, b) => a.tool.name.localeCompare(b.tool.name));
  // Array.prototype.sort is stable, so equal states keep catalog order.
  if (sortMode === "status") visible.sort((a, b) => stateRank(a.state) - stateRank(b.state));

  const showMitm = filter === "all" && searchedMitm.length > 0;
  const toggleFilter = (next: Filter) => setFilter((current) => (current === next ? "all" : next));
  const hasFilters = filter !== "all" || search.trim() !== "";
  const clearFilters = () => {
    setFilter("all");
    setSearch("");
  };

  const handlers: ToolHandlers = {
    open: (view) => navigate(`/cli-tools/${view.id}`),
    copyBaseUrl: () => {
      copy(gateway.baseUrl, "base-url");
      setNotice({ tone: "success", text: t("cliTools.notice.baseUrlCopied", { url: gateway.baseUrl }) });
    },
    copyPath: (view) => {
      copy(view.configPath, "config-path");
      setNotice({ tone: "success", text: t("cliTools.notice.pathCopied", { path: view.configPath }) });
    },
    reset: (view) => setResetTarget(view),
  };

  const confirmReset = async () => {
    const view = resetTarget;
    setResetTarget(null);
    if (!view) return;
    setBusyId(view.id);
    try {
      await resetCLIToolConfig(secret, view.id);
      setNotice({ tone: "success", text: t("cliTools.notice.reset", { name: view.tool.name }) });
      await load();
    } catch (error) {
      setNotice({ tone: "danger", text: String(error) });
    } finally {
      setBusyId("");
    }
  };

  const autoCount = tools.filter((view) => view.autoApply).length;
  const kindLabel = t(`cliTools.endpointKind.${gateway.kind}`);
  const tile = (bucket: ToolBucket) => ({
    key: bucket,
    icon: TOOL_BUCKET_ICON[bucket],
    tone: TOOL_BUCKET_TONE[bucket],
    label: t(`cliTools.bucket.${bucket}`),
    value: loading && bucket !== "manual" ? "—" : counts[bucket],
    hint: t(`cliTools.bucketHint.${bucket}`),
    muted: counts[bucket] === 0,
    active: filter === bucket,
    onClick: () => toggleFilter(bucket),
  });

  const renderItems = (list: ToolView[]) =>
    viewMode === "grid" ? (
      <div className="cp-grid ct-grid">
        {list.map((view) => (
          <ToolCard key={view.id} view={view} handlers={handlers} canReset={canReset} />
        ))}
      </div>
    ) : (
      <div className="cp-list ct-list">
        <div className="cp-list-head" aria-hidden>
          <span>{t("cliTools.columns.tool")}</span>
          <span>{t("cliTools.columns.status")}</span>
          <span>{t("cliTools.columns.pointsAt")}</span>
          <span>{t("cliTools.columns.config")}</span>
          <span>{t("cliTools.columns.setup")}</span>
          <span />
        </div>
        {list.map((view) => (
          <ToolListRow key={view.id} view={view} handlers={handlers} canReset={canReset} />
        ))}
      </div>
    );

  return (
    <section className="cp-page ct-page">
      <PageStats
        label={t("cliTools.statsLabel")}
        items={[
          {
            key: "all",
            icon: "terminal",
            label: t("cliTools.stats.tools"),
            value: searched.length,
            hint: t("cliTools.stats.toolsHint", { count: autoCount }),
            active: filter === "all",
            onClick: () => setFilter("all"),
          },
          tile("connected"),
          tile("setup"),
          tile("missing"),
          tile("manual"),
          {
            key: "endpoint",
            icon: "link",
            label: t("cliTools.stats.endpoint"),
            value: kindLabel,
            mono: true,
            hint: gateway.baseUrl.replace(/^https?:\/\//, ""),
            title: t("cliTools.stats.endpointHint", { url: gateway.baseUrl }),
          },
        ]}
      />

      <Toolbar>
        <ToolbarRow>
          <FilterTabs
            label={t("cliTools.filterLabel")}
            value={filter}
            onChange={toggleFilter}
            items={[
              { value: "all", icon: "apps", label: t("cliTools.filterAll"), count: searched.length },
              ...TOOL_BUCKETS.map((bucket) => ({
                value: bucket,
                icon: TOOL_BUCKET_ICON[bucket],
                tone: TOOL_BUCKET_TONE[bucket],
                label: t(`cliTools.bucket.${bucket}`),
                count: counts[bucket],
              })),
            ]}
          />
          <ToolbarEnd>
            <ToolButton icon="refresh" spinning={loading} disabled={loading} onClick={() => void load()} title={t("cliTools.refreshHint")}>
              {t("cliTools.refreshStatus")}
            </ToolButton>
          </ToolbarEnd>
        </ToolbarRow>
        <ToolbarRow>
          <SearchField value={search} onChange={setSearch} placeholder={t("cliTools.searchPlaceholder")} />
          <ToolbarEnd>
            <SelectField
              label={t("cliTools.sortLabel")}
              value={sortMode}
              onChange={setSortMode}
              options={[
                { value: "catalog", label: t("cliTools.sort.catalog") },
                { value: "status", label: t("cliTools.sort.status") },
                { value: "name", label: t("cliTools.sort.name") },
              ]}
            />
            <Segmented
              label={t("cliTools.viewMode")}
              value={viewMode}
              onChange={setViewMode}
              items={[
                { value: "grid", icon: "grid_view", label: t("cliTools.viewGrid") },
                { value: "list", icon: "view_list", label: t("cliTools.viewList") },
              ]}
            />
            <GuideToggle open={guide.open} onToggle={guide.toggle} />
          </ToolbarEnd>
        </ToolbarRow>
      </Toolbar>

      <PageGuide
        open={guide.open}
        onDismiss={guide.dismiss}
        title={t("cliTools.guide.title")}
        steps={[
          { icon: "key", text: t("cliTools.guide.key") },
          { icon: "touch_app", text: t("cliTools.guide.pick") },
          { icon: "bolt", text: t("cliTools.guide.apply") },
        ]}
        legend={
          <>
            <LegendItem tone="success">{t("cliTools.state.connected")}</LegendItem>
            <LegendItem tone="warning">{t("cliTools.bucket.setup")}</LegendItem>
            <LegendItem tone="neutral">{t("cliTools.state.not_installed")}</LegendItem>
            <LegendItem tone="info">{t("cliTools.state.manual")}</LegendItem>
          </>
        }
      />

      {loadError ? (
        <Note tone="danger">
          {t("cliTools.statusLoadFailed", { message: loadError })}
        </Note>
      ) : null}
      {notice ? (
        <div className="ct-notice">
          <Note tone={notice.tone}>{notice.text}</Note>
          <button type="button" className="cp-icon-btn" aria-label={t("common.dismiss")} title={t("common.dismiss")} onClick={() => setNotice(null)}>
            <span className="material-symbols-outlined" aria-hidden>close</span>
          </button>
        </div>
      ) : null}

      {visible.length === 0 && !showMitm ? (
        <EmptyPanel
          icon="filter_alt_off"
          title={t("cliTools.noMatch")}
          description={hasFilters ? t("cliTools.noMatchHint") : undefined}
          action={hasFilters ? <Button variant="secondary" icon="filter_alt_off" onClick={clearFilters}>{t("cliTools.clearFilters")}</Button> : null}
        />
      ) : null}

      {visible.length > 0 ? (
        <div className="ct-group">
          <SectionHead
            leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>terminal</span>}
            title={t("cliTools.sections.tools")}
            subtitle={t("cliTools.sections.toolsSub", { count: visible.length, auto: visible.filter((view) => view.autoApply).length })}
            meta={
              <ToneCounts
                items={TOOL_BUCKETS.map((bucket) => ({
                  tone: TOOL_BUCKET_TONE[bucket],
                  count: visible.filter((view) => bucketOf(view.state) === bucket).length,
                  label: t(`cliTools.bucket.${bucket}`),
                }))}
              />
            }
          />
          {renderItems(visible)}
        </div>
      ) : null}

      {showMitm ? (
        <div className="ct-group">
          <SectionHead
            leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>security</span>}
            title={t("cliTools.mitmTitle")}
            subtitle={t("cliTools.sections.mitmSub", { count: searchedMitm.length })}
          />
          <PageNote icon="block">{t("cliTools.sections.mitmNote")}</PageNote>
          {renderItems(searchedMitm)}
        </div>
      ) : null}

      <ConfirmDialog
        open={!!resetTarget}
        onClose={() => setResetTarget(null)}
        onConfirm={() => void confirmReset()}
        title={t("cliTools.resetConfirmTitle", { name: resetTarget?.tool.name ?? "" })}
        message={t("cliTools.resetConfirmMessage", { name: resetTarget?.tool.name ?? "", path: resetTarget?.configPath || "—" })}
        confirmText={t("cliTools.menu.reset")}
        cancelText={t("common.cancel")}
        variant="danger"
      />
    </section>
  );
}
