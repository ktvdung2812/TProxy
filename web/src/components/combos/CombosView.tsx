import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";
import type { ModelOption } from "../../lib/modelOptions";
import { Button, ConfirmDialog } from "../ui";
import {
  ActionMenu,
  BulkBar,
  BulkButton,
  EmptyPanel,
  FilterTabs,
  GuideToggle,
  LegendItem,
  PageGuide,
  PageStats,
  SearchField,
  SectionHead,
  Segmented,
  SelectField,
  ToneCounts,
  Toolbar,
  ToolbarEnd,
  ToolbarRow,
  usePageGuide,
  useStoredState,
} from "../page-kit";
import { deleteCombo, saveCombo } from "./api";
import { ComboCard, ComboRow, type ComboItemHandlers } from "./ComboItem";
import { ComboFormModal } from "./ComboFormModal";
import { CLIENT_SETUP_ROUTES, ComboPresets } from "./ComboPresets";
import { buildComboViews, HEALTH_RANK, type ComboClient, type ComboHealth, type ComboView } from "./comboHealth";
import type { ComboFormData, ComboModelOption, ComboPreset, ComboRecord } from "./types";
import { buildComboPresets, comboToForm, emptyComboForm, presetToForm, uniqueComboId } from "./utils";

type Props = {
  secret: string;
  combos: ComboRecord[];
  models: ComboModelOption[];
  modelOptions: ModelOption[];
  /** True while the first snapshot is still loading. */
  loading?: boolean;
  onMutated?: () => void;
  onNotice?: (message: string) => void;
  onError?: (message: string) => void;
};

type HealthFilter = "all" | ComboHealth;
type ClientFilter = "all" | ComboClient;
type SortMode = "default" | "attention" | "name" | "steps";
type ViewMode = "grid" | "list";

type ConfirmState = {
  title: string;
  message: string;
  confirmText: string;
  onConfirm: () => void;
};

const CLIENTS: ComboClient[] = ["claude-code", "cowork", "general"];
const CLIENT_ICONS: Record<ComboClient, string> = { "claude-code": "terminal", cowork: "groups", general: "api" };

function matchesQuery(query: string, ...parts: Array<string | undefined>) {
  const needle = query.trim().toLowerCase();
  if (!needle) return true;
  return parts.some((part) => part?.toLowerCase().includes(needle));
}

