import { useMemo } from "react";
import type { RequestLog } from "../../hooks/useRequestLogStream";
import { Badge } from "../ui";
import { fmtAbsolute, fmtClock, fmtLatency, statusVariant } from "./utils";

export type RequestSortKey = "created_at" | "status" | "latency_ms" | "path";
export type SortOrder = "asc" | "desc";

type Props = {
  logs: RequestLog[];
  sortKey: RequestSortKey;
  sortOrder: SortOrder;
  onSortKey: (key: RequestSortKey) => void;
  onSortOrder: (order: SortOrder) => void;
};

const COLUMNS: { key: RequestSortKey; label: string; alignRight?: boolean }[] = [
  { key: "created_at", label: "Time" },
  { key: "status", label: "Status", alignRight: true },
  { key: "path", label: "Method · Path" },
  { key: "latency_ms", label: "Latency", alignRight: true },
];

function SortIcon({ active, order }: { active: boolean; order: SortOrder }) {
  if (!active) return <span className="material-symbols-outlined logs-sort-muted">unfold_more</span>;
  return (
    <span className="material-symbols-outlined logs-sort-active">
      {order === "asc" ? "arrow_upward" : "arrow_downward"}
    </span>
  );
}

export function RequestLogsTable({ logs, sortKey, sortOrder, onSortKey, onSortOrder }: Props) {
  const sorted = useMemo(() => {
    const next = [...logs];
    next.sort((a, b) => {
      let cmp = 0;
      switch (sortKey) {
        case "status":
          cmp = (a.status ?? 0) - (b.status ?? 0);
          break;
        case "latency_ms":
          cmp = (a.latency_ms ?? 0) - (b.latency_ms ?? 0);
          break;
        case "path":
          cmp = `${a.method} ${a.path}`.localeCompare(`${b.method} ${b.path}`);
          break;
        case "created_at":
        default:
          cmp = new Date(a.created_at).getTime() - new Date(b.created_at).getTime();
          break;
      }
      return sortOrder === "asc" ? cmp : -cmp;
    });
    return next;
  }, [logs, sortKey, sortOrder]);

  const handleSort = (key: RequestSortKey) => {
    if (key === sortKey) {
      onSortOrder(sortOrder === "asc" ? "desc" : "asc");
    } else {
      onSortKey(key);
      onSortOrder(key === "created_at" || key === "latency_ms" ? "desc" : "asc");
    }
  };

  if (logs.length === 0) {
    return (
      <div className="logs-empty">
        <span className="material-symbols-outlined logs-empty-icon">receipt_long</span>
        <p className="logs-empty-text">No requests logged yet.</p>
        <p className="logs-empty-hint">Live requests will appear here as they happen.</p>
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
            <th>Model · Provider</th>
          </tr>
        </thead>
        <tbody>
          {sorted.map((item) => {
            const keyId = item.client_api_key_id && item.client_api_key_id.length > 0
              ? item.client_api_key_id.slice(0, 8)
              : "local";
            const model = item.public_model_id || item.metadata?.model;
            const provider = item.provider_id || item.metadata?.provider;
            return (
              <tr
                key={`${item.request_id}-${item.created_at}`}
                title={`${item.method} ${item.path}\n${fmtAbsolute(item.created_at)}\nrequest: ${item.request_id}\nkey: ${item.client_api_key_id || "local"}${item.error_code ? `\nerror: ${item.error_code}` : ""}`}
              >
                <td className="logs-time" title={fmtAbsolute(item.created_at)}>
                  <span className="logs-time-clock">{fmtClock(item.created_at)}</span>
                  <span className="logs-time-ago">{item.request_id.slice(0, 8)}</span>
                </td>
                <td className="right">
                  <Badge variant={statusVariant(item.status)} size="sm" dot={Boolean(item.error_code)}>
                    {item.status || "—"}
                  </Badge>
                </td>
                <td className="logs-path-cell">
                  <div className="logs-path-row">
                    <code className="logs-method">{item.method}</code>
                    <span className="logs-path" title={item.path}>{item.path}</span>
                  </div>
                  <div className="logs-path-meta">
                    <span>{keyId}</span>
                    {item.error_code ? <span className="logs-error">{item.error_code}</span> : null}
                  </div>
                </td>
                <td className="right logs-latency" title={`${item.latency_ms ?? 0} ms`}>
                  {fmtLatency(item.latency_ms)}
                </td>
                <td className="logs-model-cell">
                  {model || provider ? (
                    <div className="logs-model">
                      {model ? <span className="logs-model-name">{String(model)}</span> : null}
                      {provider ? <span className="logs-model-provider">{String(provider)}</span> : null}
                    </div>
                  ) : (
                    <span className="logs-muted">—</span>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
