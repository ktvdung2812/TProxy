import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router-dom";
import {
  EmptyPanel,
  FilterTabs,
  GuideToggle,
  LegendItem,
  PageGuide,
  PageStats,
  SearchField,
  Segmented,
  SelectField,
  StatusPill,
  Toolbar,
  ToolbarEnd,
  ToolbarRow,
  usePageGuide,
  useStoredState,
  type Tone,
} from "../page-kit";
import type { Credential } from "../providers/types";
import { Button } from "../ui";
import { buildUsageStatItems } from "./OverviewCards";
import { RequestDetailsTab, type LogStatusFilter } from "./RequestDetailsTab";
import { UsageStatsView } from "./UsageStatsView";
import { fetchUsageStats, type UsagePeriod, type UsageStats } from "./api";
import { filterConnectedProviders } from "./providerConnections";
import { useUsageStream, type UsageLiveUpdate, type UsageStreamState } from "./useUsageStream";
import {
  USAGE_PERIODS,
  USAGE_TABLE_VIEWS,
  isOkStatus,
  sameUsageLiveSnapshot,
  type UsageSortField,
  type UsageTableView,
  type UsageValueMode,
} from "./utils";

type ProviderItem = {
  ID: string;
  Name: string;
  Type: string;
  Enabled: boolean;
};

type Props = {
  secret: string;
  providers: ProviderItem[] | null;
  credentials?: Record<string, Credential[]>;
  apiKeys?: Array<{ id: string; name: string }>;
  onError: (message: string) => void;
};

type UsageTab = "overview" | "details";
export type RecentFilter = "all" | "errors";

const VALUE_MODES: UsageValueMode[] = ["costs", "tokens"];
const SORT_PRESETS: Array<{ field: UsageSortField; order: "asc" | "desc" }> = [
  { field: "cost", order: "desc" },
  { field: "tokens", order: "desc" },
  { field: "requests", order: "desc" },
  { field: "lastUsed", order: "desc" },
  { field: "name", order: "asc" },
];

const STREAM_TONE: Record<UsageStreamState, Tone> = { off: "muted", connecting: "info", live: "success", retrying: "warning" };

