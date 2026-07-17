import { useEffect, useMemo, useState } from "react";
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { Card } from "../ui";
import { fetchUsageChart } from "./api";
import type { UsagePeriod } from "./api";
import { fmtCost } from "./utils";

type Props = {
  secret: string;
  period: UsagePeriod;
  onError: (message: string) => void;
};

function fmtTokens(value: number) {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return String(value || 0);
}

export function UsageChart({ secret, period, onError }: Props) {
  const [data, setData] = useState<{ label: string; tokens: number; cost: number }[]>([]);
  const [loading, setLoading] = useState(true);
  const [viewMode, setViewMode] = useState<"tokens" | "cost">("tokens");

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    fetchUsageChart(secret, period)
      .then((points) => {
        if (!cancelled) setData(points);
      })
      .catch((error) => {
        if (!cancelled) onError(error instanceof Error ? error.message : "Failed to load chart");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [secret, period, onError]);

  const hasData = useMemo(
    () => data.some((point) => point.tokens > 0 || point.cost > 0),
    [data],
  );

  return (
    <Card pad="md" className="usage-chart-card">
      <div className="usage-chart-toolbar">
        <div className="usage-segmented usage-chart-toggle">
          <button type="button" className={viewMode === "tokens" ? "active" : ""} onClick={() => setViewMode("tokens")}>
            Tokens
          </button>
          <button type="button" className={viewMode === "cost" ? "active" : ""} onClick={() => setViewMode("cost")}>
            Cost
          </button>
        </div>
      </div>
      {loading ? (
        <div className="usage-chart-empty">Loading...</div>
      ) : !hasData ? (
        <div className="usage-chart-empty">No data for this period</div>
      ) : (
        <div className="usage-chart-recharts">
          <ResponsiveContainer width="100%" height={220}>
            <AreaChart data={data} margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
              <defs>
                <linearGradient id="usageGradTokens" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="#6366f1" stopOpacity={0.25} />
                  <stop offset="95%" stopColor="#6366f1" stopOpacity={0} />
                </linearGradient>
                <linearGradient id="usageGradCost" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="#f59e0b" stopOpacity={0.25} />
                  <stop offset="95%" stopColor="#f59e0b" stopOpacity={0} />
                </linearGradient>
              </defs>
              <CartesianGrid strokeDasharray="3 3" strokeOpacity={0.1} />
              <XAxis
                dataKey="label"
                tick={{ fontSize: 10, fill: "currentColor", fillOpacity: 0.5 }}
                tickLine={false}
                axisLine={false}
                interval="preserveStartEnd"
              />
              <YAxis
                tick={{ fontSize: 10, fill: "currentColor", fillOpacity: 0.5 }}
                tickLine={false}
                axisLine={false}
                tickFormatter={viewMode === "tokens" ? fmtTokens : (value) => fmtCost(Number(value))}
                width={50}
              />
              <Tooltip
                contentStyle={{
                  backgroundColor: "var(--color-surface)",
                  border: "1px solid var(--color-border)",
                  borderRadius: "8px",
                  fontSize: "12px",
                }}
                formatter={(value, name) =>
                  name === "tokens"
                    ? [fmtTokens(Number(value)), "Tokens"]
                    : [fmtCost(Number(value)), "Cost"]
                }
              />
              {viewMode === "tokens" ? (
                <Area
                  type="monotone"
                  dataKey="tokens"
                  stroke="#6366f1"
                  strokeWidth={2}
                  fill="url(#usageGradTokens)"
                  dot={false}
                  activeDot={{ r: 4 }}
                />
              ) : (
                <Area
                  type="monotone"
                  dataKey="cost"
                  stroke="#f59e0b"
                  strokeWidth={2}
                  fill="url(#usageGradCost)"
                  dot={false}
                  activeDot={{ r: 4 }}
                />
              )}
            </AreaChart>
          </ResponsiveContainer>
        </div>
      )}
    </Card>
  );
}
