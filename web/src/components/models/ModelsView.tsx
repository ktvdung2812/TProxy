import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";
import { ProviderLogo } from "../providers/ProviderLogo";
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
import { deleteModel, saveModel } from "./api";
import { DiscoveredModels } from "./DiscoveredModels";
import { ModelCard, ModelRow, type ModelItemHandlers } from "./ModelItem";
import { ModelFormModal } from "./ModelFormModal";
import { ModelSelectModal } from "./ModelSelectModal";
import { buildModelView, failoverKeyMap, HEALTH_RANK, type ModelHealth, type ModelView } from "./modelHealth";
import { ProviderPriorityModal } from "./ProviderPriorityModal";
import type { ModelFormData, ModelRecord, ProviderOption, RouteFormData, RouteRecord } from "./types";
import { useFailoverOverview } from "./useProviderFailover";
import { useProviderModelDiscovery } from "./useProviderModelDiscovery";
import {
  buildModelFormFromSelection,
  collectUnmappedDiscoveredModels,
  modelToForm,
  providerDisplayLabel,
  type DiscoveredPpmEntry,
} from "./utils";

type Props = {
  secret: string;
  models: ModelRecord[];
  routesByModel: Record<string, RouteRecord[]>;
  providers: ProviderOption[];
  credentialCounts: Record<string, number>;
  /** True while the first snapshot is still loading. */
  loading?: boolean;
  onMutated?: () => void;
  onNotice?: (message: string) => void;
  onError?: (message: string) => void;
};

type HealthFilter = "all" | ModelHealth | "discovered";
type SortMode = "default" | "name" | "attention" | "routes";
type ViewMode = "grid" | "list";

type ConfirmState = {
  title: string;
  message: string;
  confirmText: string;
  onConfirm: () => void;
};

function matchesQuery(query: string, ...parts: Array<string | undefined>) {
  const needle = query.trim().toLowerCase();
  if (!needle) return true;
  return parts.some((part) => part?.toLowerCase().includes(needle));
}

