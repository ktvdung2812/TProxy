import { Card } from "../ui";
import { fmt, fmtCompact, fmtCost } from "./utils";
import type { UsageStats } from "./api";

type Props = {
  stats: UsageStats;
};

function UsageCount({ value, className = "" }: { value: number; className?: string }) {
  const exact = fmt(value);
  return (
    <>
      <strong className={`usage-stat-value ${className}`.trim()} aria-label={exact}>
        {fmtCompact(value)}
      </strong>
      <small className="usage-stat-exact" aria-hidden="true">{exact}</small>
    </>
  );
}

export function OverviewCards({ stats }: Props) {
  return (
    <div className="usage-overview-grid">
      <Card pad="md" className="usage-stat-card">
        <span className="usage-stat-label">Total Requests</span>
        <UsageCount value={stats.totalRequests} />
      </Card>
      <Card pad="md" className="usage-stat-card">
        <span className="usage-stat-label">Total Input Tokens</span>
        <UsageCount value={stats.totalPromptTokens} className="usage-stat-primary" />
      </Card>
      <Card pad="md" className="usage-stat-card">
        <span className="usage-stat-label">Cached Tokens</span>
        <UsageCount value={stats.totalCachedTokens} className="usage-stat-info" />
      </Card>
      <Card pad="md" className="usage-stat-card">
        <span className="usage-stat-label">Output Tokens</span>
        <UsageCount value={stats.totalCompletionTokens} className="usage-stat-success" />
      </Card>
      <Card pad="md" className="usage-stat-card">
        <span className="usage-stat-label">Est. Cost</span>
        <strong className="usage-stat-value usage-stat-warning">~{fmtCost(stats.totalCost)}</strong>
        <small className="usage-stat-hint">Estimated, not actual billing</small>
      </Card>
    </div>
  );
}
