import { useMemo } from "react";
import { Badge } from "../ui";
import type { AuditEvent } from "./api";
import { fmtAbsolute, fmtClock, statusVariant } from "./utils";

export type AuditSortKey = "created_at" | "action" | "status";
export type SortOrder = "asc" | "desc";

type Props = {
  events: AuditEvent[];
  sortKey: AuditSortKey;
  sortOrder: SortOrder;
  onSortKey: (key: AuditSortKey) => void;
  onSortOrder: (order: SortOrder) => void;
};

const COLUMNS: { key: AuditSortKey; label: string; alignRight?: boolean }[] = [
  { key: "created_at", label: "Time" },
  { key: "status", label: "Status", alignRight: true },
  { key: "action", label: "Action · Resource" },
];

function SortIcon({ active, order }: { active: boolean; order: SortOrder }) {
  if (!active) return <span className="material-symbols-outlined logs-sort-muted">unfold_more</span>;
  return (
    <span className="material-symbols-outlined logs-sort-active">
      {order === "asc" ? "arrow_upward" : "arrow_downward"}
    </span>
  );
}

export function AuditTable({ events, sortKey, sortOrder, onSortKey, onSortOrder }: Props) {
  const sorted = useMemo(() => {
    const next = [...events];
    next.sort((a, b) => {
      let cmp = 0;
      switch (sortKey) {
        case "status":
          cmp = (a.status ?? 0) - (b.status ?? 0);
          break;
        case "action":
          cmp = (a.action || "").localeCompare(b.action || "");
          break;
        case "created_at":
        default:
          cmp = new Date(a.created_at).getTime() - new Date(b.created_at).getTime();
          break;
      }
      return sortOrder === "asc" ? cmp : -cmp;
    });
    return next;
  }, [events, sortKey, sortOrder]);

  const handleSort = (key: AuditSortKey) => {
    if (key === sortKey) {
      onSortOrder(sortOrder === "asc" ? "desc" : "asc");
    } else {
      onSortKey(key);
      onSortOrder(key === "created_at" ? "desc" : "asc");
    }
  };

  if (events.length === 0) {
    return (
      <div className="logs-empty">
        <span className="material-symbols-outlined logs-empty-icon">history</span>
        <p className="logs-empty-text">No audit events yet.</p>
        <p className="logs-empty-hint">Admin actions will be recorded here.</p>
      </div>
    );
  }

  return (
    <div className="logs-table-scroll custom-scrollbar">
      <table className="logs-table">
        <thead>
          <tr>
            {COLUMNS.map((col) => (
              <th
                key={col.key}
                className={col.alignRight ? "right sortable" : "sortable"}
                onClick={() => handleSort(col.key)}
                title={`Sort by ${col.label}`}
              >
                <span className="logs-th-inner">
                  {col.label}
                  <SortIcon active={sortKey === col.key} order={sortOrder} />
                </span>
              </th>
            ))}
            <th>Actor</th>
          </tr>
        </thead>
        <tbody>
          {sorted.map((item, index) => (
            <tr
              key={`${item.action}-${item.created_at}-${item.id ?? index}`}
              title={fmtAbsolute(item.created_at)}
            >
              <td className="logs-time" title={fmtAbsolute(item.created_at)}>
                <span className="logs-time-clock">{fmtClock(item.created_at)}</span>
                <span className="logs-time-ago">{item.action}</span>
              </td>
              <td className="right">
                <Badge variant={statusVariant(item.status)} size="sm" dot>
                  {item.status || "—"}
                </Badge>
              </td>
              <td className="logs-path-cell">
                <div className="logs-path-row">
                  <code className="logs-method">{item.action}</code>
                  <span className="logs-path" title={item.resource_id || item.resource_type}>
                    {item.resource_type || item.resource_id || "—"}
                  </span>
                </div>
                {item.resource_id ? (
                  <div className="logs-path-meta">
                    <span title={item.resource_id}>{item.resource_id}</span>
                  </div>
                ) : null}
              </td>
              <td className="logs-model-cell">
                {item.actor ? <span className="logs-model-name">{item.actor}</span> : <span className="logs-muted">—</span>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
