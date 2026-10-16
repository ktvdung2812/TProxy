import { useTranslation } from "react-i18next";
import type { ApiKeyUsage } from "./types";

const fields = [
  ["requests_per_minute", "requestsPerMin"],
  ["concurrent_requests", "concurrentRequests"],
  ["concurrent_streams", "concurrentStreams"],
  ["budget_usd_per_day", "dailyBudget"],
  ["media_jobs", "mediaJobs"],
  ["max_input_bytes", "maxInputBytes"],
  ["max_output_tokens", "maxOutputTokens"],
] as const;

export function ApiKeyCapacity({ usage }: { usage?: ApiKeyUsage }) {
  const { t, i18n } = useTranslation();
  if (!usage?.effective_limits) return null;
  const unknown = usage.quota_windows?.filter((item) => item.state === "unknown").length || 0;
  const stale = usage.quota_windows?.some((item) => item.stale && item.state !== "unknown");
  const format = (value: number, name: string) => name === "budget_usd_per_day"
    ? new Intl.NumberFormat(i18n.language, { style: "currency", currency: "USD", maximumFractionDigits: 4 }).format(value)
    : new Intl.NumberFormat(i18n.language, { maximumFractionDigits: 2 }).format(value);
  return (
    <details className="apis-key-capacity">
      <summary>{t("apis.sharedCapacity", { count: usage.eligible_account_count ?? 0 })}</summary>
      <p className="apis-key-row-meta">{t("apis.capacityPerRequestHint")}</p>
      <div className="apis-key-capacity-table">
        <table>
          <thead><tr><th>{t("apis.effectiveLimits")}</th><th>{t("apis.sharedLimitValue")}</th><th>{t("apis.limitValue")}</th><th>{t("apis.limitRemaining")}</th></tr></thead>
          <tbody>
            {fields.map(([name, label]) => {
              const limit = usage.effective_limits?.[name];
              if (!limit) return null;
              const shared = usage.shared_limits?.[name] ?? limit;
              const sources = [...new Set(limit.sources.map((source) => source.split(":")[0]))]
                .map((source) => t(`apis.limitSources.${source}`)).join(", ");
              return <tr key={name} title={`${t("apis.limitSource")}: ${sources}`}>
                <th scope="row">{t(`apis.${label}`)}</th>
                <td>{shared.state === "finite" && shared.value !== undefined ? format(shared.value, name) : t(`apis.limitStates.${shared.state}`)}</td>
                <td>{limit.state === "finite" && limit.value !== undefined ? format(limit.value, name) : t(`apis.limitStates.${limit.state}`)}</td>
                <td>{limit.remaining !== undefined ? format(limit.remaining, name) : "—"}</td>
              </tr>;
            })}
          </tbody>
        </table>
      </div>
      {unknown > 0 ? <p className="apis-key-row-meta">{t("apis.quotaUnknown", { count: unknown })}</p> : null}
      {stale ? <p className="apis-key-row-meta">{t("apis.quotaStale")}</p> : null}
    </details>
  );
}
