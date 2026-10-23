import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { Button } from "../ui";
import {
  ActionMenu,
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
  ToolButton,
  usePageGuide,
  useStoredState,
} from "../page-kit";
import type { NinerouterPreset } from "./api";
import { getProviderTypeInfo, providerDetailPath } from "./catalog";
import { catalogWithPreset } from "./connectionMethods";
import { ProviderCatalogSection } from "./ProviderCatalogSection";
import { ProviderCard, ProviderRow, type ProviderItemHandlers, type ProviderView } from "./ProviderListItem";
import {
  PROVIDER_KIND_ICON,
  PROVIDER_KINDS,
  countAccounts,
  matchesHealthFilter,
  nextCooldown,
  providerHealth,
  providerKind,
  timeLeft,
  type ProviderHealth,
  type ProviderHealthFilter,
  type ProviderKind,
} from "./providerHealth";
import type { Credential, Provider } from "./types";

type Props = {
  providers: Provider[];
  credentials: Record<string, Credential[]>;
  presets: NinerouterPreset[];
  presetsLoading?: boolean;
  snapshotLoading?: boolean;
  checkingIds: Set<string>;
  testingSection: string | null;
  exportBusy: boolean;
  onAdd: (type?: string) => void;
  onImport: () => void;
  onExportAuth: () => void;
  onCheck: (provider: Provider) => void;
  onTestSection: (section: string, providerIds: string[]) => void;
  onCopyId: (provider: Provider) => void;
  onDelete: (provider: Provider) => void;
};

type KindFilter = "all" | ProviderKind;
type SortMode = "attention" | "accounts" | "name";
type ViewMode = "grid" | "list";
const SORT_MODES = ["attention", "accounts", "name"] as const;
const VIEW_MODES = ["grid", "list"] as const;

// Problems first, so the operator sees what needs a hand before scrolling.
const HEALTH_RANK: Record<ProviderHealth, number> = { attention: 0, degraded: 1, idle: 2, unknown: 3, healthy: 4, off: 5 };

