import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { ActionMenu, EmptyPanel, IconAction, SectionHead, StatusPill, ToneCounts, ToolButton } from "../page-kit";
import { Button } from "../ui";
import type { OperationsAlert } from "./api";
import { SEVERITY_TONE, fmtAge, fmtClock, type AlertFix, type AlertSeverity, type AlertSubject } from "./utils";

export type AlertView = {
  alert: OperationsAlert;
  severity: AlertSeverity;
  subject: AlertSubject;
  fix: AlertFix;
  /** Human name for the affected resource (masked account label, model id…). */
  resourceLabel: string;
  /** Secondary line: provider name, model, raw id. */
  resourceDetail: string;
  /** Whether "show its metrics" can narrow the metrics list to this alert. */
  filterQuery: string | null;
};

type Props = {
  views: AlertView[];
  totals: { critical: number; warning: number };
  evaluatedAt?: string;
  hasFilters: boolean;
  onClearFilters: () => void;
  onFilterMetrics: (query: string) => void;
  onCopy: (text: string) => void;
};

export function OperationsAlerts({ views, totals, evaluatedAt, hasFilters, onClearFilters, onFilterMetrics, onCopy }: Props) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const now = Date.now();

  return (
    <section className="op-section" aria-label={t("operations.alertsTitle")}>
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>notifications_active</span>}
        title={t("operations.alertsTitle")}
        subtitle={evaluatedAt ? t("operations.alertsSubtitle", { time: fmtClock(evaluatedAt) }) : undefined}
        meta={
          <ToneCounts
            items={[
              { tone: "danger", count: totals.critical, label: t("operations.severity.critical") },
              { tone: "warning", count: totals.warning, label: t("operations.severity.warning") },
            ]}
          />
        }
      />
      {views.length === 0 ? (
        hasFilters ? (
          <EmptyPanel
            compact
            icon="filter_alt_off"
            title={t("operations.noAlertsMatch")}
            action={<Button variant="secondary" size="sm" icon="filter_alt_off" onClick={onClearFilters}>{t("operations.clearFilters")}</Button>}
          />
        ) : (
          <EmptyPanel compact icon="verified" title={t("operations.noAlerts")} description={t("operations.noAlertsHint")} />
        )
      ) : (
        <div className="cp-list op-alerts" aria-live="polite">
          {views.map((view) => {
            const { alert, severity, fix } = view;
            const title = t(`operations.alerts.${alert.kind}`, { defaultValue: alert.kind });
            return (
              <div key={alert.id} className={`cp-list-row op-alert-row op-tone-${SEVERITY_TONE[severity]}`}>
                <span className="cp-cell op-alert-severity">
                  <StatusPill tone={SEVERITY_TONE[severity]}>{t(`operations.severity.${severity}`)}</StatusPill>
                </span>
                <span className="cp-cell cp-cell-main op-alert-main">
                  <strong title={title}>{title}</strong>
                  <small className="op-alert-help">{t(`operations.alertHelp.${alert.kind}`, { defaultValue: "" })}</small>
                </span>
                <span className="cp-cell cp-cell-main op-alert-resource">
                  <strong className="op-mono-strong" title={alert.resource_id}>{view.resourceLabel}</strong>
                  {view.resourceDetail ? <small title={view.resourceDetail}>{view.resourceDetail}</small> : null}
                </span>
                <span className="cp-cell cp-cell-mono op-alert-age" title={t("operations.firstSeen", { time: new Date(alert.first_seen).toLocaleString() })}>
                  <span className="material-symbols-outlined" aria-hidden>schedule</span>
                  {fmtAge(alert.first_seen, now)}
                </span>
                <span className="cp-cell cp-cell-actions op-alert-actions">
                  <ToolButton icon={fix.icon} tone={SEVERITY_TONE[severity]} onClick={() => navigate(fix.path)}>
                    {t(fix.labelKey)}
                  </ToolButton>
                  {view.filterQuery ? (
                    <IconAction
                      icon="filter_list"
                      label={t("operations.showMetrics")}
                      onClick={() => onFilterMetrics(view.filterQuery as string)}
                    />
                  ) : null}
                  <ActionMenu
                    label={t("operations.moreActions")}
                    items={[
                      { key: "fix", icon: fix.icon, label: t(fix.labelKey), hint: fix.path, onSelect: () => navigate(fix.path) },
                      view.filterQuery
                        ? { key: "metrics", icon: "filter_list", label: t("operations.showMetrics"), onSelect: () => onFilterMetrics(view.filterQuery as string) }
                        : null,
                      { key: "copy", icon: "content_copy", label: t("operations.copyId"), hint: alert.resource_id, onSelect: () => onCopy(alert.resource_id) },
                    ]}
                  />
                </span>
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}
