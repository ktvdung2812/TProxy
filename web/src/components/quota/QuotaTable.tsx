import { cn } from "../ui";
import { formatQuotaName, formatResetTime, getColorEmoji, getColorTone } from "./utils";

type QuotaRow = {
  key: string;
  name: string;
  used: number;
  total: number;
  remaining: number;
  reset_at?: string;
  unlimited?: boolean;
};

type Props = {
  rows: QuotaRow[];
  onHide?: (row: QuotaRow) => void;
};

export function QuotaTable({ rows, onHide }: Props) {
  if (rows.length === 0) return null;

  return (
    <div className="quota-tracker-table-wrap">
      <div className="quota-tracker-table-meta">
        {rows.length} quota{rows.length === 1 ? "" : "s"}
      </div>
      <table className="quota-tracker-table">
        <tbody>
          {rows.map((row) => {
            const tone = getColorTone(row.remaining);
            const countdown = formatResetTime(row.reset_at);
            const countdownLabel = countdown !== "-" ? `in ${countdown}` : "N/A";

            return (
              <tr key={row.key} className="quota-tracker-table-row">
                <td className="quota-tracker-cell quota-tracker-cell-name">
                  <span className="quota-tracker-dot" aria-hidden>
                    {getColorEmoji(row.remaining)}
                  </span>
                  <span className="quota-tracker-name">{formatQuotaName(row.name)}</span>
                </td>
                <td className="quota-tracker-cell quota-tracker-cell-bar">
                  <div className={cn("quota-tracker-progress", `quota-tracker-progress-${tone}`, row.remaining === 0 && "quota-tracker-progress-empty")}>
                    <div className="quota-tracker-progress-fill" style={{ width: `${Math.min(row.remaining, 100)}%` }} />
                  </div>
                  <div className="quota-tracker-usage">
                    <span>
                      {Math.round(row.used).toLocaleString()} / {row.unlimited || row.total <= 0 ? "∞" : Math.round(row.total).toLocaleString()}
                    </span>
                    <span className={cn("quota-tracker-percent", `quota-tracker-percent-${tone}`)}>{row.remaining}%</span>
                  </div>
                </td>
                <td className="quota-tracker-cell quota-tracker-cell-reset">
                  <span className="quota-tracker-reset" title={row.reset_at || undefined}>
                    {countdownLabel}
                  </span>
                </td>
                {onHide ? (
                  <td className="quota-tracker-cell quota-tracker-cell-hide">
                    <button
                      type="button"
                      className="quota-tracker-hide-btn"
                      onClick={() => onHide(row)}
                      aria-label={`Hide quota ${row.name}`}
                      title="Hide this quota row"
                    >
                      <span className="material-symbols-outlined">visibility_off</span>
                    </button>
                  </td>
                ) : null}
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
