import { useTranslation } from "react-i18next";
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
  fetchCredentialModelUsage,
  fetchCredentialUsageChart,
  type CredentialModelUsageEntry,
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


export function CredentialUsageChart({ secret, credentialId, active }: Props) {
  const { t, i18n } = useTranslation();
  const formatTokens = (value: number) => new Intl.NumberFormat(i18n.language, { notation: "compact", maximumFractionDigits: 1 }).format(value || 0);
  const [period, setPeriod] = useState<CredentialUsageChartPeriod>("week");
  const [points, setPoints] = useState<CredentialUsageChartPoint[]>([]);
  const [models, setModels] = useState<CredentialModelUsageEntry[]>([]);
  const [modelsError, setModelsError] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!active || !credentialId) return;
    let cancelled = false;
    setLoading(true);
    setError("");
    setModelsError("");
    fetchCredentialUsageChart(secret, credentialId, period)
      .then((data) => {
        if (!cancelled) setPoints(data);
      })
      .catch((cause) => {
        if (!cancelled) setError(cause instanceof Error ? cause.message : t("featureA.loadUsageFailed"));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    fetchCredentialModelUsage(secret, credentialId, period)
      .then((data) => {
        if (!cancelled) setModels(data);
      })
      .catch((cause) => {
        if (!cancelled) {
          setModels([]);
          setModelsError(cause instanceof Error ? cause.message : t("featureA.loadModelUsageFailed"));
        }
      });
    return () => {
      cancelled = true;
    };
  }, [secret, credentialId, period, active, t]);

  const hasUsage = useMemo(
    () => points.some((point) => point.requests > 0 || point.tokens > 0),
    [points],
  );

  return (
    <section className="credential-usage-chart" aria-label={t("featureA.accountUsageChart")}>
      <div className="credential-usage-chart-toolbar">
        <div>
          <h5>{t("featureA.accountUsage")}</h5>
          <p>{t("featureA.accountUsageHint")}</p>
        </div>
        <div className="credential-usage-chart-periods" aria-label={t("featureA.usagePeriod")}>
          {PERIODS.map((option) => (
            <button
              key={option.value}
              type="button"
              className={period === option.value ? "is-active" : ""}
              onClick={() => setPeriod(option.value)}
              aria-pressed={period === option.value}
            >
              {t(`featureA.period.${option.value}`)}
            </button>
          ))}
        </div>
      </div>
      {loading ? (
        <div className="credential-usage-chart-state">
          <span className="material-symbols-outlined animate-spin">progress_activity</span>
          {t("featureA.loadingUsage")}
        </div>
      ) : error ? (
        <div className="credential-usage-chart-state is-error">{error}</div>
      ) : !hasUsage ? (
        <div className="credential-usage-chart-state">{t("featureA.noUsage")}</div>
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
                  name === t("featureA.tokens") ? formatTokens(Number(value)) : Number(value || 0).toLocaleString(i18n.language),
                  name,
                ]}
              />
              <Legend iconSize={8} wrapperStyle={{ fontSize: "11px" }} />
              <Bar yAxisId="tokens" dataKey="tokens" name={t("featureA.tokens")} fill="var(--color-primary)" fillOpacity={0.68} radius={[4, 4, 0, 0]} />
              <Line yAxisId="requests" type="monotone" dataKey="requests" name={t("featureA.requests")} stroke="var(--color-info)" strokeWidth={2} dot={false} activeDot={{ r: 4 }} />
            </ComposedChart>
          </ResponsiveContainer>
        </div>
      )}
      {modelsError ? (
        <div className="credential-usage-chart-state is-error credential-usage-models-error">{modelsError}</div>
      ) : models.length > 0 ? (
        <div className="credential-usage-models">
          <h6>{t("featureA.byModel")}</h6>
          <div className="logs-table-scroll">
            <table className="logs-table credential-usage-models-table">
              <thead>
                <tr>
                  <th>{t("featureA.model")}</th>
                  <th className="right">{t("featureA.requests")}</th>
                  <th className="right">{t("featureA.input")}</th>
                  <th className="right">{t("featureA.output")}</th>
                  <th className="right">{t("featureA.totalTokens")}</th>
                </tr>
              </thead>
              <tbody>
                {models.map((row) => (
                  <tr key={row.model}>
                    <td className="credential-usage-models-name">{row.model}</td>
                    <td className="right">{row.requests.toLocaleString(i18n.language)}</td>
                    <td className="right">{formatTokens(row.promptTokens)}</td>
                    <td className="right">{formatTokens(row.completionTokens)}</td>
                    <td className="right">{formatTokens(row.totalTokens)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      ) : null}
    </section>
  );
}
