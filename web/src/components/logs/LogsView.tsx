import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import type { RequestLog } from "../../hooks/useRequestLogStream";
import { useAccountMask } from "../../hooks/useAccountMask";
import { maskAccountText } from "../../lib/accountMask";
import { Button, cn } from "../ui";
import {
  AutoRefreshToggle,
  EmptyPanel,
  FilterTabs,
  GuideToggle,
  LegendItem,
  LinkButton,
  PageGuide,
  PageNote,
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
  type ActionMenuItem,
  type PageStat,
} from "../page-kit";
import type { AuditEvent } from "./api";
import { AuditList, sortAuditEvents, type AuditSortKey } from "./AuditTable";
import { AuditDetailModal, RequestLogDetailModal } from "./LogDetailModal";
import { RequestLogList, type RequestLogResolvers } from "./RequestLogList";
import {
  average,
  fmtAgo,
  fmtLatency,
  isErrorLog,
  isRateLimitedLog,
  matchesQuery,
  modelOf,
  percentile,
  protocolKey,
  providerOf,
  requestLogKey,
  sortRequestLogs,
  type RequestSortKey,
  type SortOrder,
} from "./utils";

type View = "requests" | "audit";
type StatusFilter = "all" | "errors" | "rate-limited";
type AuditStatusFilter = "all" | "failed";
type RequestSort = "newest" | "oldest" | "slowest" | "status";
type AuditSort = "newest" | "oldest" | "action" | "status";

const REQUEST_SORTS: Record<RequestSort, [RequestSortKey, SortOrder]> = {
  newest: ["created_at", "desc"],
  oldest: ["created_at", "asc"],
  slowest: ["latency_ms", "desc"],
  status: ["status", "desc"],
};
const AUDIT_SORTS: Record<AuditSort, [AuditSortKey, SortOrder]> = {
  newest: ["created_at", "desc"],
  oldest: ["created_at", "asc"],
  action: ["action", "asc"],
  status: ["status", "desc"],
};
const AUDIT_REFRESH_MS = 30_000;
const VIEWS = ["requests", "audit"] as const;

/** Known protocol ids get a readable name; anything else is shown as sent. */
const PROTOCOL_LABELS: Record<string, string> = {
  openai: "OpenAI",
  anthropic: "Anthropic",
  gemini: "Gemini",
  responses: "Responses",
  "responses-websocket": "Responses WS",
};

type AccountRef = { id: string; label?: string; email?: string };

type Props = {
  logs: RequestLog[];
  audit: AuditEvent[];
  streaming: boolean;
  onRefreshAudit: () => void | Promise<void>;
  /** Snapshot accounts and keys, used to show names instead of ids. */
  credentials?: Record<string, AccountRef[]>;
  apiKeys?: Array<{ id: string; name: string }>;
};

function LiveToggle({ live, pending, onToggle }: { live: boolean; pending: number; onToggle: () => void }) {
  const { t } = useTranslation();
  return (
    <button
      type="button"
      className={cn("cp-auto lg-live", live && "is-on")}
      onClick={onToggle}
      aria-pressed={live}
      title={live ? t("logs.pauseHint") : t("logs.resumeHint")}
    >
      <span className="cp-auto-switch" aria-hidden />
      <span>{live ? t("logs.live") : t("logs.paused")}</span>
      {!live && pending > 0 ? <span className="cp-badge-count">+{pending}</span> : null}
    </button>
  );
}

