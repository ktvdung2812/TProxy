import { Card } from "../ui";
import { fmt, fmtCost } from "./utils";
import type { UsageStats } from "./api";

type Props = {
  stats: UsageStats;
};

export function OverviewCards({ stats }: Props) {
  return (
    <div className="usage-overview-grid">
      <Card pad="md" className="usage-stat-card">
        <span className="usage-stat-label">Total Requests</span>
        <strong className="usage-stat-value">{fmt(stats.totalRequests)}</strong>
      </Card>
      <Card pad="md" className="usage-stat-card">
        <span className="usage-stat-label">Total Input Tokens</span>
        <strong className="usage-stat-value usage-stat-primary">{fmt(stats.totalPromptTokens)}</strong>
      </Card>
      <Card pad="md" className="usage-stat-card">
        <span className="usage-stat-label">Cached Tokens</span>
        <strong className="usage-stat-value usage-stat-info">{fmt(stats.totalCachedTokens)}</strong>
      </Card>
      <Card pad="md" className="usage-stat-card">
        <span className="usage-stat-label">Output Tokens</span>
        <strong className="usage-stat-value usage-stat-success">{fmt(stats.totalCompletionTokens)}</strong>
      </Card>
      <Card pad="md" className="usage-stat-card">
        <span className="usage-stat-label">Est. Cost</span>
        <strong className="usage-stat-value usage-stat-warning">~{fmtCost(stats.totalCost)}</strong>
        <small className="usage-stat-hint">Estimated, not actual billing</small>
      </Card>
    </div>
  );
}
