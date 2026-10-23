import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { SectionHead, ToneCounts, type Tone } from "../page-kit";

export type SummaryRow = {
  key: string;
  icon: string;
  label: string;
  value: number;
  detail: string;
  to: string;
  counts?: Array<{ tone: Tone; count: number; label: string }>;
};

/** What the gateway can route right now, one row per building block, each a link to its page. */
export function GatewaySummary({ rows }: { rows: SummaryRow[] }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  return (
    <section className="cp-panel ov-panel ov-summary" aria-label={t("overview.summary.title")}>
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>hub</span>}
        title={t("overview.summary.title")}
        subtitle={t("overview.summary.subtitle")}
      />
      <div className="ov-summary-list">
        {rows.map((row) => (
          <button
            key={row.key}
            type="button"
            className="ov-summary-row"
            onClick={() => navigate(row.to)}
            title={t("overview.summary.open", { name: row.label })}
          >
            <span className="material-symbols-outlined ov-summary-icon" aria-hidden>{row.icon}</span>
            <span className="ov-summary-label">
              <strong>{row.label}</strong>
              <small>{row.detail}</small>
            </span>
            {row.counts ? <ToneCounts items={row.counts} /> : null}
            <span className="ov-summary-value">{row.value}</span>
            <span className="material-symbols-outlined ov-summary-go" aria-hidden>chevron_right</span>
          </button>
        ))}
      </div>
    </section>
  );
}
