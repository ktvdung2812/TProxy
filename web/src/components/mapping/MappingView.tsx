import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useLocation, useNavigate } from "react-router-dom";
import type { ApiKeyOption } from "../cli-tools/ApiKeySelect";
import { useChatModels } from "../chat/useChatModels";
import { Button, ConfirmDialog, Input, Select } from "../ui";
import {
  FilterTabs,
  GuideToggle,
  LegendItem,
  PageGuide,
  PageStats,
  SearchField,
  Toolbar,
  ToolbarEnd,
  ToolbarRow,
  ToolButton,
  isDialogOpen,
  usePageGuide,
  type PageStat,
} from "../page-kit";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";
import type { ModelRecord, RouteRecord } from "../models/types";
import type { CursorCatalogModel } from "./api";
import { MappingCodePanel } from "./MappingCodePanel";
import { BulkTargetModal, PairBulkBar, SaveBar } from "./MappingDock";
import { CursorCatalogPanel, ProtocolReference, ResolvePanel } from "./MappingSidePanels";
import { ModelTargetCombobox } from "./ModelTargetCombobox";
import { PairEditor, type PairHandlers, type PairRow } from "./PairEditor";
import { PlaceholderPanel, TierEditor, tierDefs } from "./TierEditor";
import { useMappingTargetOptions } from "./modelOptions";
import { rowState, sectionOf, useMappingEditor, type MappingSection, type PairSection } from "./useMappingEditor";
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

type ConfirmState = {
  title: string;
  message: string;
  confirmText: string;
  danger?: boolean;
  onConfirm: () => void;
};

const TAB_ICON: Record<MappingClientTab, string> = { claude: "smart_toy", codex: "terminal", cursor: "ads_click", models: "alt_route" };
const SECTIONS: MappingSection[] = ["tiers", "cursor", "models"];
const SECTION_TAB: Record<MappingSection, MappingClientTab> = { tiers: "claude", cursor: "cursor", models: "models" };

function catalogLabel(model: CursorCatalogModel) {
  return model.name && model.name !== model.id ? `${model.name} (${model.id})` : model.id;
}

