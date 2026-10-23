import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Bar, BarChart, CartesianGrid, LabelList, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { EmptyPanel, SectionHead, Segmented, useStoredState } from "../page-kit";
import { fetchUsageChart, type UsageChartPoint, type UsagePeriod } from "./api";
import { fmt, fmtCompact, fmtCost, type UsageValueMode } from "./utils";

type Props = {
  secret: string;
  period: UsagePeriod;
  valueMode: UsageValueMode;
  reloadToken: number;
  onError: (message: string) => void;
};

type Metric = "tokens" | "cost";
type ChartView = "chart" | "table";

function formatValue(metric: Metric, value: number) {
  return metric === "cost" ? fmtCost(value) : fmtCompact(value);
}

/** Axis ticks stay short: $1.2K rather than $1,234.00. */
function formatTick(metric: Metric, value: number) {
  if (metric === "tokens") return fmtCompact(value);
  return value >= 1000 ? `$${fmtCompact(value)}` : `$${Number(value.toFixed(2))}`;
}

function ChartTooltip({ active, payload, metric }: { active?: boolean; payload?: Array<{ payload: UsageChartPoint }>; metric: Metric }) {
  const { t } = useTranslation();
  const point = active ? payload?.[0]?.payload : undefined;
  if (!point) return null;
  const other: Metric = metric === "cost" ? "tokens" : "cost";
  return (
    <div className="us-tooltip">
      <span className="us-tooltip-label">{point.label}</span>
      <span className="us-tooltip-row is-main">
        <i aria-hidden />
        <strong>{metric === "cost" ? fmtCost(point.cost) : fmt(point.tokens)}</strong>
        <span>{t(`usage.chart.metric_${metric}`)}</span>
      </span>
      <span className="us-tooltip-row">
        <strong>{other === "cost" ? fmtCost(point.cost) : fmt(point.tokens)}</strong>
        <span>{t(`usage.chart.metric_${other}`)}</span>
      </span>
    </div>
  );
}

/**
 * One measure over time as columns: a single series in the primary hue, so it
 * needs no legend; the section title names it and the peak carries the one
 * direct label. The table view is the accessible twin of the chart.
 */
export function UsageChart({ secret, period, valueMode, reloadToken, onError }: Props) {
  const { t } = useTranslation();
  const metric: Metric = valueMode === "costs" ? "cost" : "tokens";
  const [view, setView] = useStoredState<ChartView>("tproxy.usage.chartView", "chart", ["chart", "table"]);
  const [data, setData] = useState<UsageChartPoint[] | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    fetchUsageChart(secret, period)
      .then((points) => {
        if (!cancelled) setData(Array.isArray(points) ? points : []);
      })
      .catch((error) => {
        if (!cancelled) onError(error instanceof Error ? error.message : t("usage.chart.failed"));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [secret, period, onError, reloadToken]);

  const points = data ?? [];
  const summary = useMemo(() => {
    let total = 0;
    let peakIndex = -1;
    points.forEach((point, index) => {
      const value = point[metric] || 0;
      total += value;
      if (value > 0 && (peakIndex < 0 || value > (points[peakIndex][metric] || 0))) peakIndex = index;
    });
    return { total, peakIndex, hasData: points.some((point) => point.tokens > 0 || point.cost > 0) };
  }, [points, metric]);

  const hourly = period === "today" || period === "24h";
  const peak = summary.peakIndex >= 0 ? points[summary.peakIndex] : null;
  const subtitle = summary.hasData
    ? t("usage.chart.subtitle", {
        bucket: t(hourly ? "usage.chart.hourly" : "usage.chart.daily"),
        total: formatValue(metric, summary.total),
        peak: peak ? formatValue(metric, peak[metric]) : "—",
        at: peak?.label ?? "—",
      })
    : t(hourly ? "usage.chart.hourly" : "usage.chart.daily");

  return (
    <section className="us-section" aria-label={t(`usage.chart.title_${metric}`)}>
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>bar_chart</span>}
        title={t(`usage.chart.title_${metric}`)}
        subtitle={subtitle}
        actions={
          <Segmented
            label={t("usage.chart.view")}
            value={view}
            onChange={setView}
            items={[
              { value: "chart", label: t("usage.chart.asChart"), icon: "bar_chart" },
              { value: "table", label: t("usage.chart.asTable"), icon: "table_rows" },
            ]}
          />
        }
      />
      <div className="us-panel us-chart">
        {data === null && loading ? (
          <div className="cp-skeleton us-chart-skeleton" aria-hidden>
            <span />
            <span />
            <span />
          </div>
        ) : !summary.hasData ? (
          <EmptyPanel compact icon="bar_chart_off" title={t("usage.noDataForPeriod")} description={t("usage.chart.emptyHint")} />
        ) : view === "table" ? (
          <div className="cp-list us-chart-table">
            <div className="cp-list-head">
              <span>{t(hourly ? "usage.chart.hour" : "usage.chart.day")}</span>
              <span className="us-num">{t("usage.tokens")}</span>
              <span className="us-num">{t("usage.cost")}</span>
            </div>
            {points.map((point, index) => (
              <div key={`${point.label}-${index}`} className="cp-list-row">
                <span className="cp-cell cp-cell-mono">{point.label}</span>
                <span className="cp-cell us-num">{fmt(point.tokens)}</span>
                <span className="cp-cell us-num">{fmtCost(point.cost)}</span>
              </div>
            ))}
          </div>
        ) : (
          <div className="us-chart-plot" role="img" aria-label={`${t(`usage.chart.title_${metric}`)} · ${subtitle}`}>
            <ResponsiveContainer width="100%" height={240}>
              <BarChart data={points} margin={{ top: 22, right: 8, left: 0, bottom: 0 }} barCategoryGap="22%">
                <CartesianGrid vertical={false} />
                <XAxis dataKey="label" tickLine={false} interval="preserveStartEnd" minTickGap={14} tickMargin={8} />
                <YAxis
                  tickLine={false}
                  axisLine={false}
                  width={52}
                  tickFormatter={(value) => formatTick(metric, Number(value))}
                  allowDecimals={metric === "cost"}
                />
                <Tooltip cursor={{ className: "us-chart-cursor" }} isAnimationActive={false} content={<ChartTooltip metric={metric} />} />
                <Bar dataKey={metric} radius={[4, 4, 0, 0]} maxBarSize={24} isAnimationActive={false}>
                  <LabelList
                    dataKey={metric}
                    content={(props) => {
                      const { x, y, width, index, value } = props as { x?: number | string; y?: number | string; width?: number | string; index?: number; value?: number | string };
                      if (index !== summary.peakIndex || x === undefined || y === undefined) return null;
                      return (
                        <text x={Number(x) + Number(width || 0) / 2} y={Number(y) - 7} textAnchor="middle" className="us-chart-peak">
                          {formatValue(metric, Number(value))}
                        </text>
                      );
                    }}
                  />
                </Bar>
              </BarChart>
            </ResponsiveContainer>
          </div>
        )}
      </div>
    </section>
  );
}
