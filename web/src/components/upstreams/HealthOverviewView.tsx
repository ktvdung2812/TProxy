import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Button, ConfirmDialog } from "../ui";
import {
  BulkBar,
  BulkButton,
  EmptyPanel,
  FilterTabs,
  GuideToggle,
  LegendItem,
  PageGuide,
  PageStats,
  SearchField,
  Segmented,
  SelectField,
  Toolbar,
  ToolbarEnd,
  ToolbarRow,
  ToolButton,
  usePageGuide,
  useStoredState,
} from "../page-kit";
import { ProviderHealthCard, ProviderHealthRow, type ProviderHealthHandlers, type ProviderHealthView } from "./ProviderHealthItem";
import {
  PROVIDER_HEALTH_ORDER,
  PROVIDER_HEALTH_TONE,
  credentialIssue,
  formatAgo,
  parseProviderHealthFilter,
  providerHealth,
  type ProviderHealth,
  type SnapshotCredential,
  type SnapshotProvider,
  type SnapshotRoute,
} from "./health";

type Filter = ProviderHealth | "all";
type SortMode = "attention" | "name" | "checked";
type ViewMode = "list" | "grid";
const SORT_MODES = ["attention", "name", "checked"] as const;
const VIEW_MODES = ["list", "grid"] as const;

type Props = {
  providers: SnapshotProvider[];
  credentials: Record<string, SnapshotCredential[]>;
  routes: Record<string, SnapshotRoute[]>;
  discovered: Record<string, Array<{ id: string; name?: string }>>;
  loading?: boolean;
  /** True while App's "check all" pass runs. */
  checkingAll: boolean;
  onCheck: (id: string) => Promise<void>;
  /** Checks the given providers (or every provider) and reports one summary. */
  onCheckMany: (ids?: string[]) => Promise<void>;
  onDiscover: (id: string) => Promise<void>;
  /** Deletes without asking — this page confirms first. */
  onDelete: (provider: SnapshotProvider) => Promise<void>;
};