export function MappingView({ secret, apiKeys, models, combos, routesByModel, providers, credentials, onError, onNotice }: Props) {
  const location = useLocation();
  const navigate = useNavigate();
  const { t } = useTranslation();
  const { copy } = useCopyToClipboard();
  const guide = usePageGuide("mapping");
  const editor = useMappingEditor({ secret, onError, onNotice });
  const activeTab = useMemo(() => parseMappingTab(location.hash), [location.hash]);
  const section = sectionOf(activeTab);
  const pairSection: PairSection | null = section === "tiers" ? null : section;
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<string[]>([]);
  const [bulkTargetOpen, setBulkTargetOpen] = useState(false);
  const [confirm, setConfirm] = useState<ConfirmState | null>(null);

  const setActiveTab = useCallback(
    (tab: MappingClientTab) => {
      navigate({ pathname: "/mapping", hash: tab }, { replace: true });
    },
    [navigate],
  );

  useEffect(() => {
    if (!location.hash) {
      navigate({ pathname: "/mapping", hash: "claude" }, { replace: true });
    }
  }, [location.hash, navigate]);

  // Search and selection belong to the list on screen.
  useEffect(() => {
    setSearch("");
    setSelected([]);
  }, [activeTab]);

  const chatSnapshot = useMemo(
    () => ({
      providers: providers.map((provider) => ({ ID: provider.ID, Name: provider.Name || provider.ID, Enabled: provider.Enabled !== false })),
      credentials,
      models: models.map((model) => ({ ID: model.ID, DisplayName: model.DisplayName, Enabled: model.Enabled, Capabilities: model.Capabilities })),
      combos: combos.map((combo) => ({ id: combo.id, display_name: combo.display_name || combo.id, enabled: combo.enabled !== false, capabilities: [] })),
    }),
    [providers, credentials, models, combos],
  );
  const { models: discoveredModels } = useChatModels(secret, chatSnapshot);
  const modelOptions = useMappingTargetOptions(models, combos, routesByModel, discoveredModels);
  const formatTarget = useCallback((target: string) => formatMappingTargetLabel(target, models, routesByModel), [models, routesByModel]);

  const copyValue = (text: string, label: string) => {
    copy(text, label);
    onNotice(t("mapping.copiedValue", { value: label }));
  };

  /* ---------- rows ---------- */
  const cursorCatalog = useMemo((): CursorCatalogModel[] => {
    const catalog = editor.cursorData?.cursor_models || [];
    if (catalog.length > 0) return catalog;
    // Fallback labels for mapped sources if catalog is empty.
    return Object.keys(editor.cursor.overrides).sort().map((id) => ({ id, name: id }));
  }, [editor.cursorData?.cursor_models, editor.cursor.overrides]);

  const availableCursorSources = useMemo(() => {
    const mapped = new Set(Object.keys(editor.cursor.overrides).map((id) => id.toLowerCase()));
    return cursorCatalog.filter((model) => !mapped.has(model.id.toLowerCase()));
  }, [cursorCatalog, editor.cursor.overrides]);

  const cursorRows = useMemo<PairRow[]>(() => {
    const byId = new Map(cursorCatalog.map((model) => [model.id.toLowerCase(), model]));
    return Object.entries(editor.cursor.overrides)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([source, target]) => ({
        source,
        label: byId.get(source.toLowerCase())?.name || undefined,
        target,
        routed: editor.cursorData?.effective?.[source],
        state: rowState(editor.cursor.saved, source, target),
      }));
  }, [editor.cursor.overrides, editor.cursor.saved, editor.cursorData?.effective, cursorCatalog]);

  const modelRows = useMemo<PairRow[]>(() => {
    const effective = new Map((editor.modelData?.rows || []).map((row) => [row.source, row.effective || row.target]));
    return Object.entries(editor.models.overrides)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([source, target]) => ({
        source,
        target,
        routed: effective.get(source) || editor.models.saved[source],
        state: rowState(editor.models.saved, source, target),
      }));
  }, [editor.models.overrides, editor.models.saved, editor.modelData?.rows]);

  const allRows = pairSection === "cursor" ? cursorRows : pairSection === "models" ? modelRows : [];
  const query = search.trim().toLowerCase();
  const visibleRows = query
    ? allRows.filter((row) => [row.source, row.label, row.target].some((value) => value?.toLowerCase().includes(query)))
    : allRows;
  const visibleIds = visibleRows.map((row) => row.source);
  const allVisibleSelected = visibleIds.length > 0 && visibleIds.every((id) => selected.includes(id));

  // Drop selections whose rows disappeared (removed, discarded, reloaded).
  useEffect(() => {
    setSelected((current) => {
      const next = current.filter((id) => allRows.some((row) => row.source === id));
      return next.length === current.length ? current : next;
    });
  }, [allRows]);

  /* ---------- unsaved state ---------- */
  const sectionLabel = (value: MappingSection) =>
    value === "tiers" ? t("mapping.section.tiers") : value === "cursor" ? t("mapping.section.cursor") : t("mapping.section.models");
  const totalDirty = editor.dirty.tiers + editor.dirty.cursor + editor.dirty.models;

  useEffect(() => {
    if (totalDirty === 0) return;
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [totalDirty]);

  const runSave = async (target: MappingSection) => {
    if (await editor.save(target)) setSelected([]);
  };

  const requestSave = (target: MappingSection = section) => {
    if (editor.saving || editor.dirty[target] === 0) return;
    const removed = target === "tiers" ? [] : editor.removed[target];
    if (removed.length === 0) {
      void runSave(target);
      return;
    }
    setConfirm({
      title: t("mapping.confirm.removeTitle", { count: removed.length }),
      message: t("mapping.confirm.removeMessage", { count: removed.length, list: removed.slice(0, 6).join(", ") + (removed.length > 6 ? "…" : "") }),
      confirmText: t("mapping.confirm.removeConfirm", { count: removed.length }),
      danger: true,
      onConfirm: () => {
        setConfirm(null);
        void runSave(target);
      },
    });
  };

  const requestDiscard = () => {
    setConfirm({
      title: t("mapping.confirm.discardTitle"),
      message: t("mapping.confirm.discardMessage", { count: editor.dirty[section], section: sectionLabel(section) }),
      confirmText: t("mapping.save.discard"),
      danger: true,
      onConfirm: () => {
        setConfirm(null);
        editor.discard(section);
      },
    });
  };

  const requestReload = () => {
    if (totalDirty === 0) {
      void editor.load();
      return;
    }
    setConfirm({
      title: t("mapping.confirm.reloadTitle"),
      message: t("mapping.confirm.reloadMessage", { count: totalDirty }),
      confirmText: t("mapping.confirm.reloadConfirm"),
      danger: true,
      onConfirm: () => {
        setConfirm(null);
        void editor.load();
      },
    });
  };

  // Cmd/Ctrl+S saves the editor on screen.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== "s" || isDialogOpen()) return;
      event.preventDefault();
      requestSave();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  });

  /* ---------- pair handlers ---------- */
  const pairHandlers = (target: PairSection): PairHandlers => ({
    setTarget: (source, value) => editor.setPairTarget(target, [source], value),
    remove: (source) => editor.removePairs(target, [source]),
    revert: (source) => editor.revertPair(target, source),
    select: (source, select) => setSelected((current) => (select ? [...new Set([...current, source])] : current.filter((id) => id !== source))),
    copy: copyValue,
    test: target === "models" ? (source) => void editor.resolve.run(source) : undefined,
  });
  const selectVisible = (select: boolean) =>
    setSelected((current) => (select ? [...new Set([...current, ...visibleIds])] : current.filter((id) => !visibleIds.includes(id))));

  /* ---------- stats ---------- */
  const overriddenTiers = (ids: string[]) => ids.filter((tier) => editor.tiers.saved[tier as keyof typeof editor.tiers.saved]).length;
  const claudeTiers = tierDefs("claude", t).map((tier) => tier.id);
  const codexTiers = tierDefs("codex", t).map((tier) => tier.id);
  const loadingValue = editor.loading && !editor.data;
  const firstDirty = SECTIONS.find((value) => editor.dirty[value] > 0);
  const catalogSource = editor.cursorData?.catalog_source;
  const stats: PageStat[] = [
    {
      key: "claude",
      icon: TAB_ICON.claude,
      label: t("mapping.tabs.claude"),
      value: loadingValue ? "—" : `${overriddenTiers(claudeTiers)}/${claudeTiers.length}`,
      hint: t("mapping.stats.tiersHint"),
      active: activeTab === "claude",
      onClick: () => setActiveTab("claude"),
    },
    {
      key: "codex",
      icon: TAB_ICON.codex,
      label: t("mapping.tabs.codex"),
      value: loadingValue ? "—" : `${overriddenTiers(codexTiers)}/${codexTiers.length}`,
      hint: t("mapping.stats.codexHint"),
      active: activeTab === "codex",
      onClick: () => setActiveTab("codex"),
    },
    {
      key: "cursor",
      icon: TAB_ICON.cursor,
      label: t("mapping.tabs.cursor"),
      value: loadingValue ? "—" : Object.keys(editor.cursor.saved).length,
      hint:
        catalogSource && catalogSource !== "discovery"
          ? t("mapping.stats.cursorHintStatic", { count: cursorCatalog.length })
          : t("mapping.stats.cursorHint", { count: cursorCatalog.length }),
      muted: Object.keys(editor.cursor.saved).length === 0,
      active: activeTab === "cursor",
      onClick: () => setActiveTab("cursor"),
    },
    {
      key: "models",
      icon: TAB_ICON.models,
      label: t("mapping.tabs.models"),
      value: loadingValue ? "—" : Object.keys(editor.models.saved).length,
      hint: t("mapping.stats.modelsHint"),
      muted: Object.keys(editor.models.saved).length === 0,
      active: activeTab === "models",
      onClick: () => setActiveTab("models"),
    },
    {
      key: "unsaved",
      icon: "edit_note",
      label: t("mapping.stats.unsaved"),
      value: totalDirty,
      tone: totalDirty > 0 ? "warning" : "neutral",
      muted: totalDirty === 0,
      hint: firstDirty
        ? SECTIONS.filter((value) => editor.dirty[value] > 0).map((value) => `${sectionLabel(value)} (${editor.dirty[value]})`).join(" · ")
        : t("mapping.stats.allSaved"),
      ...(firstDirty && sectionOf(activeTab) !== firstDirty ? { onClick: () => setActiveTab(SECTION_TAB[firstDirty]) } : {}),
    },
  ];

  const dirtyLabel = (tab: MappingClientTab) => {
    const count = editor.dirty[sectionOf(tab)];
    return (
      <>
        {t(`mapping.tabs.${tab}`)}
        {count > 0 ? <span className="mp-dirty-dot" aria-label={t("mapping.save.unsaved", { count })} /> : null}
      </>
    );
  };
  const tabCount: Record<MappingClientTab, number> = {
    claude: claudeTiers.length,
    codex: codexTiers.length,
    cursor: cursorRows.length,
    models: modelRows.length,
  };

  /* ---------- add rows ---------- */
  const cursorAdd = (
    <>
      <div className="mp-add-fields">
        <div className="mp-field">
          <span>{t("mapping.cursorModelAvailable", { count: availableCursorSources.length })}</span>
          <Select value={editor.cursor.addSource} onChange={(event) => editor.cursor.setAddSource(event.target.value)} aria-label={t("mapping.columns.cursorModel")}>
            <option value="">{cursorCatalog.length === 0 ? t("mapping.noCursorModels") : t("mapping.selectCursorModel")}</option>
            {availableCursorSources.map((model) => (
              <option key={model.id} value={model.id}>{catalogLabel(model)}</option>
            ))}
          </Select>
        </div>
        <span className="mp-row-arrow material-symbols-outlined" aria-hidden>arrow_forward</span>
        <div className="mp-field">
          <span>{t("mapping.proxyModel")}</span>
          <ModelTargetCombobox
            aria-label={t("mapping.proxyModel")}
            value={editor.cursor.addTarget}
            placeholder={t("mapping.virtualModelOrProvider")}
            options={modelOptions}
            onChange={editor.cursor.setAddTarget}
          />
        </div>
        <Button variant="secondary" size="sm" icon="add" disabled={!editor.cursor.addSource || !editor.cursor.addTarget.trim()} onClick={() => editor.cursor.add()}>
          {t("mapping.add")}
        </Button>
      </div>
      {cursorCatalog.length > 0 && availableCursorSources.length === 0 ? <p className="mp-add-note">{t("mapping.allCursorMapped")}</p> : null}
    </>
  );

  const modelAdd = (
    <div className="mp-add-fields">
      <div className="mp-field">
        <span>{t("mapping.columns.clientModel")}</span>
        <Input
          value={editor.models.addSource}
          onChange={(event) => editor.models.setAddSource(event.target.value)}
          placeholder="gpt-5.6-sol"
          aria-label={t("mapping.columns.clientModel")}
          autoComplete="off"
          spellCheck={false}
        />
      </div>
      <span className="mp-row-arrow material-symbols-outlined" aria-hidden>arrow_forward</span>
      <div className="mp-field">
        <span>{t("mapping.columns.target")}</span>
        <ModelTargetCombobox
          aria-label={t("mapping.columns.target")}
          value={editor.models.addTarget}
          placeholder={t("mapping.modelTargetPlaceholder")}
          options={modelOptions}
          onChange={editor.models.setAddTarget}
        />
      </div>
      <Button variant="secondary" size="sm" icon="add" disabled={!editor.models.addSource.trim() || !editor.models.addTarget.trim()} onClick={() => editor.models.add()}>
        {t("mapping.add")}
      </Button>
    </div>
  );

  const contentMapping =
    section === "cursor" ? editor.cursorData?.content_mapping : section === "models" ? editor.modelData?.content_mapping : editor.data?.content_mapping;
  const otherDirty = SECTIONS.filter((value) => value !== section && editor.dirty[value] > 0).map((value) => `${sectionLabel(value)} (${editor.dirty[value]})`);
  const firstLoad = editor.loading && !editor.data && !editor.cursorData && !editor.modelData;

  return (
    <section className="cp-page mp-page">
      <PageStats label={t("mapping.statsLabel")} items={stats} />

      <Toolbar>
        <ToolbarRow>
          <FilterTabs
            label={t("mapping.tabsLabel")}
            value={activeTab}
            onChange={setActiveTab}
            items={(["claude", "codex", "cursor", "models"] as const).map((tab) => ({
              value: tab,
              icon: TAB_ICON[tab],
              label: dirtyLabel(tab),
              count: tabCount[tab],
              title: editor.dirty[sectionOf(tab)] > 0 ? t("mapping.save.unsaved", { count: editor.dirty[sectionOf(tab)] }) : t(`mapping.tabHint.${tab}`),
            }))}
          />
          <ToolbarEnd>
            <ToolButton icon="refresh" spinning={editor.loading} disabled={editor.loading} onClick={requestReload} title={t("mapping.reloadHint")}>
              {t("mapping.reload")}
            </ToolButton>
            <Button
              variant="primary"
              size="sm"
              icon="save"
              loading={editor.saving}
              disabled={editor.dirty[section] === 0}
              onClick={() => requestSave()}
              title={t("mapping.save.shortcut")}
            >
              {t("mapping.save.save")}
            </Button>
          </ToolbarEnd>
        </ToolbarRow>
        <ToolbarRow>
          <SearchField value={search} onChange={setSearch} placeholder={t(`mapping.searchPlaceholder.${section}`)} />
          <ToolbarEnd>
            <GuideToggle open={guide.open} onToggle={guide.toggle} />
          </ToolbarEnd>
        </ToolbarRow>
      </Toolbar>

      <PageGuide
        open={guide.open}
        onDismiss={guide.dismiss}
        title={t("mapping.guide.title")}
        steps={[
          { icon: "swap_horiz", text: t("mapping.guide.what") },
          { icon: "bolt", text: t("mapping.guide.fast") },
          { icon: "code", text: t("mapping.guide.client") },
        ]}
        legend={
          <>
            <LegendItem tone="warning">{t("mapping.status.changed")}</LegendItem>
            <LegendItem tone="info">{t("mapping.status.new")}</LegendItem>
            <LegendItem tone="primary">{t("mapping.status.override")}</LegendItem>
            <LegendItem tone="neutral">{t("mapping.status.default")}</LegendItem>
          </>
        }
      />

      {firstLoad ? (
        <div className="mp-layout" aria-label={t("mapping.loading")}>
          {[0, 1].map((index) => (
            <div key={index} className="cp-panel mp-panel mp-skeleton">
              <div className="cp-skeleton"><span /><span /><span /><span /></div>
            </div>
          ))}
        </div>
      ) : (
        <div className="mp-layout">
          {section === "tiers" ? (
            <>
              <TierEditor
                client={activeTab === "codex" ? "codex" : "claude"}
                editor={editor}
                options={modelOptions}
                search={search}
                formatTarget={formatTarget}
                onCopy={copyValue}
                onClearSearch={() => setSearch("")}
              />
              <PlaceholderPanel client={activeTab === "codex" ? "codex" : "claude"} data={editor.data} formatTarget={formatTarget} />
            </>
          ) : pairSection === "cursor" ? (
            <>
              <PairEditor
                section="cursor"
                icon="ads_click"
                title={t("mapping.cursorProxyModelMap")}
                subtitle={t("mapping.cursorPanelSubtitle")}
                rows={visibleRows}
                total={cursorRows.length}
                removedCount={editor.removed.cursor.length}
                selected={selected}
                onSelectAll={selectVisible}
                options={modelOptions}
                handlers={pairHandlers("cursor")}
                addRow={cursorAdd}
                emptyTitle={t("mapping.emptyCursorTitle")}
                emptyHint={t("mapping.emptyCursorHint")}
                hasSearch={Boolean(query)}
                onClearSearch={() => setSearch("")}
                formatTarget={formatTarget}
              />
              <CursorCatalogPanel
                data={editor.cursorData}
                catalogCount={cursorCatalog.length}
                unmappedCount={availableCursorSources.length}
                refreshing={editor.refreshingCursorCatalog}
                disabled={editor.loading}
                onDiscover={() => void editor.refreshCursorCatalog()}
              />
            </>
          ) : (
            <>
              <PairEditor
                section="models"
                icon="alt_route"
                title={t("mapping.globalModelMap")}
                subtitle={t("mapping.modelsPanelSubtitle")}
                rows={visibleRows}
                total={modelRows.length}
                removedCount={editor.removed.models.length}
                selected={selected}
                onSelectAll={selectVisible}
                options={modelOptions}
                handlers={pairHandlers("models")}
                addRow={modelAdd}
                emptyTitle={t("mapping.emptyModelsTitle")}
                emptyHint={t("mapping.emptyModelsHint")}
                hasSearch={Boolean(query)}
                onClearSearch={() => setSearch("")}
                formatTarget={formatTarget}
              />
              <ResolvePanel
                input={editor.resolve.input}
                onInput={editor.resolve.setInput}
                result={editor.resolve.result}
                running={editor.resolve.running}
                onRun={() => void editor.resolve.run()}
              />
            </>
          )}
        </div>
      )}

      {/* Global rewrites apply to every client, so there is no single client config to generate. */}
      {activeTab !== "models" ? (
        <MappingCodePanel
          client={activeTab}
          apiKeys={apiKeys}
          overrides={activeTab === "cursor" ? editor.cursor.overrides : editor.tiers.overrides}
          data={editor.data}
        />
      ) : null}

      <ProtocolReference mapping={contentMapping} />

      <div className="mp-dock">
        <SaveBar
          count={editor.dirty[section]}
          sectionLabel={sectionLabel(section)}
          otherDirty={otherDirty}
          saving={editor.saving}
          onDiscard={requestDiscard}
          onSave={() => requestSave()}
        />
        {pairSection ? (
          <PairBulkBar
            count={selected.length}
            visibleCount={visibleIds.length}
            allVisibleSelected={allVisibleSelected}
            onSelectAll={() => selectVisible(true)}
            onClear={() => setSelected([])}
            onSetTarget={() => setBulkTargetOpen(true)}
            onRemove={() => {
              editor.removePairs(pairSection, selected);
              setSelected([]);
            }}
          />
        ) : null}
      </div>

      <BulkTargetModal
        open={bulkTargetOpen}
        count={selected.length}
        options={modelOptions}
        onClose={() => setBulkTargetOpen(false)}
        onApply={(target) => {
          if (pairSection) editor.setPairTarget(pairSection, selected, target);
          setBulkTargetOpen(false);
        }}
      />
      <ConfirmDialog
        open={!!confirm}
        onClose={() => setConfirm(null)}
        onConfirm={() => confirm?.onConfirm()}
        title={confirm?.title || ""}
        message={confirm?.message}
        confirmText={confirm?.confirmText || t("mapping.confirm.ok")}
        cancelText={t("mapping.cancel")}
        variant={confirm?.danger ? "danger" : "primary"}
      />
    </section>
  );
}
