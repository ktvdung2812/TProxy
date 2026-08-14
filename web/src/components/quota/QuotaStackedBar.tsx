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

const BREAKDOWN_PREFIX = "product_";
const AGGREGATE_KEY = "weekly";

/**
 * Grok's weekly allowance as one segmented bar.
 *
 * Grok meters several products against a single weekly pool, so the products
 * are parts of one bar rather than bars of their own — which is how x.ai's own
 * usage page presents it. Rendering them as separate rows invited the reading
 * that an untouched product meant spare capacity, when the pool it draws from
 * could already be spent.
 */
export function QuotaStackedBar({ rows, proxyUsage, onHide }: Props) {
  const aggregate = rows.find((row) => row.key === AGGREGATE_KEY);
  const segments = rows.filter((row) => row.key.startsWith(BREAKDOWN_PREFIX));
  const others = rows.filter((row) => row.key !== AGGREGATE_KEY && !row.key.startsWith(BREAKDOWN_PREFIX));

  // Without the aggregate there is no single pool to draw, so this layout has
  // nothing to say; the caller falls back to the table.
  if (!aggregate) return null;

  const proxyUsageLabel = formatProxyUsageLabel(proxyUsage);
  const usedPct = Math.max(0, Math.min(100, 100 - aggregate.remaining));
  const tone = getColorTone(aggregate.remaining);
  const countdown = formatResetTime(aggregate.reset_at);
  const absoluteReset = formatResetAbsolute(aggregate.reset_at);
  const ordered = [...segments].sort((a, b) => b.used - a.used);

  return (
    <div className="quota-stacked">
      <div className="quota-tracker-table-meta">
        <span>{formatQuotaName(aggregate.name)}</span>
        {proxyUsageLabel ? (
          <span className="quota-tracker-table-meta-usage" title="Local proxy usage (all time)">
            {proxyUsageLabel}
          </span>
        ) : null}
      </div>

      <div className="quota-stacked-head">
        <span className={cn("quota-stacked-headline", `quota-stacked-headline-${tone}`)}>
          {usedPct}% đã sử dụng
          <span className="quota-stacked-remaining">còn {aggregate.remaining}%</span>
        </span>
        <span className="quota-stacked-reset" title={aggregate.reset_at || undefined}>
          {countdown !== "-" ? `in ${countdown}` : "N/A"}
          {absoluteReset ? <span className="quota-stacked-reset-absolute">{absoluteReset}</span> : null}
        </span>
      </div>

      <div
        className="quota-stacked-bar"
        role="img"
        aria-label={`${usedPct}% of the weekly limit used`}
      >
        {ordered.map((segment, index) => (
          <div
            key={segment.key}
            className={cn("quota-stacked-segment", `quota-stacked-segment-${Math.min(index, 4)}`)}
            style={{ width: `${Math.max(0, Math.min(100, segment.used))}%` }}
            title={`${formatQuotaName(segment.name)}: ${segment.used}%`}
          />
        ))}
      </div>

      {ordered.length > 0 ? (
        <ul className="quota-stacked-legend">
          {ordered.map((segment, index) => (
            <li key={segment.key} className="quota-stacked-legend-item">
              <span className={cn("quota-stacked-swatch", `quota-stacked-segment-${Math.min(index, 4)}`)} aria-hidden />
              <span className="quota-stacked-legend-name">{formatQuotaName(segment.name)}</span>
              <span className="quota-stacked-legend-value">{segment.used}%</span>
              {onHide ? (
                <button
                  type="button"
                  className="quota-stacked-hide"
                  onClick={() => onHide(segment)}
                  aria-label={`Hide quota ${segment.name}`}
                  title="Hide this quota"
                >
                  <span className="material-symbols-outlined">visibility_off</span>
                </button>
              ) : null}
            </li>
          ))}
        </ul>
      ) : null}

      {/* Spend windows (prepaid, on-demand) are separate pools, not slices of
          the weekly one, so they keep their own bars below. */}
      {others.map((row) => {
        const rowTone = getColorTone(row.remaining);
        return (
          <div key={row.key} className="quota-stacked-extra">
            <span className="quota-stacked-extra-name">{formatQuotaName(row.name)}</span>
            <div className={cn("quota-tracker-progress", `quota-tracker-progress-${rowTone}`)}>
              <div className="quota-tracker-progress-fill" style={{ width: `${Math.min(row.remaining, 100)}%` }} />
            </div>
            <span className={cn("quota-tracker-percent", `quota-tracker-percent-${rowTone}`)}>
              Còn {row.remaining}%
            </span>
          </div>
        );
      })}
    </div>
  );
}
