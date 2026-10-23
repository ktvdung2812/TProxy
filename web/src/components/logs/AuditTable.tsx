import { useEffect, useState } from "react";
import type { KeyboardEvent, ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ActionMenu, StatusPill, type ActionMenuItem } from "../page-kit";
import { cn } from "../ui";
import type { AuditEvent } from "./api";
import { fmtAbsolute, fmtAgo, fmtClock, statusTone } from "./utils";

export type AuditSortKey = "created_at" | "action" | "status";

export function sortAuditEvents(events: AuditEvent[], key: AuditSortKey, order: "asc" | "desc"): AuditEvent[] {
  const next = [...events];
  next.sort((a, b) => {
    let cmp = 0;
    if (key === "status") cmp = (a.status ?? 0) - (b.status ?? 0);
    else if (key === "action") cmp = (a.action || "").localeCompare(b.action || "");
    else cmp = new Date(a.created_at).getTime() - new Date(b.created_at).getTime();
    return order === "asc" ? cmp : -cmp;
  });
  return next;
}

export function auditEventKey(item: AuditEvent, index: number): string {
  return `${item.action}-${item.created_at}-${item.id ?? index}`;
}

function openOnKey(event: KeyboardEvent<HTMLElement>, open: () => void) {
  if (event.target !== event.currentTarget) return;
  if (event.key === "Enter" || event.key === " ") {
    event.preventDefault();
    open();
  }
}

type Props = {
  events: AuditEvent[];
  onOpen: (event: AuditEvent) => void;
  menuItems: (event: AuditEvent) => Array<ActionMenuItem | null | false>;
  empty?: ReactNode;
};

/** Admin changes: who did what to which resource, and whether it succeeded. */
export function AuditList({ events, onOpen, menuItems, empty }: Props) {
  const { t } = useTranslation();
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 30000);
    return () => window.clearInterval(timer);
  }, []);

  if (events.length === 0) return <>{empty}</>;

  return (
    <div className="cp-list lg-list lg-audit-list">
      <div className="cp-list-head" aria-hidden>
        <span>{t("logs.status")}</span>
        <span>{t("logs.time")}</span>
        <span>{t("logs.auditView.action")}</span>
        <span>{t("logs.auditView.resource")}</span>
        <span>{t("logs.actor")}</span>
        <span />
      </div>
      {events.map((item, index) => {
        const tone = statusTone(item.status);
        const open = () => onOpen(item);
        return (
          <div
            key={auditEventKey(item, index)}
            className={cn("cp-list-row lg-row", `lg-tone-${item.status >= 400 ? tone : "none"}`)}
            role="button"
            tabIndex={0}
            aria-label={t("logs.auditView.open", { action: item.action })}
            onClick={open}
            onKeyDown={(event) => openOnKey(event, open)}
          >
            <span className="cp-cell lg-cell-status">
              <StatusPill tone={tone}>{item.status || "—"}</StatusPill>
            </span>
            <span className="cp-cell cp-cell-main" title={fmtAbsolute(item.created_at)}>
              <strong className="lg-mono">{fmtClock(item.created_at)}</strong>
              <small>{fmtAgo(item.created_at, t, now)}</small>
            </span>
            <span className="cp-cell cp-cell-main">
              <strong className="lg-mono" title={item.action}>{item.action}</strong>
            </span>
            <span className="cp-cell cp-cell-main">
              <strong className="lg-model" title={item.resource_id || item.resource_type}>{item.resource_id || "—"}</strong>
              {item.resource_type ? <small>{item.resource_type}</small> : null}
            </span>
            <span className="cp-cell cp-cell-mono" title={item.actor}>{item.actor || "—"}</span>
            <span className="cp-cell cp-cell-actions" onClick={(event) => event.stopPropagation()}>
              <ActionMenu label={t("logs.moreActions")} items={menuItems(item)} />
            </span>
          </div>
        );
      })}
    </div>
  );
}
