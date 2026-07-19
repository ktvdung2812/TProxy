import { cn } from "../ui";
import { formatProxyUsageLabel, formatQuotaName, formatResetAbsolute, formatResetTime, getColorEmoji, getColorTone } from "./utils";

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
  credentialActive?: boolean;
  proxyUsage?: { requests: number; promptTokens: number; completionTokens: number };
  onHide?: (row: QuotaRow) => void;
};

function isSessionQuotaRow(row: QuotaRow) {
  const key = row.key.trim().toLowerCase();
  const name = row.name.trim().toLowerCase();
  return key === "session" || name === "session";
}

export function QuotaTable({ rows, credentialActive = false, proxyUsage, onHide }: Props) {
  if (rows.length === 0) return null;

  const proxyUsageLabel = formatProxyUsageLabel(proxyUsage);

  return (
    <div className="quota-tracker-table-wrap">
      <div className="quota-tracker-table-meta">
        <span>
          {rows.length} quota{rows.length === 1 ? "" : "s"}
        </span>
        {proxyUsageLabel ? (
          <span className="quota-tracker-table-meta-usage" title="Local proxy usage (all time)">
            {proxyUsageLabel}
          </span>
        ) : null}
      </div>
      <table className="quota-tracker-table">
        <tbody>
          {rows.map((row) => {
            const tone = getColorTone(row.remaining);
            const countdown = formatResetTime(row.reset_at);
            const absoluteReset = formatResetAbsolute(row.reset_at);
            const countdownLabel = countdown !== "-" ? `in ${countdown}` : "N/A";

            const isLive = credentialActive && isSessionQuotaRow(row);

            return (
              <tr key={row.key} className="quota-tracker-table-row">
                <td className={cn("quota-tracker-cell quota-tracker-cell-name", isLive && "quota-tracker-cell-name-live")}>
                  <span className="quota-tracker-dot" aria-hidden>
                    {getColorEmoji(row.remaining)}
                  </span>
                  <span className="quota-tracker-name">
                    {formatQuotaName(row.name)}
                  </span>
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
                    <span className="quota-tracker-reset-relative">{countdownLabel}</span>
                    {absoluteReset ? <span className="quota-tracker-reset-absolute">{absoluteReset}</span> : null}
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
