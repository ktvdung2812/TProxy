import { cn } from "../ui";
import { formatProxyUsageLabel, formatQuotaName, formatResetAbsolute, formatResetTime, getColorTone } from "./utils";

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
  proxyUsage?: { requests: number; promptTokens: number; completionTokens: number };
  onHide?: (row: QuotaRow) => void;
};

// Geometry of one ring. The stroke is drawn on a circle whose circumference is
// split into the remaining arc and the rest, so no path math is needed at
// render time — only a dash offset.
const RING_SIZE = 72;
const RING_STROKE = 7;
const RING_RADIUS = (RING_SIZE - RING_STROKE) / 2;
const RING_CIRCUMFERENCE = 2 * Math.PI * RING_RADIUS;

/**
 * Per-model quota as a grid of rings.
 *
 * Providers that meter every model separately (Antigravity bills ten of them)
 * produce a list too long to scan as bars, and the bar layout puts a long model
 * name and its percentage on the same line, where they collide. A ring gives
 * each model a fixed-size tile, so the row count stops driving the height.
 */
export function QuotaRingGrid({ rows, proxyUsage, onHide }: Props) {
  if (rows.length === 0) return null;

  const proxyUsageLabel = formatProxyUsageLabel(proxyUsage);

  return (
    <div className="quota-ring-wrap">
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

      <ul className="quota-ring-grid">
        {rows.map((row) => {
          const tone = getColorTone(row.remaining);
          const remaining = Math.max(0, Math.min(100, row.remaining));
          const countdown = formatResetTime(row.reset_at);
          const absoluteReset = formatResetAbsolute(row.reset_at);
          const name = formatQuotaName(row.name);
          const usageTitle = `${Math.round(row.used).toLocaleString()} / ${
            row.unlimited || row.total <= 0 ? "∞" : Math.round(row.total).toLocaleString()
          }`;

          return (
            <li key={row.key} className={cn("quota-ring-item", `quota-ring-${tone}`)}>
              {onHide ? (
                <button
                  type="button"
                  className="quota-ring-hide"
                  onClick={() => onHide(row)}
                  aria-label={`Hide quota ${name}`}
                  title="Hide this quota"
                >
                  <span className="material-symbols-outlined">visibility_off</span>
                </button>
              ) : null}

              <div className="quota-ring-chart" title={usageTitle}>
                <svg width={RING_SIZE} height={RING_SIZE} viewBox={`0 0 ${RING_SIZE} ${RING_SIZE}`} role="img" aria-label={`${name}: ${remaining}% remaining`}>
                  {/* Rotated so the arc starts at twelve o'clock rather than three. */}
                  <g transform={`rotate(-90 ${RING_SIZE / 2} ${RING_SIZE / 2})`}>
                    <circle
                      className="quota-ring-track"
                      cx={RING_SIZE / 2}
                      cy={RING_SIZE / 2}
                      r={RING_RADIUS}
                      strokeWidth={RING_STROKE}
                      fill="none"
                    />
                    <circle
                      className="quota-ring-value"
                      cx={RING_SIZE / 2}
                      cy={RING_SIZE / 2}
                      r={RING_RADIUS}
                      strokeWidth={RING_STROKE}
                      fill="none"
                      strokeLinecap="round"
                      strokeDasharray={RING_CIRCUMFERENCE}
                      strokeDashoffset={RING_CIRCUMFERENCE * (1 - remaining / 100)}
                    />
                  </g>
                </svg>
                <span className="quota-ring-percent">{remaining}%</span>
              </div>

              <span className="quota-ring-name" title={name}>
                {name}
              </span>
              <span className="quota-ring-reset" title={row.reset_at || undefined}>
                {countdown !== "-" ? `in ${countdown}` : "N/A"}
                {absoluteReset ? <span className="quota-ring-reset-absolute">{absoluteReset}</span> : null}
              </span>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
