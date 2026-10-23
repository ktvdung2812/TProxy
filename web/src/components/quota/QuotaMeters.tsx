import { useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../ui";
import { formatQuotaName, formatResetAbsolute, formatResetTime, getColorTone } from "./utils";

export type QuotaMeterRow = {
  key: string;
  name: string;
  used: number;
  total: number;
  remaining: number;
  reset_at?: string;
  unlimited?: boolean;
};

type Props = {
  rows: QuotaMeterRow[];
  /** Rows shown before the rest collapse behind a "+N more" toggle. */
  limit?: number;
  /** Compact single-line meters for the list view. */
  inline?: boolean;
  liveKey?: string;
  onHide?: (row: QuotaMeterRow) => void;
};

/**
 * Remaining quota per window as thin bars.
 *
 * Short lists (Claude, Codex) keep the provider's order — session before
 * weekly reads naturally. Long per-model lists (Antigravity meters ~30 models)
 * are sorted lowest-first and collapsed, so the models that matter are visible
 * without the card growing past the screen.
 */
export function QuotaMeters({ rows, limit = 4, inline = false, liveKey, onHide }: Props) {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState(false);
  if (rows.length === 0) return null;

  const collapsible = rows.length > limit + 1;
  const ordered = collapsible ? [...rows].sort((a, b) => a.remaining - b.remaining) : rows;
  const shown = collapsible && !expanded ? ordered.slice(0, limit) : ordered;
  const hiddenCount = ordered.length - shown.length;

  return (
    <div className={cn("qt-meters", inline && "qt-meters-inline")}>
      {shown.map((row) => {
        const unlimited = row.unlimited || row.total <= 0;
        const tone = unlimited ? "success" : getColorTone(row.remaining);
        const remaining = Math.max(0, Math.min(100, row.remaining));
        // Minutes are noise once a reset is days away: "5d 23h" not "5d 23h 3m".
        const countdown = formatResetTime(row.reset_at).replace(/^(\d+d \d+h) \d+m$/, "$1");
        const absolute = formatResetAbsolute(row.reset_at);
        const name = formatQuotaName(row.name);
        const usage = `${Math.round(row.used).toLocaleString()} / ${unlimited ? "∞" : Math.round(row.total).toLocaleString()}`;
        return (
          <div key={row.key} className={cn("qt-meter", `cp-tone-${tone}`, liveKey === row.key && "qt-meter-live")}>
            <div className="qt-meter-head">
              <span className="qt-meter-name" title={name}>{name}</span>
              <span className="qt-meter-value" title={usage}>
                {unlimited ? "∞" : t("quota.percentLeft", { value: remaining })}
              </span>
              {!inline ? (
                <span className="qt-meter-reset" title={absolute ? t("quota.resetsAt", { time: absolute }) : undefined}>
                  {countdown !== "-" ? countdown : "—"}
                </span>
              ) : null}
              {onHide && !inline ? (
                <button
                  type="button"
                  className="qt-meter-hide"
                  onClick={(event) => {
                    event.stopPropagation();
                    onHide(row);
                  }}
                  aria-label={t("quota.hideQuotaNamed", { name })}
                  title={t("quota.hideQuotaRow")}
                >
                  <span className="material-symbols-outlined" aria-hidden>visibility_off</span>
                </button>
              ) : null}
            </div>
            <div
              className="cp-meter-track"
              role="meter"
              aria-label={name}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={remaining}
              title={inline && countdown !== "-" ? `${t("quota.resetsIn", { time: countdown })}${absolute ? ` · ${absolute}` : ""}` : undefined}
            >
              <div className="cp-meter-fill" style={{ width: `${unlimited ? 100 : remaining}%` }} />
            </div>
          </div>
        );
      })}
      {collapsible ? (
        <button
          type="button"
          className="qt-meters-more"
          onClick={(event) => {
            event.stopPropagation();
            setExpanded((value) => !value);
          }}
          aria-expanded={expanded}
        >
          <span className="material-symbols-outlined" aria-hidden>{expanded ? "expand_less" : "expand_more"}</span>
          {expanded ? t("quota.showLess") : t("quota.showMoreQuotas", { count: hiddenCount })}
        </button>
      ) : null}
    </div>
  );
}
