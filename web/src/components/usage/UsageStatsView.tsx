import { useCallback, useEffect, useMemo, useState } from "react";
import { fetchUsageStats } from "./api";
import type { UsagePeriod, UsageStats } from "./api";
import { OverviewCards } from "./OverviewCards";
import { ProviderTopology } from "./ProviderTopology";
import { RecentRequests } from "./RecentRequests";
import { UsageChart } from "./UsageChart";
import { ProviderBadge, UsageTable } from "./UsageTable";
import { useUsageStream } from "./useUsageStream";
import {
  enrichUsageRows,
  fmt,
  fmtTime,
  groupUsageRows,
  sortUsageRows,
  sameUsageLiveSnapshot,
  type UsageTableView,
  type UsageValueMode,
} from "./utils";

type ProviderItem = {
  id: string;
  name: string;
  type: string;
  enabled: boolean;
};

type Props = {
  secret: string;
  period: UsagePeriod;
  providers: ProviderItem[];
  onError: (message: string) => void;
};

const TABLE_OPTIONS: Array<{ value: UsageTableView; label: string }> = [
  { value: "model", label: "Usage by Model" },
  { value: "account", label: "Usage by Credential" },
  { value: "apiKey", label: "Usage by API Key" },
  { value: "endpoint", label: "Usage by Upstream Model" },
];