export function UsageView({ secret, providers, credentials = {}, apiKeys = [], onError }: Props) {
  const { t } = useTranslation();
  const guide = usePageGuide("usage");
  const [searchParams, setSearchParams] = useSearchParams();
  const [period, setPeriod] = useState<UsagePeriod>("today");
  const [stats, setStats] = useState<UsageStats | null>(null);
  const [statsError, setStatsError] = useState("");
  const [fetching, setFetching] = useState(false);
  const [reloadToken, setReloadToken] = useState(0);

  // Overview controls.
  const [valueMode, setValueMode] = useStoredState<UsageValueMode>("tproxy.usage.valueMode", "costs", VALUE_MODES);
  const [tableView, setTableView] = useStoredState<UsageTableView>("tproxy.usage.groupBy", "model", USAGE_TABLE_VIEWS);
  const [sort, setSort] = useState<{ field: UsageSortField; order: "asc" | "desc" }>(SORT_PRESETS[0]);
  const [query, setQuery] = useState("");
  const [providerFilter, setProviderFilter] = useState("");
  const [recentFilter, setRecentFilter] = useState<RecentFilter>("all");

  // Request log controls.
  const [logProvider, setLogProvider] = useState("");
  const [logStatus, setLogStatus] = useState<LogStatusFilter>("all");
  const [logQuery, setLogQuery] = useState("");
  const [logPage, setLogPage] = useState(1);
  const [logTotal, setLogTotal] = useState<number | null>(null);

  const activeTab: UsageTab = searchParams.get("tab") === "details" ? "details" : "overview";

  useEffect(() => {
    let cancelled = false;
    setFetching(true);
    fetchUsageStats(secret, period)
      .then((data) => {
        if (cancelled) return;
        setStats(data);
        setStatsError("");
      })
      .catch((error) => {
        if (cancelled) return;
        const message = error instanceof Error ? error.message : t("usage.failedToLoad");
        setStatsError(message);
        onError(message);
      })
      .finally(() => {
        if (!cancelled) setFetching(false);
      });
    return () => {
      cancelled = true;
    };
    // t is stable per language; refetching on a language switch is not wanted.
  }, [secret, period, onError, reloadToken]);

  const applyLiveUpdate = useCallback((update: UsageLiveUpdate) => {
    setStats((current) => {
      if (!current) return current;
      const next = {
        activeRequests: update.activeRequests ?? [],
        recentRequests: update.recentRequests ?? [],
        errorProvider: update.errorProvider,
      };
      if (sameUsageLiveSnapshot(
        { activeRequests: current.activeRequests ?? [], recentRequests: current.recentRequests ?? [], errorProvider: current.errorProvider },
        next,
      )) {
        return current;
      }
      return { ...current, ...next };
    });
  }, []);

  const streamState = useUsageStream(secret, Boolean(stats), applyLiveUpdate);

  const providerItems = useMemo(
    () => (providers || []).map((provider) => ({ id: provider.ID, name: provider.Name, type: provider.Type, enabled: provider.Enabled })),
    [providers],
  );
  const connectedProviders = useMemo(() => filterConnectedProviders(providerItems, credentials), [providerItems, credentials]);
  const providerNames = useMemo(() => {
    const names: Record<string, string> = {};
    for (const provider of providerItems) names[provider.id] = provider.name || provider.id;
    return names;
  }, [providerItems]);

  const setTab = (tab: UsageTab) => {
    const next = new URLSearchParams(searchParams);
    next.set("tab", tab);
    setSearchParams(next, { replace: true });
  };

  const reload = () => setReloadToken((value) => value + 1);
  const recent = stats?.recentRequests ?? [];
  const recentErrors = recent.filter((item) => !isOkStatus(item.status)).length;

  const statItems = stats
    ? buildUsageStatItems({
        stats,
        t,
        recentErrors,
        errorsActive: activeTab === "overview" ? recentFilter === "errors" : logStatus === "errors",
        onErrorsClick: () => {
          if (activeTab === "overview") setRecentFilter((value) => (value === "errors" ? "all" : "errors"));
          else {
            setLogStatus((value) => (value === "errors" ? "all" : "errors"));
            setLogPage(1);
          }
        },
      })
    : [];

  const sortValue = `${sort.field}-${sort.order}`;
  const sortOptions = SORT_PRESETS.some((item) => item.field === sort.field && item.order === sort.order)
    ? SORT_PRESETS
    : [...SORT_PRESETS, sort];

  const hasOverviewFilters = query.trim() !== "" || providerFilter !== "";
  const clearOverviewFilters = () => {
    setQuery("");
    setProviderFilter("");
    setRecentFilter("all");
  };

  const streamPill = (
    <StatusPill tone={STREAM_TONE[streamState]} loading={streamState === "connecting" || streamState === "retrying"} title={t("usage.stream.hint")}>
      {t(`usage.stream.${streamState}`)}
    </StatusPill>
  );

  return (
    <section className="cp-page us-page">
      {stats ? (
        <PageStats label={t("usage.stats.label")} items={statItems} />
      ) : statsError ? null : (
        <div className="cp-skeleton us-stats-skeleton" aria-hidden>
          <span />
          <span />
        </div>
      )}

      <Toolbar>
        <ToolbarRow>
          <FilterTabs
            label={t("usage.toolbar.view")}
            value={activeTab}
            onChange={setTab}
            items={[
              { value: "overview", icon: "insights", label: t("usage.overview") },
              { value: "details", icon: "receipt_long", label: t("usage.toolbar.requestLog"), count: logTotal ?? undefined },
            ]}
          />
          <ToolbarEnd>
            <Segmented
              label={t("usage.toolbar.period")}
              value={period}
              onChange={setPeriod}
              items={USAGE_PERIODS.map((value) => ({ value, label: t(`usage.period.${value}`) }))}
            />
            <Button variant="primary" size="sm" icon="refresh" loading={fetching} onClick={reload}>
              {t("common.refresh")}
            </Button>
          </ToolbarEnd>
        </ToolbarRow>
        {activeTab === "overview" ? (
          <ToolbarRow>
            <SearchField value={query} onChange={setQuery} placeholder={t("usage.toolbar.search")} />
            <ToolbarEnd>
              <SelectField
                icon="category"
                label={t("usage.toolbar.groupBy")}
                value={tableView}
                onChange={setTableView}
                options={USAGE_TABLE_VIEWS.map((value) => ({ value, label: t(`usage.groupBy.${value}`) }))}
              />
              <SelectField
                label={t("usage.toolbar.sort")}
                value={sortValue}
                onChange={(value) => {
                  const [field, order] = value.split("-") as [UsageSortField, "asc" | "desc"];
                  setSort({ field, order });
                }}
                options={sortOptions.map((item) => ({ value: `${item.field}-${item.order}`, label: t(`usage.sort.${item.field}_${item.order}`) }))}
              />
              <Segmented
                label={t("usage.toolbar.measure")}
                value={valueMode}
                onChange={setValueMode}
                items={[
                  { value: "costs", label: t("usage.cost") },
                  { value: "tokens", label: t("usage.tokens") },
                ]}
              />
              <GuideToggle open={guide.open} onToggle={guide.toggle} />
            </ToolbarEnd>
          </ToolbarRow>
        ) : (
          <ToolbarRow>
            <SearchField value={logQuery} onChange={setLogQuery} placeholder={t("usage.log.search")} />
            <ToolbarEnd>
              <SelectField
                icon="dns"
                label={t("usage.provider")}
                value={logProvider}
                onChange={(value) => {
                  setLogProvider(value);
                  setLogPage(1);
                }}
                options={[
                  { value: "", label: t("usage.log.allProviders") },
                  ...providerItems.map((provider) => ({ value: provider.id, label: providerNames[provider.id] })),
                ]}
              />
              <SelectField
                icon="filter_alt"
                label={t("usage.status")}
                value={logStatus}
                onChange={setLogStatus}
                options={(["all", "errors", "ok"] as LogStatusFilter[]).map((value) => ({ value, label: t(`usage.log.status.${value}`) }))}
              />
              <GuideToggle open={guide.open} onToggle={guide.toggle} />
            </ToolbarEnd>
          </ToolbarRow>
        )}
      </Toolbar>

      <PageGuide
        open={guide.open}
        onDismiss={guide.dismiss}
        title={t("usage.guide.title")}
        steps={[
          { icon: "date_range", text: t("usage.guide.range") },
          { icon: "hub", text: t("usage.guide.providers") },
          { icon: "receipt_long", text: t("usage.guide.breakdown") },
        ]}
        legend={
          <>
            <LegendItem tone="primary">{t("usage.guide.legendVolume")}</LegendItem>
            <LegendItem tone="success">{t("usage.guide.legendOk")}</LegendItem>
            <LegendItem tone="danger">{t("usage.guide.legendError")}</LegendItem>
          </>
        }
      />

      {activeTab === "overview" ? (
        stats ? (
          <UsageStatsView
            secret={secret}
            period={period}
            stats={stats}
            refetching={fetching}
            reloadToken={reloadToken}
            providers={connectedProviders}
            valueMode={valueMode}
            tableView={tableView}
            sort={sort}
            onSort={setSort}
            query={query}
            providerFilter={providerFilter}
            onProviderFilter={setProviderFilter}
            recentFilter={recentFilter}
            onRecentFilter={setRecentFilter}
            streamPill={streamPill}
            hasFilters={hasOverviewFilters}
            onClearFilters={clearOverviewFilters}
            onError={onError}
          />
        ) : statsError ? (
          <EmptyPanel
            icon="cloud_off"
            title={t("usage.failedToLoad")}
            description={<span role="alert">{statsError}</span>}
            action={<Button variant="secondary" icon="refresh" loading={fetching} onClick={reload}>{t("usage.retry")}</Button>}
          />
        ) : (
          <EmptyPanel loading title={t("common.loading")} />
        )
      ) : (
        <RequestDetailsTab
          secret={secret}
          providerNames={providerNames}
          providerTypes={Object.fromEntries(providerItems.map((provider) => [provider.id, provider.type]))}
          providerItems={providerItems}
          credentials={credentials}
          apiKeys={apiKeys}
          providerFilter={logProvider}
          statusFilter={logStatus}
          query={logQuery}
          page={logPage}
          onPage={setLogPage}
          onTotal={setLogTotal}
          reloadToken={reloadToken}
          onClearFilters={() => {
            setLogProvider("");
            setLogStatus("all");
            setLogQuery("");
            setLogPage(1);
          }}
          onError={onError}
        />
      )}
    </section>
  );
}
