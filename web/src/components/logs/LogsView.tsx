import { useEffect, useMemo, useState } from "react";
import type { RequestLog } from "../../hooks/useRequestLogStream";
import { Badge, Card, Input, Select } from "../ui";
import type { AuditEvent } from "./api";
import { AuditTable, type AuditSortKey } from "./AuditTable";
import { RequestLogsTable, type RequestSortKey } from "./RequestLogsTable";
import { fmtLatency, matchesQuery, percentile, statusBucket, type SortOrder } from "./utils";

type Tab = "requests" | "audit";

type Props = {
  logs: RequestLog[];
  audit: AuditEvent[];
  streaming: boolean;
  onRefreshAudit: () => void | Promise<void>;
};

type StatusFilter = "all" | "2xx" | "3xx" | "4xx" | "5xx" | "other";

export function LogsView({ logs, audit, streaming, onRefreshAudit }: Props) {
  const [tab, setTab] = useState<Tab>("requests");
  const [query, setQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");

  const [reqSortKey, setReqSortKey] = useState<RequestSortKey>("created_at");
  const [reqSortOrder, setReqSortOrder] = useState<SortOrder>("desc");
  const [auditSortKey, setAuditSortKey] = useState<AuditSortKey>("created_at");
  const [auditSortOrder, setAuditSortOrder] = useState<SortOrder>("desc");

  // Auto-refresh audit events every 30s while the Audit tab is visible.
  useEffect(() => {
    if (tab !== "audit") return undefined;
    const timer = window.setInterval(() => {
      void onRefreshAudit();
    }, 30_000);
    return () => window.clearInterval(timer);
  }, [tab, onRefreshAudit]);

  const filteredLogs = useMemo(() => {
    const q = query.trim();
    return logs.filter((item) => {
      if (statusFilter !== "all" && statusBucket(item.status) !== statusFilter) return false;
      if (!q) return true;
      return (
        matchesQuery(item.path, q) ||
        matchesQuery(item.method, q) ||
        matchesQuery(item.public_model_id, q) ||
        matchesQuery(item.provider_id, q) ||
        matchesQuery(item.client_api_key_id, q) ||
        matchesQuery(item.error_code, q)
      );
    });
  }, [logs, query, statusFilter]);

  const filteredAudit = useMemo(() => {
    const q = query.trim();
    return audit.filter((item) => {
      if (!q) return true;
      return (
        matchesQuery(item.action, q) ||
        matchesQuery(item.resource_type, q) ||
        matchesQuery(item.resource_id, q) ||
        matchesQuery(item.actor, q)
      );
    });
  }, [audit, query]);

  const stats = useMemo(() => {
    const total = logs.length;
    const ok = logs.filter((item) => item.status >= 200 && item.status < 400).length;
    const errors = logs.filter((item) => item.status >= 400).length;
    const latencies = logs.map((item) => item.latency_ms ?? 0);
    const successRate = total > 0 ? Math.round((ok / total) * 1000) / 10 : 0;
    return {
      total,
      successRate,
      errors,
      p50: percentile(latencies, 50),
      p95: percentile(latencies, 95),
    };
  }, [logs]);

  return (
    <section className="section">
      <div className="logs-stats">
        <StatCard icon="receipt_long" label="Total requests" value={String(stats.total)} hint={streaming ? "Live" : "Paused"} live={streaming} />
        <StatCard icon="check_circle" label="Success rate" value={`${stats.successRate}%`} hint={`${stats.total - stats.errors} ok`} tone="success" />
        <StatCard icon="error" label="Errors" value={String(stats.errors)} hint={stats.errors === 0 ? "No errors" : "4xx / 5xx"} tone={stats.errors > 0 ? "error" : undefined} />
        <StatCard icon="timer" label="Latency p50 · p95" value={`${fmtLatency(stats.p50)} · ${fmtLatency(stats.p95)}`} hint="of recent" />
      </div>

      <Card pad="none" className="logs-card">
        <div className="logs-toolbar">
          <div className="usage-segmented">
            <button
              type="button"
              className={tab === "requests" ? "active" : ""}
              onClick={() => setTab("requests")}
            >
              <span className="material-symbols-outlined logs-tab-icon">receipt_long</span>
              Requests
              <Badge size="sm" variant={tab === "requests" ? "primary" : "default"}>{stats.total}</Badge>
            </button>
            <button
              type="button"
              className={tab === "audit" ? "active" : ""}
              onClick={() => setTab("audit")}
            >
              <span className="material-symbols-outlined logs-tab-icon">history</span>
              Audit
              <Badge size="sm" variant={tab === "audit" ? "primary" : "default"}>{audit.length}</Badge>
            </button>
          </div>

          <div className="logs-filter">
            <Input
              icon="search"
              placeholder={tab === "requests" ? "Filter path, model, key…" : "Filter action, resource, actor…"}
              value={query}
              onChange={(e) => setQuery((e.target as HTMLInputElement).value)}
              aria-label="Filter logs"
            />
            {tab === "requests" ? (
              <Select
                value={statusFilter}
                onChange={(e) => setStatusFilter((e.target as HTMLSelectElement).value as StatusFilter)}
                aria-label="Filter by status"
              >
                <option value="all">All status</option>
                <option value="2xx">2xx success</option>
                <option value="3xx">3xx redirect</option>
                <option value="4xx">4xx client</option>
                <option value="5xx">5xx server</option>
                <option value="other">Other</option>
              </Select>
            ) : null}
          </div>
        </div>

        <div className="logs-card-body">
          {tab === "requests" ? (
            <RequestLogsTable
              logs={filteredLogs}
              sortKey={reqSortKey}
              sortOrder={reqSortOrder}
              onSortKey={setReqSortKey}
              onSortOrder={setReqSortOrder}
            />
          ) : (
            <AuditTable
              events={filteredAudit}
              sortKey={auditSortKey}
              sortOrder={auditSortOrder}
              onSortKey={setAuditSortKey}
              onSortOrder={setAuditSortOrder}
            />
          )}
        </div>
      </Card>
    </section>
  );
}

function StatCard({
  icon,
  label,
  value,
  hint,
  tone,
  live,
}: {
  icon: string;
  label: string;
  value: string;
  hint?: string;
  tone?: "success" | "error" | "warning";
  live?: boolean;
}) {
  const toneClass = tone ? ` logs-stat-${tone}` : "";
  return (
    <Card pad="md" elev className={`logs-stat${toneClass}`}>
      <div className="logs-stat-head">
        <span className="logs-stat-icon">
          <span className="material-symbols-outlined">{icon}</span>
        </span>
        {live ? <span className="logs-live" title="Live streaming">●</span> : null}
      </div>
      <span className="logs-stat-label">{label}</span>
      <strong className="logs-stat-value">{value}</strong>
      {hint ? <span className="logs-stat-hint">{hint}</span> : null}
    </Card>
  );
}