export function UsageStatsView({ secret, period, providers, onError }: Props) {
  const [stats, setStats] = useState<UsageStats | null>(null);
  const [loading, setLoading] = useState(true);
  const [fetching, setFetching] = useState(false);
  const [tableView, setTableView] = useState<UsageTableView>("model");
  const [viewMode, setViewMode] = useState<UsageValueMode>("costs");
  const [sortBy, setSortBy] = useState("rawModel");
  const [sortOrder, setSortOrder] = useState<"asc" | "desc">("asc");

  useEffect(() => {
    let cancelled = false;
    if (!stats) setLoading(true);
    else setFetching(true);
    fetchUsageStats(secret, period)
      .then((data) => {
        if (!cancelled) setStats(data);
      })
      .catch((error) => {
        if (!cancelled) onError(error instanceof Error ? error.message : "Failed to load usage stats");
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
          setFetching(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [secret, period, onError]);

  const applyLiveUpdate = useCallback((update: Pick<UsageStats, "activeRequests" | "recentRequests" | "errorProvider">) => {
    setStats((current) => {
      if (!current) return current;
      const next = {
        activeRequests: update.activeRequests ?? [],
        recentRequests: update.recentRequests ?? [],
        errorProvider: update.errorProvider,
      };
      if (sameUsageLiveSnapshot(
        {
          activeRequests: current.activeRequests ?? [],
          recentRequests: current.recentRequests ?? [],
          errorProvider: current.errorProvider,
        },
        next,
      )) {
        return current;
      }
      return { ...current, ...next };
    });
  }, []);

  useUsageStream(secret, Boolean(stats), applyLiveUpdate);

  const toggleSort = useCallback((field: string) => {
    setSortBy((current) => {
      if (current === field) {
        setSortOrder((order) => (order === "asc" ? "desc" : "asc"));
        return current;
      }
      setSortOrder("asc");
      return field;
    });
  }, []);

  const tableConfig = useMemo(() => {
    if (!stats) return null;
    const source =
      tableView === "model"
        ? stats.byModel
        : tableView === "account"
          ? stats.byAccount
          : tableView === "apiKey"
            ? stats.byApiKey
            : stats.byEndpoint;
    const rows = sortUsageRows(enrichUsageRows(source), sortBy, sortOrder);
    const groupedData = groupUsageRows(rows, tableView);
    const emptyMessage =
      tableView === "model"
        ? "No usage recorded yet."
        : tableView === "account"
          ? "No credential-specific usage recorded yet."
          : tableView === "apiKey"
            ? "No API key usage recorded yet."
            : "No upstream model usage recorded yet.";

    if (tableView === "model") {
      return {
        columns: [
          { field: "rawModel", label: "Model" },
          { field: "provider", label: "Provider" },
          { field: "requests", label: "Requests", align: "right" as const },
          { field: "lastUsed", label: "Last used", align: "right" as const },
        ],
        groupedData,
        storageKey: "tproxy-usage:expanded-models",
        emptyMessage,
        renderSummaryCells: (group: ReturnType<typeof groupUsageRows>[number]) => (
          <>
            <td>—</td>
            <td className="right">{fmt(group.summary.requests)}</td>
            <td className="right muted">{fmtTime(group.summary.lastUsed)}</td>
          </>
        ),
        renderDetailCells: (item: ReturnType<typeof enrichUsageRows>[number]) => (
          <>
            <td>{item.rawModel}</td>
            <td><ProviderBadge provider={item.provider} /></td>
            <td className="right">{fmt(item.requests)}</td>
            <td className="right muted">{fmtTime(item.lastUsed)}</td>
          </>
        ),
      };
    }

    if (tableView === "account") {
      return {
        columns: [
          { field: "rawModel", label: "Model" },
          { field: "provider", label: "Provider" },
          { field: "accountName", label: "Credential" },
          { field: "requests", label: "Requests", align: "right" as const },
          { field: "lastUsed", label: "Last used", align: "right" as const },
        ],
        groupedData,
        storageKey: "tproxy-usage:expanded-accounts",
        emptyMessage,
        renderSummaryCells: (group: ReturnType<typeof groupUsageRows>[number]) => (
          <>
            <td>—</td>
            <td>—</td>
            <td className="right">{fmt(group.summary.requests)}</td>
            <td className="right muted">{fmtTime(group.summary.lastUsed)}</td>
          </>
        ),
        renderDetailCells: (item: ReturnType<typeof enrichUsageRows>[number]) => (
          <>
            <td>{item.rawModel}</td>
            <td><ProviderBadge provider={item.provider} /></td>
            <td>{item.accountName}</td>
            <td className="right">{fmt(item.requests)}</td>
            <td className="right muted">{fmtTime(item.lastUsed)}</td>
          </>
        ),
      };
    }

    if (tableView === "apiKey") {
      return {
        columns: [
          { field: "keyName", label: "API key" },
          { field: "rawModel", label: "Model" },
          { field: "provider", label: "Provider" },
          { field: "requests", label: "Requests", align: "right" as const },
          { field: "lastUsed", label: "Last used", align: "right" as const },
        ],
        groupedData,
        storageKey: "tproxy-usage:expanded-apikeys",
        emptyMessage,
        renderSummaryCells: (group: ReturnType<typeof groupUsageRows>[number]) => (
          <>
            <td>—</td>
            <td>—</td>
            <td className="right">{fmt(group.summary.requests)}</td>
            <td className="right muted">{fmtTime(group.summary.lastUsed)}</td>
          </>
        ),
        renderDetailCells: (item: ReturnType<typeof enrichUsageRows>[number]) => (
          <>
            <td>{item.keyName}</td>
            <td>{item.rawModel}</td>
            <td><ProviderBadge provider={item.provider} /></td>
            <td className="right">{fmt(item.requests)}</td>
            <td className="right muted">{fmtTime(item.lastUsed)}</td>
          </>
        ),
      };
    }

    return {
      columns: [
        { field: "endpoint", label: "Upstream model" },
        { field: "rawModel", label: "Virtual model" },
        { field: "provider", label: "Provider" },
        { field: "requests", label: "Requests", align: "right" as const },
        { field: "lastUsed", label: "Last used", align: "right" as const },
      ],
      groupedData,
      storageKey: "tproxy-usage:expanded-endpoints",
      emptyMessage,
      renderSummaryCells: (group: ReturnType<typeof groupUsageRows>[number]) => (
        <>
          <td>—</td>
          <td>—</td>
          <td className="right">{fmt(group.summary.requests)}</td>
          <td className="right muted">{fmtTime(group.summary.lastUsed)}</td>
        </>
      ),
      renderDetailCells: (item: ReturnType<typeof enrichUsageRows>[number]) => (
        <>
          <td><code>{item.endpoint}</code></td>
          <td>{item.rawModel}</td>
          <td><ProviderBadge provider={item.provider} /></td>
          <td className="right">{fmt(item.requests)}</td>
          <td className="right muted">{fmtTime(item.lastUsed)}</td>
        </>
      ),
    };
  }, [stats, tableView, sortBy, sortOrder]);

  const recentAside = useMemo(
    () => <RecentRequests requests={stats?.recentRequests ?? []} />,
    [stats?.recentRequests],
  );

  if (!stats && !loading) {
    return <div className="usage-empty">Failed to load usage statistics.</div>;
  }

  if (loading || !stats) {
    return (
      <div className="usage-loading">
        <span className="material-symbols-outlined animate-spin">progress_activity</span>
      </div>
    );
  }

  return (
    <div className="usage-stats-stack">
      {fetching ? (
        <div className="usage-fetching">
          <span className="material-symbols-outlined animate-spin">progress_activity</span>
          Refreshing...
        </div>
      ) : null}
      <OverviewCards stats={stats} />
      <ProviderTopology
        providers={providers}
        activeRequests={stats.activeRequests}
        lastProvider={stats.recentRequests?.[0]?.provider || ""}
        errorProvider={stats.errorProvider || ""}
        aside={recentAside}
      />
      <UsageChart secret={secret} period={period} onError={onError} />
      <div className="usage-table-toolbar">
        <select value={tableView} onChange={(event) => setTableView(event.target.value as UsageTableView)} className="select">
          {TABLE_OPTIONS.map((option) => (
            <option key={option.value} value={option.value}>{option.label}</option>
          ))}
        </select>
        <div className="usage-segmented">
          <button type="button" className={viewMode === "costs" ? "active" : ""} onClick={() => setViewMode("costs")}>Costs</button>
          <button type="button" className={viewMode === "tokens" ? "active" : ""} onClick={() => setViewMode("tokens")}>Tokens</button>
        </div>
      </div>
      {tableConfig ? (
        <UsageTable
          columns={tableConfig.columns}
          groupedData={tableConfig.groupedData}
          sortBy={sortBy}
          sortOrder={sortOrder}
          onToggleSort={toggleSort}
          viewMode={viewMode}
          storageKey={tableConfig.storageKey}
          renderSummaryCells={tableConfig.renderSummaryCells}
          renderDetailCells={tableConfig.renderDetailCells}
          emptyMessage={tableConfig.emptyMessage}
        />
      ) : null}
    </div>
  );
}