export function LogsView({ logs, audit, streaming, onRefreshAudit, credentials, apiKeys }: Props) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { masked } = useAccountMask();
  const guide = usePageGuide("logs");
  const [view, setView] = useStoredState<View>("tproxy.logs.view", "requests", VIEWS);

  const [query, setQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [protocol, setProtocol] = useState("all");
  const [requestSort, setRequestSort] = useState<RequestSort>("newest");
  const [live, setLive] = useState(true);
  const [frozen, setFrozen] = useState<RequestLog[] | null>(null);
  const [detail, setDetail] = useState<RequestLog | null>(null);

  const [auditQuery, setAuditQuery] = useState("");
  const [auditStatus, setAuditStatus] = useState<AuditStatusFilter>("all");
  const [auditType, setAuditType] = useState("all");
  const [auditSort, setAuditSort] = useState<AuditSort>("newest");
  const [auditDetail, setAuditDetail] = useState<AuditEvent | null>(null);
  const [auditAuto, setAuditAuto] = useState(true);
  const [auditNextAt, setAuditNextAt] = useState(() => Date.now() + AUDIT_REFRESH_MS);
  const [refreshingAudit, setRefreshingAudit] = useState(false);

  const refreshAudit = useCallback(async () => {
    setRefreshingAudit(true);
    setAuditNextAt(Date.now() + AUDIT_REFRESH_MS);
    try {
      await onRefreshAudit();
    } finally {
      setRefreshingAudit(false);
    }
  }, [onRefreshAudit]);

  // Audit events auto-refresh every 30s while the audit view is open.
  useEffect(() => {
    if (view !== "audit" || !auditAuto) return undefined;
    const timer = window.setTimeout(() => void refreshAudit(), Math.max(1000, auditNextAt - Date.now()));
    return () => window.clearTimeout(timer);
  }, [view, auditAuto, auditNextAt, refreshAudit]);

  const resolvers = useMemo<RequestLogResolvers>(() => {
    const accounts = new Map<string, string>();
    for (const items of Object.values(credentials || {})) {
      for (const item of items) {
        const label = item.label?.trim() || item.email?.trim();
        if (label) accounts.set(item.id, maskAccountText(label, masked));
      }
    }
    const keys = new Map((apiKeys || []).map((key) => [key.id, key.name || key.id]));
    return { account: (id) => accounts.get(id), apiKey: (id) => keys.get(id) };
  }, [credentials, apiKeys, masked]);

  // While paused the list keeps the snapshot taken at pause time, so rows do not move under the cursor.
  const shownLogs = live || !frozen ? logs : frozen;
  const pendingCount = useMemo(() => {
    if (live || !frozen) return 0;
    const seen = new Set(frozen.map(requestLogKey));
    return logs.filter((item) => !seen.has(requestLogKey(item))).length;
  }, [live, frozen, logs]);

  const toggleLive = () => {
    if (live) {
      setFrozen(logs);
      setLive(false);
    } else {
      setFrozen(null);
      setLive(true);
    }
  };

  // Search scope: the stat tiles and protocol tabs count over this, so clicking one does not move the others.
  const searchedLogs = useMemo(() => {
    const q = query.trim();
    if (!q) return shownLogs;
    return shownLogs.filter((item) =>
      [
        item.request_id,
        item.path,
        item.method,
        modelOf(item),
        providerOf(item),
        item.credential_id,
        item.credential_id ? resolvers.account?.(item.credential_id) : "",
        item.client_api_key_id,
        item.client_api_key_id ? resolvers.apiKey?.(item.client_api_key_id) : "",
        item.error_code,
        item.protocol,
        String(item.status),
      ].some((value) => matchesQuery(value, q)),
    );
  }, [shownLogs, query, resolvers]);

  const protocolCounts = useMemo(() => {
    const counts = new Map<string, number>();
    for (const item of searchedLogs) counts.set(protocolKey(item), (counts.get(protocolKey(item)) || 0) + 1);
    return counts;
  }, [searchedLogs]);
  const protocolOptions = useMemo(() => {
    const keys = new Set([...protocolCounts.keys()]);
    for (const item of shownLogs) keys.add(protocolKey(item));
    return [...keys].sort((a, b) => (a === "other" ? 1 : b === "other" ? -1 : (protocolCounts.get(b) || 0) - (protocolCounts.get(a) || 0)));
  }, [protocolCounts, shownLogs]);

  // A protocol that left the buffer falls back to "all" instead of an empty list.
  useEffect(() => {
    if (protocol !== "all" && !protocolOptions.includes(protocol)) setProtocol("all");
  }, [protocol, protocolOptions]);

  const scopeLogs = useMemo(
    () => (protocol === "all" ? searchedLogs : searchedLogs.filter((item) => protocolKey(item) === protocol)),
    [searchedLogs, protocol],
  );

  const requestStats = useMemo(() => {
    const errors = scopeLogs.filter(isErrorLog).length;
    const rateLimited = scopeLogs.filter(isRateLimitedLog).length;
    const latencies = scopeLogs.map((item) => item.latency_ms ?? 0);
    return {
      total: scopeLogs.length,
      errors,
      rateLimited,
      errorRate: scopeLogs.length > 0 ? Math.round((errors / scopeLogs.length) * 1000) / 10 : 0,
      p50: percentile(latencies, 50),
      p95: percentile(latencies, 95),
      avg: average(latencies),
    };
  }, [scopeLogs]);

  const visibleLogs = useMemo(() => {
    const filtered = scopeLogs.filter((item) =>
      statusFilter === "errors" ? isErrorLog(item) : statusFilter === "rate-limited" ? isRateLimitedLog(item) : true,
    );
    const [key, order] = REQUEST_SORTS[requestSort];
    return sortRequestLogs(filtered, key, order);
  }, [scopeLogs, statusFilter, requestSort]);

  /* ---------------- Audit ---------------- */
  const searchedAudit = useMemo(() => {
    const q = auditQuery.trim();
    if (!q) return audit;
    return audit.filter((item) =>
      [item.action, item.resource_type, item.resource_id, item.actor, String(item.status)].some((value) => matchesQuery(value, q)),
    );
  }, [audit, auditQuery]);

  const auditTypeCounts = useMemo(() => {
    const counts = new Map<string, number>();
    for (const item of searchedAudit) {
      const type = item.resource_type || "other";
      counts.set(type, (counts.get(type) || 0) + 1);
    }
    return counts;
  }, [searchedAudit]);
  const auditTypes = useMemo(() => {
    const keys = new Set([...auditTypeCounts.keys(), ...audit.map((item) => item.resource_type || "other")]);
    return [...keys].sort((a, b) => (auditTypeCounts.get(b) || 0) - (auditTypeCounts.get(a) || 0) || a.localeCompare(b));
  }, [auditTypeCounts, audit]);

  useEffect(() => {
    if (auditType !== "all" && !auditTypes.includes(auditType)) setAuditType("all");
  }, [auditType, auditTypes]);

  const scopeAudit = useMemo(
    () => (auditType === "all" ? searchedAudit : searchedAudit.filter((item) => (item.resource_type || "other") === auditType)),
    [searchedAudit, auditType],
  );
  const visibleAudit = useMemo(() => {
    const filtered = auditStatus === "failed" ? scopeAudit.filter((item) => item.status >= 400) : scopeAudit;
    const [key, order] = AUDIT_SORTS[auditSort];
    return sortAuditEvents(filtered, key, order);
  }, [scopeAudit, auditStatus, auditSort]);
  const auditStats = useMemo(() => {
    const failed = scopeAudit.filter((item) => item.status >= 400).length;
    const actors = new Set(scopeAudit.map((item) => item.actor).filter(Boolean));
    const latest = sortAuditEvents(scopeAudit, "created_at", "desc")[0];
    return { total: scopeAudit.length, failed, actors: actors.size, latest };
  }, [scopeAudit]);

  /* ---------------- Filters ---------------- */
  const requestFilterCount = Number(statusFilter !== "all") + Number(protocol !== "all") + Number(query.trim() !== "");
  const auditFilterCount = Number(auditStatus !== "all") + Number(auditType !== "all") + Number(auditQuery.trim() !== "");
  const clearRequestFilters = () => {
    setQuery("");
    setStatusFilter("all");
    setProtocol("all");
  };
  const clearAuditFilters = () => {
    setAuditQuery("");
    setAuditStatus("all");
    setAuditType("all");
  };
  const toggleStatus = (value: StatusFilter) => setStatusFilter((current) => (current === value ? "all" : value));

  const filterTile = (count: number, clear: () => void): PageStat => ({
    key: "filters",
    icon: "filter_alt",
    label: t("logs.stats.filters"),
    value: count,
    hint: count > 0 ? t("logs.stats.filtersClear") : t("logs.stats.filtersNone"),
    muted: count === 0,
    tone: count > 0 ? "primary" : "neutral",
    onClick: count > 0 ? clear : undefined,
    title: count > 0 ? t("logs.stats.filtersClear") : undefined,
  });

  const requestTiles: PageStat[] = [
    {
      key: "requests",
      icon: "receipt_long",
      label: t("logs.stats.requests"),
      value: requestStats.total,
      hint: live
        ? t("logs.stats.requestsLive", { count: shownLogs.length })
        : t("logs.stats.requestsPaused", { count: pendingCount }),
      active: statusFilter === "all",
      onClick: () => setStatusFilter("all"),
      title: t("logs.stats.requestsHint"),
    },
    {
      key: "errors",
      icon: "error",
      tone: "danger",
      label: t("logs.stats.errorRate"),
      value: `${requestStats.errorRate}%`,
      hint: t("logs.stats.errorCount", { count: requestStats.errors }),
      muted: requestStats.errors === 0,
      active: statusFilter === "errors",
      onClick: () => toggleStatus("errors"),
      title: t("logs.stats.errorHint"),
    },
    {
      key: "rate-limited",
      icon: "speed",
      tone: "warning",
      label: t("logs.stats.rateLimited"),
      value: requestStats.rateLimited,
      hint: t("logs.stats.rateLimitedHint"),
      muted: requestStats.rateLimited === 0,
      active: statusFilter === "rate-limited",
      onClick: () => toggleStatus("rate-limited"),
      title: t("logs.stats.rateLimitedHint"),
    },
    {
      key: "latency",
      icon: "timer",
      label: t("logs.stats.p95"),
      value: requestStats.total > 0 ? fmtLatency(requestStats.p95) : "—",
      hint: requestStats.total > 0
        ? t("logs.stats.latencyHint", { avg: fmtLatency(requestStats.avg), p50: fmtLatency(requestStats.p50) })
        : t("logs.stats.noData"),
      mono: true,
    },
    filterTile(requestFilterCount, clearRequestFilters),
  ];

  const auditTiles: PageStat[] = [
    {
      key: "events",
      icon: "history",
      label: t("logs.stats.events"),
      value: auditStats.total,
      hint: t("logs.stats.eventsHint"),
      active: auditStatus === "all",
      onClick: () => setAuditStatus("all"),
    },
    {
      key: "failed",
      icon: "error",
      tone: "danger",
      label: t("logs.stats.failed"),
      value: auditStats.failed,
      hint: t("logs.stats.failedHint"),
      muted: auditStats.failed === 0,
      active: auditStatus === "failed",
      onClick: () => setAuditStatus((current) => (current === "failed" ? "all" : "failed")),
    },
    {
      key: "actors",
      icon: "person",
      label: t("logs.stats.actors"),
      value: auditStats.actors,
      hint: t("logs.stats.actorsHint"),
    },
    {
      key: "latest",
      icon: "update",
      label: t("logs.stats.latest"),
      value: auditStats.latest ? fmtAgo(auditStats.latest.created_at, t) : "—",
      hint: auditStats.latest ? auditStats.latest.action : t("logs.stats.noData"),
      mono: true,
    },
    filterTile(auditFilterCount, clearAuditFilters),
  ];

  const requestMenu = (item: RequestLog): Array<ActionMenuItem | null> => {
    const model = modelOf(item);
    const provider = providerOf(item);
    return [
      { key: "details", icon: "open_in_full", label: t("logs.menu.details"), onSelect: () => setDetail(item) },
      model ? { key: "model", icon: "filter_alt", label: t("logs.menu.filterModel"), hint: model, onSelect: () => setQuery(model) } : null,
      provider ? { key: "provider", icon: "filter_alt", label: t("logs.menu.filterProvider"), hint: provider, onSelect: () => setQuery(provider) } : null,
      item.client_api_key_id
        ? { key: "key", icon: "key", label: t("logs.menu.filterKey"), hint: resolvers.apiKey?.(item.client_api_key_id) || item.client_api_key_id, onSelect: () => setQuery(item.client_api_key_id || "") }
        : null,
      provider ? { key: "open-provider", icon: "dns", label: t("logs.menu.openProvider"), onSelect: () => navigate(`/providers/${encodeURIComponent(provider)}`) } : null,
    ];
  };

  const auditMenu = (item: AuditEvent): Array<ActionMenuItem | null> => [
    { key: "details", icon: "open_in_full", label: t("logs.menu.details"), onSelect: () => setAuditDetail(item) },
    item.resource_id ? { key: "resource", icon: "filter_alt", label: t("logs.auditView.filterResource"), hint: item.resource_id, onSelect: () => setAuditQuery(item.resource_id || "") } : null,
    item.actor ? { key: "actor", icon: "person_search", label: t("logs.auditView.filterActor"), hint: item.actor, onSelect: () => setAuditQuery(item.actor || "") } : null,
  ];

  const protocolLabel = (key: string) => (key === "other" ? t("logs.protocolOther") : PROTOCOL_LABELS[key] || key);
  const viewSwitch = (
    <Segmented
      label={t("logs.viewLabel")}
      value={view}
      onChange={setView}
      items={[
        { value: "requests", label: t("logs.requests") },
        { value: "audit", label: t("logs.auditView.title") },
      ]}
    />
  );

  return (
    <section className="cp-page lg-page">
      <PageStats label={t("logs.stats.label")} items={view === "requests" ? requestTiles : auditTiles} />

      <Toolbar>
        <ToolbarRow>
          {viewSwitch}
          {view === "requests" ? (
            <FilterTabs
              label={t("logs.filterByProtocol")}
              value={protocol}
              onChange={(value) => setProtocol(value === protocol ? "all" : value)}
              items={[
                { value: "all", icon: "apps", label: t("logs.allProtocols"), count: searchedLogs.length },
                ...protocolOptions.map((key) => ({ value: key, label: protocolLabel(key), count: protocolCounts.get(key) || 0 })),
              ]}
            />
          ) : (
            <FilterTabs
              label={t("logs.auditView.filterByType")}
              value={auditType}
              onChange={(value) => setAuditType(value === auditType ? "all" : value)}
              items={[
                { value: "all", icon: "apps", label: t("logs.auditView.allTypes"), count: searchedAudit.length },
                ...auditTypes.map((key) => ({ value: key, label: key === "other" ? t("logs.protocolOther") : key, count: auditTypeCounts.get(key) || 0 })),
              ]}
            />
          )}
          <ToolbarEnd>
            {view === "requests" ? (
              streaming ? <LiveToggle live={live} pending={pendingCount} onToggle={toggleLive} /> : null
            ) : (
              <>
                <AutoRefreshToggle
                  enabled={auditAuto}
                  nextAt={auditNextAt}
                  title={t("logs.auditView.autoRefreshHint")}
                  onToggle={() => {
                    setAuditNextAt(Date.now() + AUDIT_REFRESH_MS);
                    setAuditAuto((value) => !value);
                  }}
                />
                <Button variant="primary" size="sm" icon="refresh" loading={refreshingAudit} onClick={() => void refreshAudit()}>
                  {t("logs.auditView.refresh")}
                </Button>
              </>
            )}
          </ToolbarEnd>
        </ToolbarRow>
        <ToolbarRow>
          {view === "requests" ? (
            <SearchField value={query} onChange={setQuery} placeholder={t("logs.searchRequests")} />
          ) : (
            <SearchField value={auditQuery} onChange={setAuditQuery} placeholder={t("logs.searchAudit")} />
          )}
          <ToolbarEnd>
            {view === "requests" ? (
              <SelectField
                label={t("logs.sortLabel")}
                value={requestSort}
                onChange={setRequestSort}
                options={[
                  { value: "newest", label: t("logs.sort.newest") },
                  { value: "oldest", label: t("logs.sort.oldest") },
                  { value: "slowest", label: t("logs.sort.slowest") },
                  { value: "status", label: t("logs.sort.status") },
                ]}
              />
            ) : (
              <SelectField
                label={t("logs.sortLabel")}
                value={auditSort}
                onChange={setAuditSort}
                options={[
                  { value: "newest", label: t("logs.sort.newest") },
                  { value: "oldest", label: t("logs.sort.oldest") },
                  { value: "action", label: t("logs.sort.action") },
                  { value: "status", label: t("logs.sort.status") },
                ]}
              />
            )}
            <GuideToggle open={guide.open} onToggle={guide.toggle} />
          </ToolbarEnd>
        </ToolbarRow>
      </Toolbar>

      <PageGuide
        open={guide.open}
        onDismiss={guide.dismiss}
        title={t("logs.guide.title")}
        steps={[
          { icon: "receipt_long", text: t("logs.guide.stream") },
          { icon: "filter_alt", text: t("logs.guide.filter") },
          { icon: "pause_circle", text: t("logs.guide.inspect") },
        ]}
        legend={
          <>
            <LegendItem tone="success">{t("logs.guide.legendOk")}</LegendItem>
            <LegendItem tone="warning">{t("logs.guide.legendClient")}</LegendItem>
            <LegendItem tone="danger">{t("logs.guide.legendServer")}</LegendItem>
          </>
        }
      />

      {view === "requests" ? (
        <section className="lg-section" aria-label={t("logs.requests")}>
          <SectionHead
            leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>receipt_long</span>}
            title={t("logs.requests")}
            subtitle={
              visibleLogs.length === shownLogs.length
                ? t("logs.summary", { count: shownLogs.length })
                : t("logs.summaryFiltered", { shown: visibleLogs.length, count: shownLogs.length })
            }
            meta={
              <ToneCounts
                label={t("logs.stats.label")}
                items={[
                  { tone: "success", count: scopeLogs.filter((item) => item.status >= 200 && item.status < 400).length, label: t("logs.guide.legendOk") },
                  { tone: "warning", count: scopeLogs.filter((item) => item.status >= 400 && item.status < 500).length, label: t("logs.guide.legendClient") },
                  { tone: "danger", count: scopeLogs.filter((item) => item.status >= 500).length, label: t("logs.guide.legendServer") },
                ]}
              />
            }
          />
          {!live ? (
            <PageNote icon="pause_circle" action={<LinkButton onClick={toggleLive}>{t("logs.resume")}</LinkButton>}>
              {pendingCount > 0 ? t("logs.pausedNotePending", { count: pendingCount }) : t("logs.pausedNote")}
            </PageNote>
          ) : null}
          <RequestLogList
            logs={visibleLogs}
            resolvers={resolvers}
            onOpen={setDetail}
            menuItems={requestMenu}
            empty={
              shownLogs.length === 0 ? (
                <EmptyPanel
                  icon="receipt_long"
                  title={t("logs.noRequestsLogged")}
                  description={t("logs.requestsHint")}
                  action={<Button variant="secondary" icon="terminal" onClick={() => navigate("/cli-tools")}>{t("logs.connectClient")}</Button>}
                />
              ) : (
                <EmptyPanel
                  icon="filter_alt_off"
                  title={t("logs.noMatch")}
                  description={t("logs.noMatchHint")}
                  action={<Button variant="secondary" icon="filter_alt_off" onClick={clearRequestFilters}>{t("logs.clearFilters")}</Button>}
                />
              )
            }
          />
        </section>
      ) : (
        <section className="lg-section" aria-label={t("logs.auditView.title")}>
          <SectionHead
            leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>history</span>}
            title={t("logs.auditView.title")}
            subtitle={
              visibleAudit.length === audit.length
                ? t("logs.auditView.summary", { count: audit.length })
                : t("logs.summaryFiltered", { shown: visibleAudit.length, count: audit.length })
            }
            actions={
              <ToolButton icon="filter_alt_off" disabled={auditFilterCount === 0} onClick={clearAuditFilters}>
                {t("logs.clearFilters")}
              </ToolButton>
            }
          />
          <AuditList
            events={visibleAudit}
            onOpen={setAuditDetail}
            menuItems={auditMenu}
            empty={
              audit.length === 0 ? (
                <EmptyPanel icon="history" title={t("logs.noAuditEvents")} description={t("logs.auditHint")} />
              ) : (
                <EmptyPanel
                  icon="filter_alt_off"
                  title={t("logs.noMatch")}
                  description={t("logs.noMatchHint")}
                  action={<Button variant="secondary" icon="filter_alt_off" onClick={clearAuditFilters}>{t("logs.clearFilters")}</Button>}
                />
              )
            }
          />
        </section>
      )}

      <RequestLogDetailModal
        log={detail}
        resolvers={resolvers}
        onClose={() => setDetail(null)}
        onFilter={setQuery}
        onOpenProvider={(providerId) => navigate(`/providers/${encodeURIComponent(providerId)}`)}
      />
      <AuditDetailModal event={auditDetail} onClose={() => setAuditDetail(null)} onFilter={setAuditQuery} />
    </section>
  );
}
