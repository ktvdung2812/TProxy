import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { EmptyPanel, SectionHead } from "../page-kit";
import type { UsageEvent } from "../usage/api";
import { fmt, fmtCompact } from "../usage/utils";

type Bucket = { label: string; saved: number; requests: number };

const HOUR_MS = 3_600_000;

function SavingsTooltip({ active, payload }: { active?: boolean; payload?: Array<{ payload: Bucket }> }) {
  const { t } = useTranslation();
  const point = active ? payload?.[0]?.payload : undefined;
  if (!point) return null;
  return (
    <div className="us-tooltip">
      <span className="us-tooltip-label">{point.label}</span>
      <span className="us-tooltip-row is-main">
        <i aria-hidden />
        <strong>{fmt(point.saved)}</strong>
        <span>{t("tokenSaver.chart.saved")}</span>
      </span>
      <span className="us-tooltip-row">
        <strong>{fmt(point.requests)}</strong>
        <span>{t("tokenSaver.chart.requests")}</span>
      </span>
    </div>
  );
}

/** Tokens removed by compression per hour, over the newest requests in the usage log. */
export function SavingsChart({ events, sampleSize }: { events: UsageEvent[] | null; sampleSize: number }) {
  const { t } = useTranslation();
  const buckets = useMemo<Bucket[]>(() => {
    if (!events?.length) return [];
    const times = events.map((event) => Date.parse(event.created_at)).filter(Number.isFinite);
    if (!times.length) return [];
    const end = Math.floor(Date.now() / HOUR_MS) * HOUR_MS;
    // Cap the span so one stray old event cannot stretch the axis over weeks.
    const start = Math.max(Math.floor(Math.min(...times) / HOUR_MS) * HOUR_MS, end - 47 * HOUR_MS);
    const list: Bucket[] = [];
    for (let at = start; at <= end; at += HOUR_MS) {
      const date = new Date(at);
      list.push({ label: `${date.toLocaleDateString([], { day: "2-digit", month: "2-digit" })} ${String(date.getHours()).padStart(2, "0")}:00`, saved: 0, requests: 0 });
    }
    for (const event of events) {
      const index = Math.floor((Date.parse(event.created_at) - start) / HOUR_MS);
      if (index < 0 || index >= list.length) continue;
      list[index].saved += event.tokens_saved || 0;
      list[index].requests += 1;
    }
    return list;
  }, [events]);

  const total = buckets.reduce((sum, bucket) => sum + bucket.saved, 0);

  return (
    <section className="ts-section" aria-label={t("tokenSaver.chart.title")}>
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>savings</span>}
        title={t("tokenSaver.chart.title")}
        subtitle={t("tokenSaver.chart.subtitle", { count: events?.length ?? sampleSize, total: fmtCompact(total) })}
      />
      <div className="us-panel us-chart">
        {events === null ? (
          <div className="cp-skeleton us-chart-skeleton ts-chart-skeleton" aria-hidden>
            <span />
            <span />
          </div>
        ) : total === 0 ? (
          <EmptyPanel compact icon="savings" title={t("tokenSaver.chart.empty")} description={t("tokenSaver.chart.emptyHint")} />
        ) : (
          <div className="ts-chart-plot" role="img" aria-label={`${t("tokenSaver.chart.title")} · ${fmt(total)}`}>
            <ResponsiveContainer width="100%" height={180}>
              <BarChart data={buckets} margin={{ top: 8, right: 8, left: 0, bottom: 0 }} barCategoryGap="22%">
                <CartesianGrid vertical={false} />
                <XAxis
                  dataKey="label"
                  tickLine={false}
                  interval="preserveStartEnd"
                  minTickGap={24}
                  tickMargin={8}
                  tickFormatter={(value: string) => value.slice(-5)}
                />
                <YAxis tickLine={false} axisLine={false} width={52} tickFormatter={(value) => fmtCompact(Number(value))} />
                <Tooltip cursor={{ className: "us-chart-cursor" }} isAnimationActive={false} content={<SavingsTooltip />} />
                <Bar dataKey="saved" radius={[4, 4, 0, 0]} maxBarSize={24} isAnimationActive={false} />
              </BarChart>
            </ResponsiveContainer>
          </div>
        )}
      </div>
    </section>
  );
}