export function HealthOverviewView({
  providers,
  credentials,
  routes,
  discovered,
  loading,
  checkingAll,
  onCheck,
  onCheckMany,
  onDiscover,
  onDelete,
}: Props) {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const filter = parseProviderHealthFilter(searchParams.get("status"));
  const [search, setSearch] = useState("");
  const [sortMode, setSortMode] = useStoredState<SortMode>("tproxy.health.sort", "attention", SORT_MODES);
  const [viewMode, setViewMode] = useStoredState<ViewMode>("tproxy.health.view", "list", VIEW_MODES);
  const guide = usePageGuide("upstreams");
  const [checkingIds, setCheckingIds] = useState<Set<string>>(() => new Set());
  const [discoveringIds, setDiscoveringIds] = useState<Set<string>>(() => new Set());
  // Providers inside a running "check all"/bulk pass, so only they spin.
  const [batchIds, setBatchIds] = useState<Set<string> | null>(null);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(() => new Set());
  const [bulkBusy, setBulkBusy] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<SnapshotProvider | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [clock, setClock] = useState(() => Date.now());

  // "Checked 4m ago" stays honest while the page is open.
  useEffect(() => {
    const timer = window.setInterval(() => setClock(Date.now()), 30000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    const ids = new Set(providers.map((provider) => provider.ID));
    setSelectedIds((current) => {
      const next = new Set([...current].filter((id) => ids.has(id)));
      return next.size === current.size ? current : next;
    });
  }, [providers]);

  const setFilter = (next: Filter) => {
    setSearchParams((current) => {
      const params = new URLSearchParams(current);
      if (next === "all") params.delete("status");
      else params.set("status", next);
      return params;
    }, { replace: true });
  };
  const toggleFilter = (next: Filter) => setFilter(filter === next ? "all" : next);

  const routeCountByProvider = useMemo(() => {
    const counts: Record<string, number> = {};
    for (const list of Object.values(routes || {})) {
      for (const route of list || []) {
        if (route.Enabled) counts[route.ProviderID] = (counts[route.ProviderID] || 0) + 1;
      }
    }
    return counts;
  }, [routes]);

  const views = useMemo<ProviderHealthView[]>(() => {
    return providers.map((provider) => {
      const accounts = credentials[provider.ID] || [];
      return {
        provider,
        name: provider.Name || provider.ID,
        typeName: provider.Type,
        health: providerHealth(provider),
        accounts: accounts.length,
        accountsOn: accounts.filter((item) => item.enabled).length,
        accountIssues: accounts.filter((item) => credentialIssue(item) !== null).length,
        routes: routeCountByProvider[provider.ID] || 0,
        proxyPools: provider.ProxyPoolIDs?.length || 0,
        lastChecked: formatAgo(provider.LastChecked, i18n.language, clock),
        discovered: discovered[provider.ID],
        checking: checkingIds.has(provider.ID) || Boolean(batchIds?.has(provider.ID)),
        discovering: discoveringIds.has(provider.ID),
        busy: deletingId === provider.ID || bulkBusy,
        deleting: deletingId === provider.ID,
        selected: selectedIds.has(provider.ID),
      };
    });
  }, [providers, credentials, routeCountByProvider, discovered, checkingIds, batchIds, discoveringIds, deletingId, bulkBusy, selectedIds, i18n.language, clock]);

  const searched = useMemo(() => {
    const query = search.trim().toLowerCase();
    if (!query) return views;
    return views.filter((view) =>
      [view.name, view.provider.ID, view.provider.Type, view.provider.BaseURL, view.provider.LastError]
        .some((value) => value?.toLowerCase().includes(query)),
    );
  }, [views, search]);

  const counts = useMemo(() => {
    const result: Record<ProviderHealth, number> = { healthy: 0, degraded: 0, auth_required: 0, disabled: 0, unchecked: 0 };
    for (const view of searched) result[view.health] += 1;
    return result;
  }, [searched]);

  const visible = useMemo(() => {
    const list = filter === "all" ? [...searched] : searched.filter((view) => view.health === filter);
    const byName = (a: ProviderHealthView, b: ProviderHealthView) => a.name.localeCompare(b.name);
    if (sortMode === "name") return list.sort(byName);
    if (sortMode === "checked") {
      // Stalest first: the provider you know least about right now.
      const time = (view: ProviderHealthView) => Date.parse(view.provider.LastChecked || "") || 0;
      return list.sort((a, b) => time(a) - time(b) || byName(a, b));
    }
    return list.sort((a, b) => PROVIDER_HEALTH_ORDER.indexOf(a.health) - PROVIDER_HEALTH_ORDER.indexOf(b.health) || byName(a, b));
  }, [searched, filter, sortMode]);

  const lastCheck = useMemo(() => {
    const times = providers
      .filter((provider) => provider.Enabled)
      .map((provider) => Date.parse(provider.LastChecked || ""))
      .filter((time) => Number.isFinite(time) && time > 0);
    if (times.length === 0) return null;
    return {
      newest: formatAgo(new Date(Math.max(...times)).toISOString(), i18n.language, clock),
      oldest: formatAgo(new Date(Math.min(...times)).toISOString(), i18n.language, clock),
    };
  }, [providers, i18n.language, clock]);

  const selectedProviders = providers.filter((provider) => selectedIds.has(provider.ID));
  const allVisibleSelected = visible.length > 0 && visible.every((view) => selectedIds.has(view.provider.ID));

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

  const markBusy = (setter: typeof setCheckingIds, id: string, busy: boolean) => {
    setter((current) => {
      const next = new Set(current);
      if (busy) next.add(id);
      else next.delete(id);
      return next;
    });
  };

  const check = async (provider: SnapshotProvider) => {
    markBusy(setCheckingIds, provider.ID, true);
    try {
      await onCheck(provider.ID);
    } finally {
      markBusy(setCheckingIds, provider.ID, false);
    }
  };

  const discover = async (provider: SnapshotProvider) => {
    markBusy(setDiscoveringIds, provider.ID, true);
    try {
      await onDiscover(provider.ID);
    } finally {
      markBusy(setDiscoveringIds, provider.ID, false);
    }
  };

  const checkMany = async (ids?: string[]) => {
    if (bulkBusy || checkingAll) return;
    setBulkBusy(true);
    setBatchIds(new Set(ids ?? providers.map((provider) => provider.ID)));
    try {
      await onCheckMany(ids);
    } finally {
      setBatchIds(null);
      setBulkBusy(false);
    }
  };

  const bulkDiscover = async () => {
    if (bulkBusy || selectedProviders.length === 0) return;
    setBulkBusy(true);
    try {
      for (const provider of selectedProviders) await discover(provider);
    } finally {
      setBulkBusy(false);
    }
  };

  const confirmDelete = async () => {
    const target = deleteTarget;
    if (!target) return;
    setDeleteTarget(null);
    setDeletingId(target.ID);
    try {
      await onDelete(target);
    } finally {
      setDeletingId(null);
    }
  };

  const handlers: ProviderHealthHandlers = {
    open: (provider) => navigate(`/providers/${encodeURIComponent(provider.ID)}`),
    select: (provider, select) => toggleSelection([provider.ID], select),
    check: (provider) => void check(provider),
    discover: (provider) => void discover(provider),
    remove: (provider) => setDeleteTarget(provider),
  };

  const hasFilters = filter !== "all" || search.trim() !== "";
  const clearFilters = () => {
    setSearch("");
    setFilter("all");
  };

  if (providers.length === 0) {
    return (
      <section className="cp-page">
        {loading ? (
          <EmptyPanel loading title={t("upstreams.loading")} />
        ) : (
          <EmptyPanel
            icon="dns"
            title={t("upstreams.emptyTitle")}
            description={t("upstreams.emptyHint")}
            action={<Button variant="primary" icon="add_link" onClick={() => navigate("/providers")}>{t("upstreams.connectProvider")}</Button>}
          />
        )}
      </section>
    );
  }

  const tile = (health: ProviderHealth, icon: string) => ({
    key: health,
    icon,
    tone: PROVIDER_HEALTH_TONE[health],
    label: t(`upstreams.health.${health}`),
    value: counts[health],
    hint: t(`upstreams.tileHint.${health}`),
    muted: counts[health] === 0,
    active: filter === health,
    onClick: () => toggleFilter(health),
    title: t(`upstreams.healthHint.${health}`),
  });

  const deleteAccounts = deleteTarget ? (credentials[deleteTarget.ID] || []).length : 0;

  return (
    <section className="cp-page">
      <PageStats
        label={t("upstreams.statsLabel")}
        items={[
          {
            key: "all",
            icon: "dns",
            label: t("upstreams.stats.providers"),
            value: searched.length,
            hint: counts.unchecked > 0
              ? t("upstreams.stats.unchecked", { count: counts.unchecked })
              : t("upstreams.stats.providersHint", { count: searched.length - counts.disabled }),
            active: filter === "all",
            onClick: () => setFilter("all"),
          },
          tile("healthy", "check_circle"),
          tile("degraded", "warning"),
          tile("auth_required", "key_off"),
          tile("disabled", "power_settings_new"),
          {
            key: "last-check",
            icon: "history",
            label: t("upstreams.stats.lastCheck"),
            value: lastCheck?.newest ?? "—",
            hint: lastCheck ? t("upstreams.stats.oldestCheck", { time: lastCheck.oldest }) : t("upstreams.stats.noChecks"),
            mono: true,
          },
        ]}
      />

      <Toolbar>
        <ToolbarRow>
          <FilterTabs
            label={t("upstreams.filterLabel")}
            value={filter}
            onChange={toggleFilter}
            items={[
              { value: "all", icon: "apps", label: t("upstreams.filterAll"), count: searched.length },
              ...PROVIDER_HEALTH_ORDER
                .filter((health) => health !== "unchecked" || counts.unchecked > 0)
                .map((health) => ({
                  value: health,
                  label: t(`upstreams.health.${health}`),
                  count: counts[health],
                  tone: PROVIDER_HEALTH_TONE[health],
                  icon: { healthy: "check_circle", degraded: "warning", auth_required: "key_off", disabled: "power_settings_new", unchecked: "help" }[health],
                })),
            ]}
          />
          <ToolbarEnd>
            <Button
              variant="primary"
              size="sm"
              icon="monitor_heart"
              loading={checkingAll && batchIds?.size === providers.length}
              disabled={checkingAll || bulkBusy}
              onClick={() => void checkMany()}
              title={t("upstreams.checkAllHint")}
            >
              {checkingAll ? t("upstreams.checkingAll") : t("upstreams.checkAll")}
            </Button>
          </ToolbarEnd>
        </ToolbarRow>
        <ToolbarRow>
          <SearchField value={search} onChange={setSearch} placeholder={t("upstreams.searchPlaceholder")} />
          <ToolbarEnd>
            <SelectField
              label={t("upstreams.sortLabel")}
              value={sortMode}
              onChange={setSortMode}
              options={[
                { value: "attention", label: t("upstreams.sort.attention") },
                { value: "name", label: t("upstreams.sort.name") },
                { value: "checked", label: t("upstreams.sort.checked") },
              ]}
            />
            <Segmented
              label={t("upstreams.viewMode")}
              value={viewMode}
              onChange={setViewMode}
              items={[
                { value: "list", icon: "view_list", label: t("upstreams.viewList") },
                { value: "grid", icon: "grid_view", label: t("upstreams.viewGrid") },
              ]}
            />
            <ToolButton icon="dns" onClick={() => navigate("/providers")} title={t("upstreams.manageProvidersHint")}>
              {t("upstreams.manageProviders")}
            </ToolButton>
            <GuideToggle open={guide.open} onToggle={guide.toggle} />
          </ToolbarEnd>
        </ToolbarRow>
      </Toolbar>

      <PageGuide
        open={guide.open}
        onDismiss={guide.dismiss}
        title={t("upstreams.guide.title")}
        steps={[
          { icon: "monitor_heart", text: t("upstreams.guide.status") },
          { icon: "travel_explore", text: t("upstreams.guide.actions") },
          { icon: "tune", text: t("upstreams.guide.fix") },
        ]}
        legend={
          <>
            <LegendItem tone="success">{t("upstreams.health.healthy")}</LegendItem>
            <LegendItem tone="warning">{t("upstreams.health.degraded")}</LegendItem>
            <LegendItem tone="danger">{t("upstreams.health.auth_required")}</LegendItem>
            <LegendItem tone="muted">{t("upstreams.health.disabled")}</LegendItem>
          </>
        }
      />

      {visible.length === 0 ? (
        <EmptyPanel
          icon="filter_alt_off"
          title={t("upstreams.noMatch")}
          description={t("upstreams.noMatchHint")}
          action={hasFilters ? <Button variant="secondary" icon="filter_alt_off" onClick={clearFilters}>{t("upstreams.clearFilters")}</Button> : null}
        />
      ) : viewMode === "grid" ? (
        <div className="cp-grid" style={{ ["--cp-card-min" as string]: "320px" }}>
          {visible.map((view) => <ProviderHealthCard key={view.provider.ID} view={view} handlers={handlers} />)}
        </div>
      ) : (
        <div className="cp-list hl-list">
          <div className="cp-list-head" aria-hidden>
            <span>
              <input
                type="checkbox"
                className="cp-check"
                checked={allVisibleSelected}
                ref={(element) => {
                  if (element) element.indeterminate = !allVisibleSelected && visible.some((view) => selectedIds.has(view.provider.ID));
                }}
                onChange={(event) => toggleSelection(visible.map((view) => view.provider.ID), event.target.checked)}
                aria-label={t("upstreams.selectVisible")}
                tabIndex={-1}
              />
            </span>
            <span>{t("upstreams.columns.provider")}</span>
            <span>{t("upstreams.columns.status")}</span>
            <span>{t("upstreams.columns.accounts")}</span>
            <span>{t("upstreams.columns.routes")}</span>
            <span>{t("upstreams.columns.lastCheck")}</span>
            <span />
          </div>
          {visible.map((view) => <ProviderHealthRow key={view.provider.ID} view={view} handlers={handlers} />)}
        </div>
      )}

      <BulkBar
        count={selectedProviders.length}
        busy={bulkBusy || checkingAll}
        onClear={() => setSelectedIds(new Set())}
        selectAll={
          allVisibleSelected
            ? null
            : { label: t("upstreams.selectAllVisible", { count: visible.length }), onSelect: () => toggleSelection(visible.map((view) => view.provider.ID), true) }
        }
      >
        <BulkButton icon="monitor_heart" spinning={checkingAll && batchIds !== null && batchIds.size < providers.length} disabled={bulkBusy || checkingAll} onClick={() => void checkMany(selectedProviders.map((provider) => provider.ID))}>
          {t("upstreams.bulkCheck")}
        </BulkButton>
        <BulkButton icon="travel_explore" disabled={bulkBusy || checkingAll} onClick={() => void bulkDiscover()}>
          {t("upstreams.bulkDiscover")}
        </BulkButton>
      </BulkBar>

      <ConfirmDialog
        open={Boolean(deleteTarget)}
        title={t("upstreams.deleteTitle")}
        message={
          deleteTarget
            ? t("upstreams.deleteMessage", { name: deleteTarget.Name || deleteTarget.ID, count: deleteAccounts })
            : ""
        }
        confirmText={t("upstreams.deleteProvider")}
        cancelText={t("common.cancel")}
        variant="danger"
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => void confirmDelete()}
      />
    </section>
  );
}