/** Providers list: what is connected and how healthy it is, then the catalog to add more. */
export function ProviderList({
  providers,
  credentials,
  presets,
  presetsLoading,
  snapshotLoading,
  checkingIds,
  testingSection,
  exportBusy,
  onAdd,
  onImport,
  onExportAuth,
  onCheck,
  onTestSection,
  onCopyId,
  onDelete,
}: Props) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const guide = usePageGuide("providers");
  const [search, setSearch] = useState("");
  const [kindFilter, setKindFilter] = useState<KindFilter>("all");
  const [healthFilter, setHealthFilter] = useState<ProviderHealthFilter>("all");
  const [sortMode, setSortMode] = useStoredState<SortMode>("tproxy.providers.sort", "attention", SORT_MODES);
  const [viewMode, setViewMode] = useStoredState<ViewMode>("tproxy.providers.view", "grid", VIEW_MODES);
  const [now, setNow] = useState(() => Date.now());

  // Relative "checked 4m ago" labels and cooldown countdowns stay current.
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 30000);
    return () => window.clearInterval(timer);
  }, []);

  const presetIds = useMemo(() => new Set(presets.map((preset) => preset.id)), [presets]);

  const views = useMemo<ProviderView[]>(
    () =>
      providers.map((provider) => {
        const items = credentials[provider.ID] || [];
        const catalog = catalogWithPreset(provider.Type, provider.ID, presets);
        const noAuth = Boolean(catalog.noAuth);
        return {
          provider,
          name: provider.Name?.trim() || catalog.name || provider.ID,
          kind: providerKind(provider, presets),
          health: providerHealth(provider, items, noAuth),
          counts: countAccounts(items),
          noAuth,
          isPreset: presetIds.has(provider.ID),
          color: catalog.color || getProviderTypeInfo(provider.Type).color,
          cooldownUntil: nextCooldown(items),
          checking: checkingIds.has(provider.ID),
        };
      }),
    // now re-evaluates cooldowns that expire while the page is open.
    [providers, credentials, presets, presetIds, checkingIds, now],
  );

  const query = search.trim().toLowerCase();
  const scoped = useMemo(
    () =>
      views.filter((view) => {
        if (kindFilter !== "all" && view.kind !== kindFilter) return false;
        if (!query) return true;
        return [view.name, view.provider.ID, view.provider.Type].some((value) => value?.toLowerCase().includes(query));
      }),
    [views, kindFilter, query],
  );

  const stats = useMemo(() => {
    const result = { total: scoped.length, healthy: 0, attention: 0, off: 0, accounts: 0, accountsOn: 0, cooldown: 0, cooldownUntil: null as string | null };
    for (const view of scoped) {
      if (view.health === "healthy") result.healthy += 1;
      if (view.health === "degraded" || view.health === "attention") result.attention += 1;
      if (view.health === "off") result.off += 1;
      result.accounts += view.counts.total;
      result.accountsOn += view.counts.on;
      result.cooldown += view.counts.cooldown;
      if (view.cooldownUntil && (!result.cooldownUntil || Date.parse(view.cooldownUntil) < Date.parse(result.cooldownUntil))) {
        result.cooldownUntil = view.cooldownUntil;
      }
    }
    return result;
  }, [scoped]);

  const visible = useMemo(() => {
    const items = scoped.filter((view) => matchesHealthFilter(healthFilter, view.health, view.counts));
    const byName = (a: ProviderView, b: ProviderView) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" });
    return items.sort((a, b) => {
      if (sortMode === "name") return byName(a, b);
      if (sortMode === "attention") {
        const rank = HEALTH_RANK[a.health] - HEALTH_RANK[b.health];
        if (rank !== 0) return rank;
      }
      return b.counts.total - a.counts.total || b.counts.on - a.counts.on || byName(a, b);
    });
  }, [scoped, healthFilter, sortMode]);

  const groups = useMemo(
    () =>
      PROVIDER_KINDS.map((kind) => ({ kind, items: visible.filter((view) => view.kind === kind) })).filter((group) => group.items.length > 0),
    [visible],
  );

  const kindCounts = useMemo(() => {
    const counts: Record<ProviderKind, number> = { oauth: 0, apikey: 0, custom: 0, other: 0 };
    for (const view of views) counts[view.kind] += 1;
    return counts;
  }, [views]);

  const handlers: ProviderItemHandlers = {
    open: (provider) => navigate(providerDetailPath(provider.ID)),
    check: onCheck,
    copyId: onCopyId,
    remove: onDelete,
  };

  const filterTile = (filter: ProviderHealthFilter) => ({
    active: healthFilter === filter,
    onClick: () => setHealthFilter(healthFilter === filter && filter !== "all" ? "all" : filter),
  });

  const hasFilters = kindFilter !== "all" || healthFilter !== "all" || query !== "";
  const clearFilters = () => {
    setSearch("");
    setKindFilter("all");
    setHealthFilter("all");
  };

  const enabledIds = views.filter((view) => view.provider.Enabled).map((view) => view.provider.ID);
  const attentionIds = views.filter((view) => view.health === "degraded" || view.health === "attention").map((view) => view.provider.ID);
  const cooldownLeft = timeLeft(stats.cooldownUntil ?? undefined, now);
  const loadingFirst = snapshotLoading && providers.length === 0;

  return (
    <section className="cp-page pv-page">
      <PageStats
        label={t("providers.stats.label")}
        items={[
          {
            key: "all",
            icon: "hub",
            label: t("providers.stats.connected"),
            value: stats.total,
            hint: kindFilter === "all" && !query ? t("providers.stats.connectedHint") : t("providers.stats.connectedOf", { count: views.length }),
            ...filterTile("all"),
          },
          {
            key: "healthy",
            icon: "check_circle",
            tone: "success",
            label: t("providers.stats.healthy"),
            value: stats.healthy,
            hint: t("providers.stats.healthyHint"),
            muted: stats.healthy === 0,
            ...filterTile("healthy"),
          },
          {
            key: "attention",
            icon: "report",
            tone: "danger",
            label: t("providers.stats.attention"),
            value: stats.attention,
            hint: t("providers.stats.attentionHint"),
            muted: stats.attention === 0,
            ...filterTile("attention"),
          },
          {
            key: "off",
            icon: "power_settings_new",
            tone: "muted",
            label: t("providers.stats.off"),
            value: stats.off,
            hint: t("providers.stats.offHint"),
            ...filterTile("off"),
          },
          {
            key: "accounts",
            icon: "group",
            label: t("providers.stats.accounts"),
            value: stats.accounts,
            hint: t("providers.stats.accountsHint", { on: stats.accountsOn, off: stats.accounts - stats.accountsOn }),
          },
          {
            key: "cooldown",
            icon: "timer",
            tone: "warning",
            label: t("providers.stats.cooldown"),
            value: stats.cooldown,
            hint: cooldownLeft ? t("providers.stats.cooldownNext", { time: cooldownLeft }) : t("providers.stats.cooldownNone"),
            muted: stats.cooldown === 0,
            ...filterTile("cooldown"),
          },
        ]}
      />

      <Toolbar>
        <ToolbarRow>
          <FilterTabs
            label={t("providers.list.kindFilter")}
            value={kindFilter}
            onChange={(kind) => setKindFilter(kind === kindFilter ? "all" : kind)}
            items={[
              { value: "all" as KindFilter, icon: "apps", label: t("providers.kind.all"), count: views.length },
              ...PROVIDER_KINDS.filter((kind) => kindCounts[kind] > 0 || kind === "custom").map((kind) => ({
                value: kind as KindFilter,
                icon: PROVIDER_KIND_ICON[kind],
                label: t(`providers.kind.${kind}`),
                count: kindCounts[kind],
              })),
            ]}
          />
          <ToolbarEnd>
            <ToolButton icon="upload" onClick={onImport} title={t("import.cardDesc")}>
              {t("providers.list.import")}
            </ToolButton>
            <Button variant="primary" size="sm" icon="add" onClick={() => onAdd()}>
              {t("providers.list.addProvider")}
            </Button>
          </ToolbarEnd>
        </ToolbarRow>
        <ToolbarRow>
          <SearchField value={search} onChange={setSearch} placeholder={t("providers.list.searchPlaceholder")} />
          <ToolbarEnd>
            <SelectField
              label={t("providers.list.sortBy")}
              value={sortMode}
              onChange={setSortMode}
              options={SORT_MODES.map((mode) => ({ value: mode, label: t(`providers.list.sort.${mode}`) }))}
            />
            <Segmented
              label={t("providers.list.viewMode")}
              value={viewMode}
              onChange={setViewMode}
              items={[
                { value: "grid", icon: "grid_view", label: t("providers.list.viewGrid") },
                { value: "list", icon: "view_list", label: t("providers.list.viewList") },
              ]}
            />
            <ActionMenu
              label={t("providers.list.quickActions")}
              text={t("providers.list.quickActions")}
              icon="auto_fix_high"
              badge={attentionIds.length || undefined}
              items={[
                {
                  key: "check-attention",
                  icon: "report",
                  label: t("providers.list.checkAttention"),
                  hint: t("providers.list.checkAttentionHint", { count: attentionIds.length }),
                  disabled: attentionIds.length === 0 || testingSection !== null,
                  onSelect: () => onTestSection("attention", attentionIds),
                },
                {
                  key: "check-all",
                  icon: "monitor_heart",
                  label: t("providers.list.checkAll"),
                  hint: t("providers.list.checkAllHint", { count: enabledIds.length }),
                  disabled: enabledIds.length === 0 || testingSection !== null,
                  onSelect: () => onTestSection("all", enabledIds),
                },
                {
                  key: "export",
                  icon: "download",
                  label: t("providers.exportOAuthBundle"),
                  hint: t("providers.list.exportHint"),
                  disabled: exportBusy,
                  onSelect: onExportAuth,
                },
                {
                  key: "import",
                  icon: "upload_file",
                  label: t("import.cardTitle"),
                  hint: t("providers.list.importHint"),
                  onSelect: onImport,
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
        title={t("providers.guide.title")}
        steps={[
          { icon: "hub", text: t("providers.guide.connected") },
          { icon: "monitor_heart", text: t("providers.guide.health") },
          { icon: "add_circle", text: t("providers.guide.add") },
        ]}
        legend={
          <>
            <LegendItem tone="success">{t("providers.health.healthy")}</LegendItem>
            <LegendItem tone="warning">{t("providers.health.degraded")}</LegendItem>
            <LegendItem tone="danger">{t("providers.health.attention")}</LegendItem>
            <LegendItem tone="muted">{t("providers.health.off")}</LegendItem>
          </>
        }
      />

      {loadingFirst ? (
        <div className="cp-grid" style={{ ["--cp-card-min" as string]: "300px" }}>
          {[0, 1, 2].map((index) => (
            <div key={index} className="cp-card pv-card-skeleton">
              <div className="cp-skeleton"><span /><span /></div>
            </div>
          ))}
        </div>
      ) : views.length === 0 ? (
        <EmptyPanel
          icon="hub"
          title={t("providers.list.emptyTitle")}
          description={t("providers.list.emptyHint")}
          action={
            <div className="pv-empty-actions">
              <Button variant="primary" icon="add" onClick={() => onAdd()}>{t("providers.list.addProvider")}</Button>
              <Button variant="secondary" icon="upload" onClick={onImport}>{t("import.cardTitle")}</Button>
            </div>
          }
        />
      ) : groups.length === 0 ? (
        <EmptyPanel
          compact
          icon="filter_alt_off"
          title={t("providers.list.noMatch")}
          description={query ? t("providers.list.noMatchSearch") : t("providers.list.noMatchHint")}
          action={hasFilters ? <Button variant="secondary" icon="filter_alt_off" onClick={clearFilters}>{t("providers.list.clearFilters")}</Button> : null}
        />
      ) : (
        <div className="pv-groups">
          {groups.map((group) => {
            const accounts = group.items.reduce((sum, view) => sum + view.counts.total, 0);
            const health = { healthy: 0, warning: 0, danger: 0 };
            for (const view of group.items) {
              if (view.health === "healthy") health.healthy += 1;
              else if (view.health === "degraded") health.warning += 1;
              else if (view.health === "attention") health.danger += 1;
            }
            const ids = group.items.filter((view) => view.provider.Enabled).map((view) => view.provider.ID);
            return (
              <section key={group.kind} className="pv-group" aria-label={t(`providers.kind.${group.kind}`)}>
                <SectionHead
                  leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>{PROVIDER_KIND_ICON[group.kind]}</span>}
                  title={t(`providers.kindTitle.${group.kind}`)}
                  subtitle={`${t("providers.list.groupProviders", { count: group.items.length })} · ${t("providers.list.groupAccounts", { count: accounts })}`}
                  meta={
                    <ToneCounts
                      label={t("providers.list.groupHealth")}
                      items={[
                        { tone: "success", count: health.healthy, label: t("providers.health.healthy") },
                        { tone: "warning", count: health.warning, label: t("providers.health.degraded") },
                        { tone: "danger", count: health.danger, label: t("providers.health.attention") },
                      ]}
                    />
                  }
                  actions={
                    <ToolButton
                      icon="monitor_heart"
                      spinning={testingSection === group.kind}
                      disabled={ids.length === 0 || testingSection !== null}
                      onClick={() => onTestSection(group.kind, ids)}
                      title={t("providers.list.checkGroupHint", { count: ids.length })}
                    >
                      {testingSection === group.kind ? t("providers.testing") : t("providers.list.checkGroup")}
                    </ToolButton>
                  }
                />
                {viewMode === "grid" ? (
                  <div className="cp-grid" style={{ ["--cp-card-min" as string]: "300px" }}>
                    {group.items.map((view) => (
                      <ProviderCard key={view.provider.ID} view={view} handlers={handlers} now={now} />
                    ))}
                  </div>
                ) : (
                  <div className="cp-list pv-list">
                    <div className="cp-list-head" aria-hidden>
                      <span>{t("providers.columns.provider")}</span>
                      <span>{t("providers.columns.kind")}</span>
                      <span>{t("providers.columns.status")}</span>
                      <span>{t("providers.columns.accounts")}</span>
                      <span>{t("providers.columns.checked")}</span>
                      <span />
                    </div>
                    {group.items.map((view) => (
                      <ProviderRow key={view.provider.ID} view={view} handlers={handlers} now={now} />
                    ))}
                  </div>
                )}
              </section>
            );
          })}
        </div>
      )}

      <ProviderCatalogSection
        presets={presets}
        providers={providers}
        credentials={credentials}
        search={search}
        loading={presetsLoading}
        onAddCustom={(type) => onAdd(type)}
      />
    </section>
  );
}
