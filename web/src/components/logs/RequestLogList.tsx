import { useEffect, useRef, useState } from "react";
import type { KeyboardEvent, ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { RequestLog } from "../../hooks/useRequestLogStream";
import { ActionMenu, IconAction, StatusPill, type ActionMenuItem } from "../page-kit";
import { cn } from "../ui";
import {
  fmtAbsolute,
  fmtAgo,
  fmtClock,
  fmtLatency,
  isRateLimitedLog,
  modelOf,
  providerOf,
  requestLogKey,
  statusTone,
  type RequestSortKey,
  type SortOrder,
} from "./utils";

export type RequestLogResolvers = {
  /** Display label for a credential id (already masked when accounts are hidden). */
  account?: (credentialId: string) => string | undefined;
  /** Display name for a client API key id. */
  apiKey?: (keyId: string) => string | undefined;
};

type Props = {
  logs: RequestLog[];
  resolvers?: RequestLogResolvers;
  onOpen?: (log: RequestLog) => void;
  /** Extra overflow-menu entries for a row; the menu is hidden when omitted. */
  menuItems?: (log: RequestLog) => Array<ActionMenuItem | null | false>;
  /** Makes the column heads sort buttons (used where no toolbar sort exists). */
  sort?: { key: RequestSortKey; order: SortOrder; onSort: (key: RequestSortKey) => void };
  empty?: ReactNode;
  className?: string;
};

const SORTABLE: Array<{ key: RequestSortKey; label: string } | { key: null; label: string }> = [
  { key: "status", label: "logs.status" },
  { key: "created_at", label: "logs.time" },
  { key: "path", label: "logs.request" },
  { key: null, label: "logs.route" },
  { key: "latency_ms", label: "logs.latency" },
];

export function keyLabel(item: RequestLog, resolvers?: RequestLogResolvers): string {
  if (!item.client_api_key_id) return "local";
  return resolvers?.apiKey?.(item.client_api_key_id) || item.client_api_key_id;
}

function openOnKey(event: KeyboardEvent<HTMLElement>, open: () => void) {
  if (event.target !== event.currentTarget) return;
  if (event.key === "Enter" || event.key === " ") {
    event.preventDefault();
    open();
  }
}

/** Dense request rows: status, time, request, route (model → provider/account) and latency. */
export function RequestLogList({ logs, resolvers, onOpen, menuItems, sort, empty, className }: Props) {
  const { t } = useTranslation();
  const [copiedKey, setCopiedKey] = useState<string | null>(null);
  const copyTimer = useRef<number | undefined>(undefined);
  const maxLatency = Math.max(1, ...logs.map((item) => item.latency_ms || 0));
  // One clock for every row's relative time, refreshed twice a minute.
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 30000);
    return () => window.clearInterval(timer);
  }, []);
  useEffect(() => () => window.clearTimeout(copyTimer.current), []);

  const copyId = async (item: RequestLog) => {
    try {
      await navigator.clipboard.writeText(item.request_id);
      setCopiedKey(requestLogKey(item));
      window.clearTimeout(copyTimer.current);
      copyTimer.current = window.setTimeout(() => setCopiedKey(null), 1600);
    } catch {
      /* clipboard may be unavailable */
    }
  };

  if (logs.length === 0) return <>{empty}</>;

  return (
    <div className={cn("cp-list lg-list lg-request-list", className)}>
      <div className="cp-list-head" role={sort ? undefined : "presentation"} aria-hidden={sort ? undefined : true}>
        {SORTABLE.map((column) =>
          sort && column.key ? (
            <button
              key={column.label}
              type="button"
              className={cn("lg-sort", sort.key === column.key && "is-active")}
              onClick={() => sort.onSort(column.key as RequestSortKey)}
              title={t("logs.sortBy", { label: t(column.label) })}
            >
              {t(column.label)}
              <span className="material-symbols-outlined" aria-hidden>
                {sort.key !== column.key ? "unfold_more" : sort.order === "asc" ? "arrow_upward" : "arrow_downward"}
              </span>
            </button>
          ) : (
            <span key={column.label}>{t(column.label)}</span>
          ),
        )}
        <span />
      </div>
      {logs.map((item) => {
        const rowKey = requestLogKey(item);
        const tone = statusTone(item.status);
        const model = modelOf(item);
        const provider = providerOf(item);
        const account = item.credential_id ? resolvers?.account?.(item.credential_id) || item.credential_id : "";
        const routeMeta = [provider, account, item.attempt && item.attempt > 1 ? t("logs.attemptN", { count: item.attempt }) : ""]
          .filter(Boolean)
          .join(" · ");
        const extraItems = menuItems ? menuItems(item) : [];
        const open = onOpen ? () => onOpen(item) : undefined;
        return (
          <div
            key={rowKey}
            className={cn("cp-list-row lg-row", `lg-tone-${item.status >= 400 ? tone : "none"}`)}
            role={open ? "button" : undefined}
            tabIndex={open ? 0 : undefined}
            aria-label={open ? t("logs.openRequest", { id: item.request_id }) : undefined}
            onClick={open}
            onKeyDown={open ? (event) => openOnKey(event, open) : undefined}
          >
            <span className="cp-cell lg-cell-status">
              <StatusPill tone={tone} title={isRateLimitedLog(item) ? t("logs.stats.rateLimited") : undefined}>
                {item.status || "—"}
              </StatusPill>
              {item.error_code ? <small className={`cp-tone-${tone}`} title={item.error_code}>{item.error_code}</small> : null}
            </span>
            <span className="cp-cell cp-cell-main" title={fmtAbsolute(item.created_at)}>
              <strong className="lg-mono">{fmtClock(item.created_at)}</strong>
              <small>{fmtAgo(item.created_at, t, now)}</small>
            </span>
            <span className="cp-cell cp-cell-main">
              <span className="lg-path-line" title={`${item.method} ${item.path}`}>
                <code className="lg-method">{item.method}</code>
                <span className="lg-path">{item.path}</span>
              </span>
              <small title={item.request_id}>
                {item.request_id} · {keyLabel(item, resolvers)}
              </small>
            </span>
            <span className="cp-cell cp-cell-main">
              {model || provider ? (
                <>
                  <strong className="lg-model" title={model}>{model || "—"}</strong>
                  {routeMeta ? <small title={routeMeta}>→ {routeMeta}</small> : null}
                </>
              ) : (
                <small>{t("logs.noRoute")}</small>
              )}
            </span>
            <span className="cp-cell lg-cell-latency" title={`${item.latency_ms ?? 0} ms`}>
              <span className="lg-mono">{fmtLatency(item.latency_ms)}</span>
              <span className="cp-meter-track" aria-hidden>
                <span className="cp-meter-fill" style={{ width: `${Math.max(3, ((item.latency_ms || 0) / maxLatency) * 100)}%` }} />
              </span>
            </span>
            <span className="cp-cell cp-cell-actions" onClick={(event) => event.stopPropagation()}>
              <IconAction
                icon={copiedKey === rowKey ? "check" : "content_copy"}
                label={copiedKey === rowKey ? t("common.copied") : t("logs.copyRequestId")}
                onClick={() => void copyId(item)}
              />
              {menuItems ? <ActionMenu label={t("logs.moreActions")} items={extraItems} /> : null}
            </span>
          </div>
        );
      })}
    </div>
  );
}