export function CombosView({
  secret,
  combos,
  models,
  modelOptions,
  loading = false,
  onMutated,
  onNotice,
  onError,
}: Props) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const guide = usePageGuide("combos");
  const { copied, copy } = useCopyToClipboard();
  const [showFormModal, setShowFormModal] = useState(false);
  const [editingCombo, setEditingCombo] = useState<ComboRecord | null>(null);
  const [formSeed, setFormSeed] = useState<ComboFormData | null>(null);
  const [saving, setSaving] = useState(false);
  const [confirmState, setConfirmState] = useState<ConfirmState | null>(null);
  const [search, setSearch] = useState("");
  const [clientFilter, setClientFilter] = useState<ClientFilter>("all");
  const [healthFilter, setHealthFilter] = useState<HealthFilter>("all");
  const [sortMode, setSortMode] = useStoredState<SortMode>("tproxy.combos.sort", "default", ["default", "attention", "name", "steps"]);
  const [viewMode, setViewMode] = useStoredState<ViewMode>("tproxy.combos.view", "grid", ["grid", "list"]);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [busyIds, setBusyIds] = useState<Set<string>>(new Set());
  const [bulkBusy, setBulkBusy] = useState(false);

  const existingIds = useMemo(() => combos.map((combo) => combo.id), [combos]);
  const presets = useMemo(() => buildComboPresets(models), [models]);
  const views = useMemo(() => buildComboViews(combos, models, modelOptions), [combos, models, modelOptions]);
  const disabledTargets = useMemo(
    () => new Set([...models.filter((model) => !model.enabled).map((model) => model.id), ...combos.filter((combo) => !combo.enabled).map((combo) => combo.id)]),
    [models, combos],
  );

  const counts = useMemo(() => {
    const result = { ready: 0, partial: 0, broken: 0, paused: 0 };
    for (const view of views) result[view.health] += 1;
    return result;
  }, [views]);

  const modelsInUse = useMemo(
    () => new Set(views.flatMap((view) => view.steps.map((step) => step.id))).size,
    [views],
  );

  const clientCounts = useMemo(() => {
    const result: Record<ComboClient, number> = { "claude-code": 0, cowork: 0, general: 0 };
    for (const view of views) result[view.client] += 1;
    return result;
  }, [views]);

  const visibleViews = useMemo(() => {
    const filtered = views.filter((view) => {
      if (healthFilter !== "all" && view.health !== healthFilter) return false;
      if (clientFilter !== "all" && view.client !== clientFilter) return false;
      return matchesQuery(search, view.name, view.combo.id, ...view.steps.flatMap((step) => [step.id, step.label]));
    });
    if (sortMode === "default") return filtered;
    return [...filtered].sort((left, right) => {
      if (sortMode === "attention") return HEALTH_RANK[left.health] - HEALTH_RANK[right.health] || left.name.localeCompare(right.name);
      if (sortMode === "steps") return right.steps.length - left.steps.length || left.name.localeCompare(right.name);
      return left.name.localeCompare(right.name);
    });
  }, [views, healthFilter, clientFilter, search, sortMode]);

  const selectedCombos = useMemo(() => combos.filter((combo) => selectedIds.has(combo.id)), [combos, selectedIds]);
  const allVisibleSelected = visibleViews.length > 0 && visibleViews.every((view) => selectedIds.has(view.combo.id));
  const partlySelected = !allVisibleSelected && visibleViews.some((view) => selectedIds.has(view.combo.id));

  const toggleSelection = (ids: string[], select: boolean) => {
    setSelectedIds((current) => {
      const next = new Set(current);
      for (const id of ids) {
        if (select) next.add(id);
        else next.delete(id);
      }
      return next;
    });
  };

  const markBusy = (id: string, busy: boolean) => {
    setBusyIds((current) => {
      const next = new Set(current);
      if (busy) next.add(id);
      else next.delete(id);
      return next;
    });
  };

  const comboName = (combo: ComboRecord) => combo.display_name || combo.id;

  const openForm = (seed: ComboFormData, editing: ComboRecord | null) => {
    setEditingCombo(editing);
    setFormSeed(seed);
    setShowFormModal(true);
  };
  const closeForm = () => {
    setShowFormModal(false);
    setEditingCombo(null);
    setFormSeed(null);
  };

  const openCreate = () => openForm(emptyComboForm(), null);
  const openEdit = (combo: ComboRecord) => openForm(comboToForm(combo), combo);
  const openDuplicate = (combo: ComboRecord) => {
    const form = comboToForm(combo);
    openForm(
      { ...form, id: uniqueComboId(`${combo.id}-copy`, existingIds), display_name: t("combos.copyName", { name: comboName(combo) }) },
      null,
    );
  };
  const openPreset = (preset: ComboPreset) => {
    const form = presetToForm(preset, existingIds);
    if (!form) {
      onError?.(t("combos.noPresetError"));
      return;
    }
    openForm(form, null);
  };

  const handleSave = async (form: ComboFormData) => {
    setSaving(true);
    try {
      await saveCombo(secret, form, !!editingCombo);
      onNotice?.(editingCombo
        ? t("combos.comboUpdated", { name: form.display_name || form.id })
        : t("combos.comboCreated", { name: form.display_name || form.id }));
      closeForm();
      onMutated?.();
    } catch (error) {
      onError?.(error instanceof Error ? error.message : t("combos.failedSaveCombo"));
    } finally {
      setSaving(false);
    }
  };

  // Toggling re-saves the combo exactly as the edit form would, with only `enabled` changed.
  const putEnabled = (combo: ComboRecord, enabled: boolean) => saveCombo(secret, { ...comboToForm(combo), enabled }, true);

  const setEnabled = async (combo: ComboRecord, enabled: boolean) => {
    markBusy(combo.id, true);
    try {
      await putEnabled(combo, enabled);
      onNotice?.(t(enabled ? "combos.notice.enabled" : "combos.notice.disabled", { name: comboName(combo) }));
      onMutated?.();
    } catch (error) {
      onError?.(error instanceof Error ? error.message : t("combos.failedSaveCombo"));
    } finally {
      markBusy(combo.id, false);
    }
  };

  const bulkSetEnabled = async (enabled: boolean) => {
    const targets = selectedCombos.filter((combo) => combo.enabled !== enabled);
    if (targets.length === 0) return;
    setBulkBusy(true);
    let failed = 0;
    for (const combo of targets) {
      try {
        await putEnabled(combo, enabled);
      } catch {
        failed += 1;
      }
    }
    setBulkBusy(false);
    onMutated?.();
    if (failed > 0) onError?.(t("combos.notice.bulkFailed", { count: failed }));
    else onNotice?.(t(enabled ? "combos.notice.bulkEnabled" : "combos.notice.bulkDisabled", { count: targets.length }));
  };

  const confirmDelete = (targets: ComboRecord[]) => {
    if (targets.length === 0) return;
    const single = targets.length === 1 ? targets[0] : null;
    setConfirmState({
      title: single ? t("combos.deleteCombo") : t("combos.deleteCombos", { count: targets.length }),
      message: single
        ? t("combos.deleteComboMessage", { name: comboName(single) })
        : t("combos.deleteCombosMessage", { count: targets.length }),
      confirmText: t("common.delete"),
      onConfirm: () => {
        void (async () => {
          setBulkBusy(targets.length > 1);
          let failed = 0;
          for (const combo of targets) {
            markBusy(combo.id, true);
            try {
              await deleteCombo(secret, combo.id);
            } catch (error) {
              failed += 1;
              if (single) onError?.(error instanceof Error ? error.message : t("combos.failedDeleteCombo"));
            } finally {
              markBusy(combo.id, false);
            }
          }
          setBulkBusy(false);
          toggleSelection(targets.map((combo) => combo.id), false);
          if (!single && failed > 0) onError?.(t("combos.notice.bulkFailed", { count: failed }));
          else if (failed === 0) {
            onNotice?.(single ? t("combos.comboDeleted", { name: comboName(single) }) : t("combos.notice.bulkDeleted", { count: targets.length }));
          }
          onMutated?.();
        })();
      },
    });
  };

  const handlers: ComboItemHandlers = {
    edit: openEdit,
    duplicate: openDuplicate,
    setEnabled: (combo, enabled) => void setEnabled(combo, enabled),
    select: (combo, select) => toggleSelection([combo.id], select),
    copyId: (combo) => {
      copy(combo.id, `combo-${combo.id}`);
      onNotice?.(t("combos.notice.copied", { id: combo.id }));
    },
    remove: (combo) => confirmDelete([combo]),
  };

  const hasFilters = healthFilter !== "all" || clientFilter !== "all" || search.trim() !== "";
  const clearFilters = () => {
    setSearch("");
    setHealthFilter("all");
    setClientFilter("all");
  };
  const filterTile = (filter: HealthFilter) => ({
    active: healthFilter === filter,
    onClick: () => setHealthFilter(healthFilter === filter && filter !== "all" ? "all" : filter),
  });

  if (combos.length === 0 && loading) {
    return (
      <section className="cp-page">
        <EmptyPanel loading title={t("combos.loading")} />
      </section>
    );
  }

  const enabledCount = combos.length - counts.paused;
  const presetTitle = (preset: ComboPreset) => t(`combos.presets.${preset.id}.title`, { defaultValue: preset.title });

  return (
    <section className="cp-page cb-page">
      <PageStats
        label={t("combos.stats.label")}
        items={[
          {
            key: "all",
            icon: "layers",
            label: t("combos.stats.combos"),
            value: combos.length,
            hint: t("combos.stats.combosHint", { enabled: enabledCount }),
            ...filterTile("all"),
          },
          {
            key: "ready",
            icon: "check_circle",
            tone: "success",
            label: t("combos.stats.ready"),
            value: counts.ready,
            hint: t("combos.stats.readyHint"),
            muted: counts.ready === 0,
            ...filterTile("ready"),
          },
          {
            key: "partial",
            icon: "alt_route",
            tone: "warning",
            label: t("combos.stats.partial"),
            value: counts.partial,
            hint: t("combos.stats.partialHint"),
            muted: counts.partial === 0,
            ...filterTile("partial"),
          },
          {
            key: "broken",
            icon: "link_off",
            tone: "danger",
            label: t("combos.stats.broken"),
            value: counts.broken,
            hint: t("combos.stats.brokenHint"),
            muted: counts.broken === 0,
            ...filterTile("broken"),
          },
          {
            key: "paused",
            icon: "power_settings_new",
            tone: "muted",
            label: t("combos.stats.paused"),
            value: counts.paused,
            hint: t("combos.stats.pausedHint"),
            ...filterTile("paused"),
          },
          {
            key: "models",
            icon: "hub",
            label: t("combos.stats.models"),
            value: modelsInUse,
            hint: t("combos.stats.modelsHint"),
            muted: modelsInUse === 0,
          },
        ]}
      />

      <Toolbar>
        <ToolbarRow>
          <FilterTabs<ClientFilter>
            label={t("combos.filterByClient")}
            value={clientFilter}
            onChange={(value) => setClientFilter(value === clientFilter ? "all" : value)}
            items={[
              { value: "all", icon: "apps", label: t("combos.allClients"), count: combos.length },
              ...CLIENTS.map((client) => ({
                value: client,
                icon: CLIENT_ICONS[client],
                label: t(`combos.client.${client}`),
                count: clientCounts[client],
                title: t(`combos.clientHint.${client}`),
              })),
            ]}
          />
          <ToolbarEnd>
            <Button variant="primary" size="sm" icon="add" onClick={openCreate}>
              {t("combos.createCombo")}
            </Button>
          </ToolbarEnd>
        </ToolbarRow>
        <ToolbarRow>
          <SearchField value={search} onChange={setSearch} placeholder={t("combos.search")} />
          <ToolbarEnd>
            <SelectField
              label={t("combos.sortBy")}
              value={sortMode}
              onChange={setSortMode}
              options={[
                { value: "default", label: t("combos.sort.default") },
                { value: "attention", label: t("combos.sort.attention") },
                { value: "name", label: t("combos.sort.name") },
                { value: "steps", label: t("combos.sort.steps") },
              ]}
            />
            <Segmented
              label={t("combos.viewMode")}
              value={viewMode}
              onChange={setViewMode}
              items={[
                { value: "grid", icon: "grid_view", label: t("combos.viewGrid") },
                { value: "list", icon: "view_list", label: t("combos.viewList") },
              ]}
            />
            <ActionMenu
              label={t("combos.quickActions")}
              text={t("combos.quickActions")}
              icon="auto_fix_high"
              items={[
                ...presets.map((preset) => ({
                  key: `preset-${preset.id}`,
                  icon: "auto_awesome",
                  label: t("combos.quick.fromPreset", { name: presetTitle(preset) }),
                  hint: preset.modelIds.length ? preset.modelIds.join(" → ") : t("combos.noVirtualModels"),
                  disabled: preset.modelIds.length === 0,
                  onSelect: () => openPreset(preset),
                })),
                ...CLIENTS.filter((client) => CLIENT_SETUP_ROUTES[client]).map((client) => ({
                  key: `setup-${client}`,
                  icon: CLIENT_ICONS[client],
                  label: client === "claude-code" ? t("combos.claudeCodeSetup") : t("combos.coworkSetup"),
                  hint: t(`combos.clientHint.${client}`),
                  onSelect: () => navigate(CLIENT_SETUP_ROUTES[client] || "/cli-tools"),
                })),
                {
                  key: "select-visible",
                  icon: "select_all",
                  label: t("combos.quick.selectVisible", { count: visibleViews.length }),
                  disabled: visibleViews.length === 0 || allVisibleSelected,
                  onSelect: () => toggleSelection(visibleViews.map((view) => view.combo.id), true),
                },
              ]}
            />
            <GuideToggle open={guide.open} onToggle={guide.toggle} />
          </ToolbarEnd>
        </ToolbarRow>
      </Toolbar>

      <PageGuide
        open={guide.open}
        onDismiss={guide.dismiss}
        title={t("combos.guide.title")}
        steps={[
          { icon: "layers", text: t("combos.guide.what") },
          { icon: "terminal", text: t("combos.guide.clients") },
          { icon: "bolt", text: t("combos.guide.fast") },
        ]}
        legend={
          <>
            <LegendItem tone="success">{t("combos.health.ready")}</LegendItem>
            <LegendItem tone="warning">{t("combos.guide.legendPartial")}</LegendItem>
            <LegendItem tone="danger">{t("combos.guide.legendBroken")}</LegendItem>
            <LegendItem tone="muted">{t("combos.health.paused")}</LegendItem>
          </>
        }
      />

      <section className="cb-group" aria-label={t("combos.listTitle")}>
        <SectionHead
          leading={
            <>
              <input
                type="checkbox"
                className="cp-check"
                checked={allVisibleSelected}
                disabled={visibleViews.length === 0}
                ref={(element) => {
                  if (element) element.indeterminate = partlySelected;
                }}
                onChange={(event) => toggleSelection(visibleViews.map((view) => view.combo.id), event.target.checked)}
                aria-label={t("combos.selectAllVisible")}
              />
              <span className="cp-section-icon material-symbols-outlined" aria-hidden>layers</span>
            </>
          }
          title={t("combos.listTitle")}
          subtitle={
            visibleViews.length === combos.length
              ? t("combos.summary", { count: combos.length, enabled: enabledCount })
              : t("combos.summaryFiltered", { shown: visibleViews.length, count: combos.length })
          }
          meta={
            <ToneCounts
              label={t("combos.stats.label")}
              items={[
                { tone: "success", count: counts.ready, label: t("combos.health.ready") },
                { tone: "warning", count: counts.partial, label: t("combos.health.partial") },
                { tone: "danger", count: counts.broken, label: t("combos.health.broken") },
              ]}
            />
          }
        />

        {combos.length === 0 ? (
          <EmptyPanel
            icon="layers"
            title={t("combos.noCombosConfigured")}
            description={t("combos.noCombosHint")}
            action={
              <Button variant="primary" icon="add" onClick={openCreate}>
                {t("combos.createCombo")}
              </Button>
            }
          />
        ) : visibleViews.length === 0 ? (
          <EmptyPanel
            icon="filter_alt_off"
            title={t("combos.empty.noMatch")}
            description={t("combos.empty.noMatchHint")}
            action={hasFilters ? <Button variant="secondary" icon="filter_alt_off" onClick={clearFilters}>{t("combos.clearFilters")}</Button> : null}
          />
        ) : viewMode === "grid" ? (
          <div className="cp-grid cb-grid">
            {visibleViews.map((view: ComboView) => (
              <ComboCard
                key={view.combo.id}
                view={view}
                handlers={handlers}
                selected={selectedIds.has(view.combo.id)}
                busy={busyIds.has(view.combo.id) || bulkBusy}
                copied={copied === `combo-${view.combo.id}`}
              />
            ))}
          </div>
        ) : (
          <div className="cp-list cb-list">
            <div className="cp-list-head" aria-hidden>
              <span />
              <span>{t("combos.columns.combo")}</span>
              <span>{t("combos.columns.status")}</span>
              <span>{t("combos.columns.client")}</span>
              <span>{t("combos.columns.chain")}</span>
              <span>{t("combos.columns.enabled")}</span>
              <span />
            </div>
            {visibleViews.map((view) => (
              <ComboRow
                key={view.combo.id}
                view={view}
                handlers={handlers}
                selected={selectedIds.has(view.combo.id)}
                busy={busyIds.has(view.combo.id) || bulkBusy}
                copied={copied === `combo-${view.combo.id}`}
              />
            ))}
          </div>
        )}
      </section>

      <ComboPresets presets={presets} onUse={openPreset} />

      <BulkBar
        count={selectedCombos.length}
        busy={bulkBusy}
        onClear={() => setSelectedIds(new Set())}
        selectAll={
          allVisibleSelected
            ? null
            : { label: t("combos.quick.selectVisible", { count: visibleViews.length }), onSelect: () => toggleSelection(visibleViews.map((view) => view.combo.id), true) }
        }
      >
        <BulkButton icon="toggle_on" disabled={bulkBusy} onClick={() => void bulkSetEnabled(true)}>
          {t("combos.bulk.enable")}
        </BulkButton>
        <BulkButton icon="toggle_off" disabled={bulkBusy} onClick={() => void bulkSetEnabled(false)}>
          {t("combos.bulk.disable")}
        </BulkButton>
        <BulkButton icon="delete" tone="danger" disabled={bulkBusy} onClick={() => confirmDelete(selectedCombos)}>
          {t("combos.bulk.delete")}
        </BulkButton>
      </BulkBar>

      <ComboFormModal
        open={showFormModal}
        editing={!!editingCombo}
        initial={formSeed}
        modelOptions={editingCombo ? modelOptions.filter((option) => option.value !== editingCombo.id) : modelOptions}
        existingIds={existingIds}
        disabledTargets={disabledTargets}
        saving={saving}
        onClose={closeForm}
        onSubmit={(form) => void handleSave(form)}
      />

      <ConfirmDialog
        open={confirmState !== null}
        title={confirmState?.title || ""}
        message={confirmState?.message || ""}
        variant="danger"
        confirmText={confirmState?.confirmText || t("common.confirm")}
        cancelText={t("common.cancel")}
        onClose={() => setConfirmState(null)}
        onConfirm={() => {
          confirmState?.onConfirm();
          setConfirmState(null);
        }}
      />
    </section>
  );
}
