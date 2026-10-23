import { useTranslation } from "react-i18next";
import { PageNote, StatusPill, type Tone } from "../page-kit";
import type { ApiKeyUsage, EffectiveLimit } from "./types";

const fields = [
  ["requests_per_minute", "requestsPerMin"],
  ["concurrent_requests", "concurrentRequests"],
  ["concurrent_streams", "concurrentStreams"],
  ["budget_usd_per_day", "dailyBudget"],
  ["media_jobs", "mediaJobs"],
  ["max_input_bytes", "maxInputBytes"],
  ["max_output_tokens", "maxOutputTokens"],
] as const;

const STATE_TONE: Record<EffectiveLimit["state"], Tone> = {
  finite: "info",
  unbounded: "neutral",
  unknown: "warning",
  unavailable: "danger",
};

/** Effective limits for one key: its own ceiling, the pool it shares with other keys, and what is left now. */
export function ApiKeyCapacity({ usage }: { usage?: ApiKeyUsage }) {
  const { t, i18n } = useTranslation();
  if (!usage?.effective_limits) return null;
  const unknown = usage.quota_windows?.filter((item) => item.state === "unknown").length || 0;
  const stale = usage.quota_windows?.some((item) => item.stale && item.state !== "unknown");
  const format = (value: number, name: string) => name === "budget_usd_per_day"
    ? new Intl.NumberFormat(i18n.language, { style: "currency", currency: "USD", maximumFractionDigits: 4 }).format(value)
    : new Intl.NumberFormat(i18n.language, { maximumFractionDigits: 2 }).format(value);
  const describe = (limit: EffectiveLimit, name: string) =>
    limit.state === "finite" && limit.value !== undefined
      ? <span className="ap-capacity-value">{format(limit.value, name)}</span>
      : <StatusPill tone={STATE_TONE[limit.state]} dot={false}>{t(`apis.limitStates.${limit.state}`)}</StatusPill>;

  return (
    <div className="ap-capacity">
      <PageNote icon="groups">{t("apis.sharedCapacity", { count: usage.eligible_account_count ?? 0 })} · {t("apis.capacityPerRequestHint")}</PageNote>
      <div className="cp-list ap-capacity-list">
        <div className="cp-list-head" aria-hidden>
          <span>{t("apis.effectiveLimits")}</span>
          <span>{t("apis.limitValue")}</span>
          <span>{t("apis.sharedLimitValue")}</span>
          <span>{t("apis.limitRemaining")}</span>
          <span>{t("apis.limitSource")}</span>
        </div>
        {fields.map(([name, label]) => {
          const limit = usage.effective_limits?.[name];
          if (!limit) return null;
          const shared = usage.shared_limits?.[name] ?? limit;
          const sources = [...new Set(limit.sources.map((source) => source.split(":")[0]))]
            .map((source) => t(`apis.limitSources.${source}`)).join(", ");
          return (
            <div key={name} className="cp-list-row">
              <span className="cp-cell cp-cell-main"><strong>{t(`apis.${label}`)}</strong></span>
              <span className="cp-cell">{describe(limit, name)}</span>
              <span className="cp-cell">{describe(shared, name)}</span>
              <span className="cp-cell cp-cell-mono">{limit.remaining !== undefined ? format(limit.remaining, name) : "—"}</span>
              <span className="cp-cell cp-cell-mono" title={sources}>{sources || "—"}</span>
            </div>
          );
        })}
      </div>
      {unknown > 0 ? <PageNote icon="help">{t("apis.quotaUnknown", { count: unknown })}</PageNote> : null}
      {stale ? <PageNote icon="schedule">{t("apis.quotaStale")}</PageNote> : null}
    </div>
  );
}
