import { useMemo, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { PageNote } from "../page-kit";
import { cn } from "../ui";
import type { UsagePeriod, UsageStats } from "./api";
import { ProviderUsagePanel, type ProviderItem } from "./ProviderUsageList";
import { RecentRequests } from "./RecentRequests";
import { UsageChart } from "./UsageChart";
import { UsageTable } from "./UsageTable";
import type { RecentFilter } from "./UsageView";
import type { UsageSortField, UsageTableView, UsageValueMode } from "./utils";

type Props = {
  secret: string;
  period: UsagePeriod;
  stats: UsageStats;
  refetching: boolean;
  reloadToken: number;
  providers: ProviderItem[];
  valueMode: UsageValueMode;
  tableView: UsageTableView;
  sort: { field: UsageSortField; order: "asc" | "desc" };
  onSort: (sort: { field: UsageSortField; order: "asc" | "desc" }) => void;
  query: string;
  providerFilter: string;
  onProviderFilter: (value: string) => void;
  recentFilter: RecentFilter;
  onRecentFilter: (value: RecentFilter) => void;
  streamPill: ReactNode;
  hasFilters: boolean;
  onClearFilters: () => void;
  onError: (message: string) => void;
};

/** Overview tab: traffic over time, where it went, what is happening now, and who used what. */
export function UsageStatsView({
  secret,
  period,
  stats,
  refetching,
  reloadToken,
  providers,
  valueMode,
  tableView,
  sort,
  onSort,
  query,
  providerFilter,
  onProviderFilter,
  recentFilter,
  onRecentFilter,
  streamPill,
  hasFilters,
  onClearFilters,
  onError,
}: Props) {
  const { t } = useTranslation();
  const providerLabel = useMemo(() => {
    if (!providerFilter) return "";
    return stats.byProvider[providerFilter]?.provider || providers.find((item) => item.id === providerFilter)?.name || providerFilter;
  }, [providerFilter, stats.byProvider, providers]);

  return (
    // While a new period loads, the previous numbers stay in place, dimmed, so nothing jumps.
    <div className={cn("us-overview", refetching && "is-refetching")} aria-busy={refetching}>
      <UsageChart secret={secret} period={period} valueMode={valueMode} reloadToken={reloadToken} onError={onError} />

      <div className="us-split">
        <ProviderUsagePanel
          stats={stats}
          providers={providers}
          valueMode={valueMode}
          selected={providerFilter}
          onSelect={(id) => onProviderFilter(id === providerFilter ? "" : id)}
        />
        <RecentRequests
          requests={stats.recentRequests ?? []}
          activeRequests={stats.activeRequests ?? []}
          providers={providers}
          filter={recentFilter}
          onFilter={onRecentFilter}
          streamPill={streamPill}
        />
      </div>

      <UsageTable
        stats={stats}
        tableView={tableView}
        valueMode={valueMode}
        sort={sort}
        onSort={onSort}
        query={query}
        providerId={providerFilter}
        providerLabel={providerLabel}
        onClearProvider={() => onProviderFilter("")}
        hasFilters={hasFilters}
        onClearFilters={onClearFilters}
      />
      <PageNote>{t("usage.costNote")}</PageNote>
    </div>
  );
}