export function ModelsView({
  secret,
  models,
  routesByModel,
  providers,
  credentialCounts,
  loading = false,
  onMutated,
  onNotice,
  onError,
}: Props) {
  const { t } = useTranslation();
  const guide = usePageGuide("models");
  const { copy } = useCopyToClipboard();
  const [showSelectModal, setShowSelectModal] = useState(false);
  const [priorityModel, setPriorityModel] = useState<ModelRecord | null>(null);
  const [detailsModel, setDetailsModel] = useState<ModelRecord | null>(null);
  const [saving, setSaving] = useState(false);
  const [confirmState, setConfirmState] = useState<ConfirmState | null>(null);
  const [search, setSearch] = useState("");
  const [providerFilter, setProviderFilter] = useState("all");
  const [healthFilter, setHealthFilter] = useState<HealthFilter>("all");
  const [sortMode, setSortMode] = useStoredState<SortMode>("tproxy.models.sort", "default", ["default", "name", "attention", "routes"]);
  const [viewMode, setViewMode] = useStoredState<ViewMode>("tproxy.models.view", "grid", ["grid", "list"]);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [busyIds, setBusyIds] = useState<Set<string>>(new Set());
  const [bulkBusy, setBulkBusy] = useState(false);
  const [addingKey, setAddingKey] = useState<string | null>(null);

  const discovery = useProviderModelDiscovery(secret, providers, true);
  const { modelsByProvider } = discovery;
  const { failoverStates, reloadFailover, resetFailover } = useFailoverOverview(secret);
  const failoverByKey = useMemo(() => failoverKeyMap(failoverStates), [failoverStates]);

  const existingIds = useMemo(() => models.map((model) => model.ID), [models]);

  const views = useMemo(
    () =>
      models.map((model) =>
        buildModelView(model, routesByModel[model.ID] || [], providers, credentialCounts, modelsByProvider, discovery.ready, failoverByKey),
      ),
    [models, routesByModel, providers, credentialCounts, modelsByProvider, discovery.ready, failoverByKey],
  );

  const discoveredEntries = useMemo(
    () => (discovery.ready ? collectUnmappedDiscoveredModels(models, routesByModel, providers, modelsByProvider) : []),
    [discovery.ready, models, routesByModel, providers, modelsByProvider],
  );

  const counts = useMemo(() => {
    const result = { serving: 0, degraded: 0, attention: 0, disabled: 0, aliases: 0, routesOff: 0 };
    for (const view of views) {
      result[view.health] += 1;
      result.aliases += (view.model.Aliases || []).length;
      result.routesOff += view.offCount;
    }
    return result;
  }, [views]);

  // Provider tabs list every provider that serves (or could serve) something here.
  const providerTabs = useMemo(() => {
    const tally = new Map<string, number>();
    for (const view of views) {
      for (const id of new Set(view.steps.map((step) => step.providerId))) tally.set(id, (tally.get(id) || 0) + 1);
    }
    for (const entry of discoveredEntries) {
      for (const id of entry.providerIds) if (!tally.has(id)) tally.set(id, 0);
    }
    return providers.filter((provider) => tally.has(provider.id)).map((provider) => ({ provider, count: tally.get(provider.id) || 0 }));
  }, [views, discoveredEntries, providers]);

  const visibleViews = useMemo(() => {
    const filtered = views.filter((view) => {
      if (healthFilter === "discovered") return false;
      if (healthFilter !== "all" && view.health !== healthFilter) return false;
      if (providerFilter !== "all" && !view.steps.some((step) => step.providerId === providerFilter)) return false;
      return matchesQuery(
        search,
        view.model.DisplayName,
        view.model.ID,
        ...(view.model.Aliases || []),
        ...(view.model.Capabilities || []),
        ...view.steps.flatMap((step) => [step.providerLabel, step.upstreamModel]),
      );
    });
    if (sortMode === "default") return filtered;
    const name = (view: ModelView) => view.model.DisplayName || view.model.ID;
    return [...filtered].sort((left, right) => {
      if (sortMode === "attention") return HEALTH_RANK[left.health] - HEALTH_RANK[right.health] || name(left).localeCompare(name(right));
      if (sortMode === "routes") return right.usableCount - left.usableCount || name(left).localeCompare(name(right));
      return name(left).localeCompare(name(right));
    });
  }, [views, healthFilter, providerFilter, search, sortMode]);

  const showDiscovered = healthFilter === "all" || healthFilter === "discovered";
  const visibleDiscovered = useMemo(
    () =>
      discoveredEntries.filter(
        (entry) =>
          (providerFilter === "all" || entry.providerIds.includes(providerFilter)) &&
          matchesQuery(
            search,
            entry.name,
            entry.upstreamModel,
            ...entry.capabilities,
            ...entry.providerIds.map((id) => providerDisplayLabel(providers, id)),
          ),
      ),
    [discoveredEntries, providers, search, providerFilter],
  );

  const discoveryErrors = useMemo(
    () =>
      Object.entries(discovery.errorsByProvider)
        .filter(([providerId]) => providerFilter === "all" || providerId === providerFilter)
        .map(([providerId, message]) => ({ providerId, message })),
    [discovery.errorsByProvider, providerFilter],
  );

  const pausedStates = useMemo(() => failoverStates.filter((state) => state.state === "open"), [failoverStates]);

  const selectedModels = useMemo(() => models.filter((model) => selectedIds.has(model.ID)), [models, selectedIds]);
  const allVisibleSelected = visibleViews.length > 0 && visibleViews.every((view) => selectedIds.has(view.model.ID));
  const partlySelected = !allVisibleSelected && visibleViews.some((view) => selectedIds.has(view.model.ID));

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

  const modelLabel = (model: ModelRecord) => model.DisplayName || model.ID;

  // Routes passed to the priority editor must stay referentially stable: the
  // editor resets its working copy whenever they change.
  const priorityRoutes = useMemo<RouteFormData[]>(
    () => (priorityModel ? modelToForm(priorityModel, routesByModel[priorityModel.ID] || []).routes : []),
    [priorityModel, routesByModel],
  );
  const detailsForm = useMemo(
    () => (detailsModel ? modelToForm(detailsModel, routesByModel[detailsModel.ID] || []) : null),
    [detailsModel, routesByModel],
  );

  const handleCreateModel = async (form: ModelFormData) => {
    setSaving(true);
    try {
      await saveModel(secret, form, false);
      onNotice?.(t("models.notice.created", { name: form.display_name || form.id }));
      setShowSelectModal(false);
      const createdModel: ModelRecord = {
        ID: form.id.trim(),
        DisplayName: form.display_name.trim() || form.id.trim(),
        Aliases: [],
        Enabled: form.enabled,
        RewriteResponseModel: form.rewrite_response_model,
        Capabilities: form.capabilities,
      };
      onMutated?.();
      // Open the chain right away so the operator can review the fallback order.
      setPriorityModel(createdModel);
    } catch (error) {
      onError?.(error instanceof Error ? error.message : t("models.failedCreate"));
    } finally {
      setSaving(false);
    }
  };

  const handleAddDiscovered = async (entry: DiscoveredPpmEntry) => {
    const preferredProvider =
      [...entry.providerIds].sort((left, right) => (credentialCounts[right] ?? 0) - (credentialCounts[left] ?? 0))[0] ||
      entry.providerIds[0];
    if (!preferredProvider) return;
    const form = buildModelFormFromSelection({
      providerId: preferredProvider,
      upstreamModel: entry.upstreamModel,
      modelName: entry.name,
      capabilities: entry.capabilities,
      existingIds,
      credentialCounts,
    });
    setAddingKey(entry.upstreamModel);
    try {
      await handleCreateModel(form);
    } finally {
      setAddingKey(null);
    }
  };

  const handleSavePriority = async (routes: RouteFormData[]) => {
    if (!priorityModel) return;
    setSaving(true);
    try {
      const form = modelToForm(priorityModel, routesByModel[priorityModel.ID] || []);
      form.routes = routes;
      await saveModel(secret, form, true);
      onNotice?.(t("models.notice.prioritySaved", { name: modelLabel(priorityModel) }));
      setPriorityModel(null);
      onMutated?.();
      void reloadFailover();
    } catch (error) {
      onError?.(error instanceof Error ? error.message : t("models.failedSavePriority"));
    } finally {
      setSaving(false);
    }
  };

  const handleSaveDetails = async (form: ModelFormData) => {
    setSaving(true);
    try {
      await saveModel(secret, form, true);
      onNotice?.(t("models.notice.updated", { name: form.display_name || form.id }));
      setDetailsModel(null);
      onMutated?.();
    } catch (error) {
      onError?.(error instanceof Error ? error.message : t("models.failedUpdate"));
    } finally {
      setSaving(false);
    }
  };

  const putEnabled = (model: ModelRecord, enabled: boolean) => {
    const form = modelToForm(model, routesByModel[model.ID] || []);
    form.enabled = enabled;
    return saveModel(secret, form, true);
  };

  const setEnabled = async (model: ModelRecord, enabled: boolean) => {
    markBusy(model.ID, true);
    try {
      await putEnabled(model, enabled);
      onNotice?.(enabled ? t("models.notice.enabled", { name: modelLabel(model) }) : t("models.notice.disabled", { name: modelLabel(model) }));
      onMutated?.();
    } catch (error) {
      onError?.(error instanceof Error ? error.message : t("models.failedUpdate"));
    } finally {
      markBusy(model.ID, false);
    }
  };

  const bulkSetEnabled = async (enabled: boolean) => {
    const targets = selectedModels.filter((model) => model.Enabled !== enabled);
    if (targets.length === 0) return;
    setBulkBusy(true);
    let failed = 0;
    for (const model of targets) {
      try {
        await putEnabled(model, enabled);
      } catch {
        failed += 1;
      }
    }
    setBulkBusy(false);
    onMutated?.();
    if (failed > 0) onError?.(t("models.notice.bulkFailed", { count: failed }));
    else onNotice?.(t(enabled ? "models.notice.bulkEnabled" : "models.notice.bulkDisabled", { count: targets.length }));
  };

  const confirmDelete = (targets: ModelRecord[]) => {
    if (targets.length === 0) return;
    const single = targets.length === 1 ? targets[0] : null;
    setConfirmState({
      title: single ? t("models.deleteModel") : t("models.deleteModels", { count: targets.length }),
      message: single
        ? t("models.deleteConfirm", { name: modelLabel(single) })
        : t("models.deleteConfirmMany", { count: targets.length }),
      confirmText: t("common.delete"),
      onConfirm: () => {
        void (async () => {
          setBulkBusy(targets.length > 1);
          let failed = 0;
          for (const model of targets) {
            markBusy(model.ID, true);
            try {
              await deleteModel(secret, model.ID);
            } catch (error) {
              failed += 1;
              if (single) onError?.(error instanceof Error ? error.message : t("models.failedDelete"));
            } finally {
              markBusy(model.ID, false);
            }
          }
          setBulkBusy(false);
          toggleSelection(targets.map((model) => model.ID), false);
          if (!single && failed > 0) onError?.(t("models.notice.bulkFailed", { count: failed }));
          else if (failed === 0) {
            onNotice?.(single ? t("models.notice.deleted", { name: modelLabel(single) }) : t("models.notice.bulkDeleted", { count: targets.length }));
          }
          onMutated?.();
        })();
      },
    });
  };

  const resetPaused = async () => {
    let failed = 0;
    for (const state of pausedStates) {
      try {
        await resetFailover(state.model_id, state.provider_id);
      } catch {
        failed += 1;
      }
    }
    if (failed > 0) onError?.(t("models.notice.bulkFailed", { count: failed }));
    else onNotice?.(t("models.notice.pausedReset", { count: pausedStates.length }));
  };

  const handlers: ModelItemHandlers = {
    openPriority: (model) => setPriorityModel(model),
    editDetails: (model) => setDetailsModel(model),
    setEnabled: (model, enabled) => void setEnabled(model, enabled),
    select: (model, select) => toggleSelection([model.ID], select),
    copyId: (model) => {
      copy(model.ID, model.ID);
      onNotice?.(t("models.notice.copied", { id: model.ID }));
    },
    remove: (model) => confirmDelete([model]),
  };

  const hasFilters = healthFilter !== "all" || providerFilter !== "all" || search.trim() !== "";
  const clearFilters = () => {
    setSearch("");
    setHealthFilter("all");
    setProviderFilter("all");
  };
  const filterTile = (filter: HealthFilter) => ({
    active: healthFilter === filter,
    onClick: () => setHealthFilter(healthFilter === filter && filter !== "all" ? "all" : filter),
  });

  const enabledCount = models.length - counts.disabled;

  if (models.length === 0 && loading) {
    return (
      <section className="cp-page">
        <EmptyPanel loading title={t("models.loading")} />
      </section>
    );
  }

  return (
    <section className="cp-page md-page">
      <PageStats
        label={t("models.stats.label")}
        items={[
          {
            key: "all",
            icon: "hub",
            label: t("models.stats.models"),
            value: models.length,
            hint: t("models.stats.modelsHint", { enabled: enabledCount, aliases: counts.aliases }),
            title: t("models.stats.modelsTitle"),
            ...filterTile("all"),
          },
          {
            key: "serving",
            icon: "check_circle",
            tone: "success",
            label: t("models.stats.serving"),
            value: counts.serving,
            hint: t("models.stats.servingHint"),
            muted: counts.serving === 0,
            ...filterTile("serving"),
          },
          {
            key: "degraded",
            icon: "alt_route",
            tone: "warning",
            label: t("models.stats.degraded"),
            value: counts.degraded,
            hint: t("models.stats.degradedHint"),
            muted: counts.degraded === 0,
            ...filterTile("degraded"),
          },
          {
            key: "attention",
            icon: "link_off",
            tone: "danger",
            label: t("models.stats.attention"),
            value: counts.attention,
            hint: t("models.stats.attentionHint"),
            muted: counts.attention === 0,
            ...filterTile("attention"),
          },
          {
            key: "disabled",
            icon: "power_settings_new",
            tone: "muted",
            label: t("models.stats.disabled"),
            value: counts.disabled,
            hint: t("models.stats.disabledHint", { count: counts.routesOff }),
            ...filterTile("disabled"),
          },
          {
            key: "discovered",
            icon: "travel_explore",
            tone: "info",
            label: t("models.stats.discovered"),
            value: discovery.ready ? discoveredEntries.length : "…",
            hint: discovery.ready ? t("models.stats.discoveredHint") : t("models.discovered.scanning"),
            muted: discovery.ready && discoveredEntries.length === 0,
            ...filterTile("discovered"),
          },
        ]}
      />

      <Toolbar>
        <ToolbarRow>
          <FilterTabs
            label={t("models.filterByProvider")}
            value={providerFilter}
            onChange={(value) => setProviderFilter(value === providerFilter ? "all" : value)}
            items={[
              { value: "all", icon: "apps", label: t("models.allProviders"), count: models.length },
              ...providerTabs.map(({ provider, count }) => ({
                value: provider.id,
                label: providerDisplayLabel(providers, provider.id),
                count,
                tone: provider.status && provider.status !== "healthy" && provider.status !== "unknown" ? ("warning" as const) : undefined,
                title: provider.status ? t("models.providerTabHint", { status: t(`models.providerStatus.${provider.status}`, { defaultValue: provider.status }) }) : undefined,
                logo: <ProviderLogo className="cp-tab-logo" providerId={provider.id} providerType={provider.type} />,
              })),
            ]}
          />
          <ToolbarEnd>
            <Button variant="primary" size="sm" icon="add" onClick={() => setShowSelectModal(true)}>
              {t("models.createModel")}
            </Button>
          </ToolbarEnd>
        </ToolbarRow>
        <ToolbarRow>
          <SearchField value={search} onChange={setSearch} placeholder={t("models.searchModels")} />
          <ToolbarEnd>
            <SelectField
              label={t("models.sortBy")}
              value={sortMode}
              onChange={setSortMode}
              options={[
                { value: "default", label: t("models.sort.default") },
                { value: "attention", label: t("models.sort.attention") },
                { value: "name", label: t("models.sort.name") },
                { value: "routes", label: t("models.sort.routes") },
              ]}
            />
            <Segmented
              label={t("models.viewMode")}
              value={viewMode}
              onChange={setViewMode}
              items={[
                { value: "grid", icon: "grid_view", label: t("models.viewGrid") },
                { value: "list", icon: "view_list", label: t("models.viewList") },
              ]}
            />
            <ActionMenu
              label={t("models.quickActions")}
              text={t("models.quickActions")}
              icon="auto_fix_high"
              badge={pausedStates.length || undefined}
              items={[
                {
                  key: "reset-paused",
                  icon: "restart_alt",
                  label: t("models.quick.resetPaused"),
                  hint: t("models.quick.resetPausedHint", { count: pausedStates.length }),
                  disabled: pausedStates.length === 0,
                  onSelect: () => void resetPaused(),
                },
                {
                  key: "rescan",
                  icon: "sync",
                  label: t("models.discovered.rescan"),
                  hint: t("models.discovered.rescanHint"),
                  disabled: discovery.loading,
                  onSelect: discovery.reload,
                },
                {
                  key: "select-visible",
                  icon: "select_all",
                  label: t("models.quick.selectVisible", { count: visibleViews.length }),
                  disabled: visibleViews.length === 0 || allVisibleSelected,
                  onSelect: () => toggleSelection(visibleViews.map((view) => view.model.ID), true),
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
        title={t("models.guide.title")}
        steps={[
          { icon: "hub", text: t("models.guide.models") },
          { icon: "low_priority", text: t("models.guide.chain") },
          { icon: "bolt", text: t("models.guide.fast") },
        ]}
        legend={
          <>
            <LegendItem tone="success">{t("models.guide.legendServing")}</LegendItem>
            <LegendItem tone="warning">{t("models.guide.legendDegraded")}</LegendItem>
            <LegendItem tone="danger">{t("models.guide.legendAttention")}</LegendItem>
            <LegendItem tone="info">{t("models.guide.legendSuggested")}</LegendItem>
          </>
        }
      />

      {healthFilter !== "discovered" ? (
        <section className="md-group" aria-label={t("models.configured.title")}>
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
                  onChange={(event) => toggleSelection(visibleViews.map((view) => view.model.ID), event.target.checked)}
                  aria-label={t("models.selectAllVisible")}
                />
                <span className="cp-section-icon material-symbols-outlined" aria-hidden>hub</span>
              </>
            }
            title={t("models.configured.title")}
            subtitle={
              visibleViews.length === models.length
                ? t("models.configured.summary", { count: models.length, enabled: enabledCount })
                : t("models.configured.summaryFiltered", { shown: visibleViews.length, count: models.length })
            }
            meta={
              <ToneCounts
                label={t("models.stats.label")}
                items={[
                  { tone: "success", count: counts.serving, label: t("models.health.serving") },
                  { tone: "warning", count: counts.degraded, label: t("models.health.degraded") },
                  { tone: "danger", count: counts.attention, label: t("models.health.attention") },
                ]}
              />
            }
          />

          {models.length === 0 ? (
            <EmptyPanel
              icon="hub"
              title={t("models.empty.title")}
              description={t("models.empty.description")}
              action={
                <Button variant="primary" icon="add" onClick={() => setShowSelectModal(true)}>
                  {t("models.createModel")}
                </Button>
              }
            />
          ) : visibleViews.length === 0 ? (
            <EmptyPanel
              icon="filter_alt_off"
              title={t("models.empty.noMatch")}
              description={t("models.empty.noMatchHint")}
              action={hasFilters ? <Button variant="secondary" icon="filter_alt_off" onClick={clearFilters}>{t("models.clearFilters")}</Button> : null}
            />
          ) : viewMode === "grid" ? (
            <div className="cp-grid" style={{ ["--cp-card-min" as string]: "330px" }}>
              {visibleViews.map((view) => (
                <ModelCard
                  key={view.model.ID}
                  view={view}
                  handlers={handlers}
                  selected={selectedIds.has(view.model.ID)}
                  busy={busyIds.has(view.model.ID) || bulkBusy}
                  discovering={!discovery.ready}
                />
              ))}
            </div>
          ) : (
            <div className="cp-list md-list">
              <div className="cp-list-head" aria-hidden>
                <span />
                <span>{t("models.columns.model")}</span>
                <span>{t("models.columns.status")}</span>
                <span>{t("models.columns.chain")}</span>
                <span>{t("models.columns.aliases")}</span>
                <span>{t("models.columns.enabled")}</span>
                <span />
              </div>
              {visibleViews.map((view) => (
                <ModelRow
                  key={view.model.ID}
                  view={view}
                  handlers={handlers}
                  selected={selectedIds.has(view.model.ID)}
                  busy={busyIds.has(view.model.ID) || bulkBusy}
                  discovering={!discovery.ready}
                />
              ))}
            </div>
          )}
        </section>
      ) : null}

      {showDiscovered ? (
        <DiscoveredModels
          entries={visibleDiscovered}
          totalCount={discoveredEntries.length}
          providers={providers}
          credentialCounts={credentialCounts}
          loading={discovery.loading}
          ready={discovery.ready}
          errors={discoveryErrors}
          filtered={providerFilter !== "all" || search.trim() !== ""}
          addingKey={addingKey}
          disabled={saving}
          onAdd={(entry) => void handleAddDiscovered(entry)}
          onRescan={discovery.reload}
        />
      ) : null}

      <BulkBar
        count={selectedModels.length}
        busy={bulkBusy}
        onClear={() => setSelectedIds(new Set())}
        selectAll={
          allVisibleSelected
            ? null
            : { label: t("models.quick.selectVisible", { count: visibleViews.length }), onSelect: () => toggleSelection(visibleViews.map((view) => view.model.ID), true) }
        }
      >
        <BulkButton icon="toggle_on" disabled={bulkBusy} onClick={() => void bulkSetEnabled(true)}>
          {t("models.bulk.enable")}
        </BulkButton>
        <BulkButton icon="toggle_off" disabled={bulkBusy} onClick={() => void bulkSetEnabled(false)}>
          {t("models.bulk.disable")}
        </BulkButton>
        <BulkButton icon="delete" tone="danger" disabled={bulkBusy} onClick={() => confirmDelete(selectedModels)}>
          {t("models.bulk.delete")}
        </BulkButton>
      </BulkBar>

      <ModelSelectModal
        open={showSelectModal}
        secret={secret}
        providers={providers}
        models={models}
        routesByModel={routesByModel}
        existingIds={existingIds}
        credentialCounts={credentialCounts}
        saving={saving}
        onClose={() => setShowSelectModal(false)}
        onSubmit={(form) => void handleCreateModel(form)}
      />

      <ModelFormModal
        open={Boolean(detailsModel)}
        editing
        initial={detailsForm}
        existingIds={existingIds}
        saving={saving}
        onClose={() => setDetailsModel(null)}
        onSubmit={(form) => void handleSaveDetails(form)}
      />

      <ProviderPriorityModal
        open={Boolean(priorityModel)}
        secret={secret}
        model={priorityModel}
        routes={priorityRoutes}
        providers={providers}
        credentialCounts={credentialCounts}
        saving={saving}
        onClose={() => setPriorityModel(null)}
        onSubmit={(routes) => void handleSavePriority(routes)}
      />

      <ConfirmDialog
        open={Boolean(confirmState)}
        onClose={() => setConfirmState(null)}
        onConfirm={() => {
          confirmState?.onConfirm();
          setConfirmState(null);
        }}
        title={confirmState?.title || ""}
        message={confirmState?.message || ""}
        confirmText={confirmState?.confirmText}
        cancelText={t("common.cancel")}
        variant="danger"
      />
    </section>
  );
}
