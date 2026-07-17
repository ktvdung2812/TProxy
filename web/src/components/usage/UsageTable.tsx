import { Fragment, useEffect, useState, type ReactNode } from "react";
import { Badge, Card } from "../ui";
import { fmt, fmtCost, fmtTime, type UsageGroup, type UsageValueMode } from "./utils";

type Column = {
  field: string;
  label: string;
  align?: "right";
};

type Props = {
  columns: Column[];
  groupedData: UsageGroup[];
  sortBy: string;
  sortOrder: "asc" | "desc";
  onToggleSort: (field: string) => void;
  viewMode: UsageValueMode;
  storageKey: string;
  renderSummaryCells: (group: UsageGroup) => ReactNode;
  renderDetailCells: (item: UsageGroup["items"][number]) => ReactNode;
  emptyMessage: string;
};

function SortIcon({ field, sortBy, sortOrder }: { field: string; sortBy: string; sortOrder: "asc" | "desc" }) {
  if (sortBy !== field) return <span className="usage-sort-muted">↕</span>;
  return <span>{sortOrder === "asc" ? "↑" : "↓"}</span>;
}

function ValueCells({ item, viewMode, isSummary = false }: { item: UsageGroup["summary"]; viewMode: UsageValueMode; isSummary?: boolean }) {
  if (viewMode === "tokens") {
    return (
      <>
        <td>{isSummary && item.promptTokens === undefined ? "—" : fmt(item.promptTokens)}</td>
        <td>{item.cachedTokens ? fmt(item.cachedTokens) : "—"}</td>
        <td>{isSummary && item.completionTokens === undefined ? "—" : fmt(item.completionTokens)}</td>
        <td className="strong">{fmt(item.totalTokens)}</td>
      </>
    );
  }
  return (
    <>
      <td>{isSummary && item.inputCost === undefined ? "—" : fmtCost(item.inputCost)}</td>
      <td>{item.cachedCost ? fmtCost(item.cachedCost) : "—"}</td>
      <td>{isSummary && item.outputCost === undefined ? "—" : fmtCost(item.outputCost)}</td>
      <td className="strong warning">{fmtCost(item.totalCost || item.cost)}</td>
    </>
  );
}

export function UsageTable({
  columns,
  groupedData,
  sortBy,
  sortOrder,
  onToggleSort,
  viewMode,
  storageKey,
  renderSummaryCells,
  renderDetailCells,
  emptyMessage,
}: Props) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  useEffect(() => {
    try {
      const saved = window.localStorage.getItem(storageKey);
      if (saved) setExpanded(new Set(JSON.parse(saved) as string[]));
    } catch {
      setExpanded(new Set());
    }
  }, [storageKey]);

  useEffect(() => {
    try {
      window.localStorage.setItem(storageKey, JSON.stringify([...expanded]));
    } catch {
      // ignore storage failures
    }
  }, [expanded, storageKey]);

  const valueColumns =
    viewMode === "tokens"
      ? ["Input Tokens", "Saved", "Output Tokens", "Total Tokens"]
      : ["Input Cost", "Saved Cost", "Output Cost", "Total Cost"];

  return (
    <Card pad="none" className="usage-table-card">
      <div className="usage-table-scroll">
        <table className="usage-table">
          <thead>
            <tr>
              {columns.map((column) => (
                <th key={column.field} className={column.align === "right" ? "right" : ""} onClick={() => onToggleSort(column.field)}>
                  {column.label} <SortIcon field={column.field} sortBy={sortBy} sortOrder={sortOrder} />
                </th>
              ))}
              {valueColumns.map((label) => (
                <th key={label} className="right">
                  {label}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {groupedData.map((group) => (
              <Fragment key={group.groupKey}>
                <tr className="summary" onClick={() => setExpanded((current) => {
                  const next = new Set(current);
                  if (next.has(group.groupKey)) next.delete(group.groupKey);
                  else next.add(group.groupKey);
                  return next;
                })}>
                  <td>
                    <div className="usage-group-label">
                      <span className={`material-symbols-outlined chevron ${expanded.has(group.groupKey) ? "open" : ""}`}>chevron_right</span>
                      <span>{group.groupKey}</span>
                    </div>
                  </td>
                  {renderSummaryCells(group)}
                  <ValueCells item={group.summary} viewMode={viewMode} isSummary />
                </tr>
                {expanded.has(group.groupKey)
                  ? group.items.map((item) => (
                      <tr key={item.key} className="detail">
                        {renderDetailCells(item)}
                        <ValueCells item={item} viewMode={viewMode} />
                      </tr>
                    ))
                  : null}
              </Fragment>
            ))}
            {groupedData.length === 0 ? (
              <tr>
                <td colSpan={columns.length + valueColumns.length} className="empty">
                  {emptyMessage}
                </td>
              </tr>
            ) : null}
          </tbody>
        </table>
      </div>
    </Card>
  );
}

export function ProviderBadge({ provider }: { provider?: string }) {
  return <Badge variant="default" size="sm">{provider || "unknown"}</Badge>;
}

export { fmtTime };
