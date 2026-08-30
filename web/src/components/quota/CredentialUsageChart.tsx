import { useEffect, useMemo, useState } from "react";
import {
  Bar,
  CartesianGrid,
  ComposedChart,
  Legend,
  Line,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import {
  fetchCredentialUsageChart,
  type CredentialUsageChartPeriod,
  type CredentialUsageChartPoint,
} from "./api";

type Props = {
  secret: string;
  credentialId: string;
  active: boolean;
};

const PERIODS: Array<{ value: CredentialUsageChartPeriod; label: string }> = [
  { value: "day", label: "Day" },
  { value: "week", label: "Week" },
  { value: "month", label: "Month" },
];

function formatTokens(value: number) {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return String(value || 0);
}

export function CredentialUsageChart({ secret, credentialId, active }: Props) {
  const [period, setPeriod] = useState<CredentialUsageChartPeriod>("week");
  const [points, setPoints] = useState<CredentialUsageChartPoint[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!active || !credentialId) return;
    let cancelled = false;
    setLoading(true);
    setError("");
    fetchCredentialUsageChart(secret, credentialId, period)
      .then((data) => {
        if (!cancelled) setPoints(data);
      })
      .catch((cause) => {
        if (!cancelled) setError(cause instanceof Error ? cause.message : "Failed to load account usage");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [secret, credentialId, period, active]);

  const hasUsage = useMemo(
    () => points.some((point) => point.requests > 0 || point.tokens > 0),
    [points],
  );

  return (
    <section className="credential-usage-chart" aria-label="Account usage chart">
      <div className="credential-usage-chart-toolbar">
        <div>
          <h5>Account usage</h5>
          <p>Requests and proxy tokens routed through this account.</p>
        </div>
        <div className="credential-usage-chart-periods" aria-label="Usage period">
          {PERIODS.map((option) => (
            <button
              key={option.value}
              type="button"
              className={period === option.value ? "is-active" : ""}
              onClick={() => setPeriod(option.value)}
              aria-pressed={period === option.value}
            >
              {option.label}
            </button>
          ))}
        </div>
      </div>
      {loading ? (
        <div className="credential-usage-chart-state">
          <span className="material-symbols-outlined animate-spin">progress_activity</span>
          Loading usage…
        </div>
      ) : error ? (
        <div className="credential-usage-chart-state is-error">{error}</div>
      ) : !hasUsage ? (
        <div className="credential-usage-chart-state">No usage recorded for this period.</div>
      ) : (
        <div className="credential-usage-chart-canvas">
          <ResponsiveContainer width="100%" height={220}>
            <ComposedChart data={points} margin={{ top: 8, right: 8, left: 0, bottom: 0 }}>
              <CartesianGrid strokeDasharray="3 3" strokeOpacity={0.12} vertical={false} />
              <XAxis
                dataKey="label"
                interval="preserveStartEnd"
                tick={{ fontSize: 10, fill: "currentColor", fillOpacity: 0.58 }}
                tickLine={false}
                axisLine={false}
              />
              <YAxis
                yAxisId="tokens"
                tickFormatter={(value) => formatTokens(Number(value))}
                tick={{ fontSize: 10, fill: "currentColor", fillOpacity: 0.58 }}
                tickLine={false}
                axisLine={false}
                width={48}
              />
              <YAxis
                yAxisId="requests"
                orientation="right"
                allowDecimals={false}
                tick={{ fontSize: 10, fill: "currentColor", fillOpacity: 0.58 }}
                tickLine={false}
                axisLine={false}
                width={36}
              />
              <Tooltip
                contentStyle={{
                  backgroundColor: "var(--color-surface)",
                  border: "1px solid var(--color-border)",
                  borderRadius: "8px",
                  fontSize: "12px",
                }}
                formatter={(value, name) => [
                  name === "Tokens" ? formatTokens(Number(value)) : Number(value || 0).toLocaleString(),
                  name,
                ]}
              />
              <Legend iconSize={8} wrapperStyle={{ fontSize: "11px" }} />
              <Bar yAxisId="tokens" dataKey="tokens" name="Tokens" fill="var(--color-primary)" fillOpacity={0.68} radius={[4, 4, 0, 0]} />
              <Line yAxisId="requests" type="monotone" dataKey="requests" name="Requests" stroke="var(--color-info)" strokeWidth={2} dot={false} activeDot={{ r: 4 }} />
            </ComposedChart>
          </ResponsiveContainer>
        </div>
      )}
    </section>
  );
}
