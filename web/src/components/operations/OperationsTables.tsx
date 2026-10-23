import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { EmptyPanel, IconAction, PageNote, SectionHead, Tag, ToneCounts, type Tone } from "../page-kit";
import { ProviderLogo } from "../providers/ProviderLogo";
import { cn } from "../ui";
import { fmtCompact, fmt } from "../usage/utils";
import type { OperationsAccount, OperationsMetric } from "./api";
import { errorTone, fmtAge, fmtMs, fmtNumber, fmtPercent, quotaTone } from "./utils";

export type MetricView = {
  key: string;
  metric: OperationsMetric;
  rate: number;
  tone: Tone;
  providerName: string;
  providerType: string;
  accountLabel: string;
};

export type AccountView = {
  account: OperationsAccount;
  tone: Tone | null;
  label: string;
  providerId: string | null;
  providerName: string;
  providerType: string;
  stale: boolean;
};

function stop(event: { stopPropagation: () => void }) {
  event.stopPropagation();
}

function TokenPair({ top, bottom }: { top: number; bottom: number }) {
  return (
    <span className="cp-cell op-num op-num-pair" title={`${fmt(top)} / ${fmt(bottom)}`}>
      <span>{fmtCompact(top)}</span>
      <small>{fmtCompact(bottom)}</small>
    </span>
  );
}

function EmptyState({ hasFilters, title, hint, onClearFilters }: { hasFilters: boolean; title: string; hint: string; onClearFilters: () => void }) {
  const { t } = useTranslation();
  return hasFilters ? (
    <EmptyPanel
      compact
      icon="filter_alt_off"
      title={t("operations.noMatch")}
      action={<button type="button" className="cp-link-btn" onClick={onClearFilters}>{t("operations.clearFilters")}</button>}
    />
  ) : (
    <EmptyPanel compact icon="hourglass_empty" title={title} description={hint} />
  );
}

export function MetricsSection({ views, hasFilters, onClearFilters }: { views: MetricView[]; hasFilters: boolean; onClearFilters: () => void }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const counts = { danger: 0, warning: 0 };
  for (const view of views) if (view.tone === "danger" || view.tone === "warning") counts[view.tone] += 1;

  return (
    <section className="op-section" aria-label={t("operations.requests")}>
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>query_stats</span>}
        title={t("operations.requests")}
        subtitle={t("operations.metricsSubtitle", { count: views.length })}
        meta={
          <ToneCounts
            items={[
              { tone: "danger", count: counts.danger, label: t("operations.highErrors") },
              { tone: "warning", count: counts.warning, label: t("operations.someErrors") },
            ]}
          />
        }
      />
      {views.length === 0 ? (
        <EmptyState hasFilters={hasFilters} title={t("operations.noRequests")} hint={t("operations.noRequestsHint")} onClearFilters={onClearFilters} />
      ) : (
        <div className="cp-list op-metrics">
          <div className="cp-list-head" aria-hidden>
            <span>{t("operations.account")}</span>
            <span className="op-num">{t("operations.attempts")}</span>
            <span className="op-num">{t("operations.errors")}</span>
            <span className="op-num">{t("operations.fallbacks")}</span>
            <span className="op-num">{t("operations.latency")}</span>
            <span className="op-num">{t("operations.ttft")}</span>
            <span className="op-num">{t("operations.queue")}</span>
            <span className="op-num">{t("operations.inputOutput")}</span>
            <span className="op-num">{t("operations.cacheReadWrite")}</span>
            <span />
          </div>
          {views.map((view) => {
            const { metric } = view;
            const toneVar = view.tone === "success" ? undefined : view.tone;
            return (
              <div key={view.key} className={cn("cp-list-row", toneVar && `op-tone-${toneVar}`)}>
                <span className="cp-cell op-entity">
                  <ProviderLogo className="op-logo" providerId={metric.provider_id} providerType={view.providerType} />
                  <span className="cp-cell-main">
                    <strong title={metric.model_id}>{metric.model_id}</strong>
                    <small title={`${view.providerName} / ${view.accountLabel}`}>{view.providerName} · {view.accountLabel}</small>
                  </span>
                </span>
                <span className="cp-cell op-num">{fmtNumber(metric.requests)}</span>
                <span className={cn("cp-cell op-num op-num-pair", `cp-tone-${view.tone}`)} title={t("operations.errorsCount", { count: metric.errors })}>
                  <span className="op-toned">{fmtPercent(view.rate)}</span>
                  <small>{t("operations.errorsCount", { count: metric.errors })}</small>
                </span>
                <span className={cn("cp-cell op-num", metric.fallbacks === 0 && "op-dim")}>{fmtNumber(metric.fallbacks)}</span>
                <span className="cp-cell op-num">{fmtMs(metric.p95_ms)}</span>
                <span className={cn("cp-cell op-num", metric.ttft_p95_ms === null && "op-dim")}>{fmtMs(metric.ttft_p95_ms)}</span>
                <span className={cn("cp-cell op-num", !metric.queue_ms && "op-dim")}>{fmtMs(metric.queue_ms)}</span>
                <TokenPair top={metric.input_tokens} bottom={metric.output_tokens} />
                <TokenPair top={metric.cached_tokens} bottom={metric.cache_creation_tokens} />
                <span className="cp-cell cp-cell-actions">
                  <IconAction
                    icon="open_in_new"
                    label={t("operations.openProvider")}
                    onClick={(event) => {
                      stop(event);
                      navigate(`/providers/${encodeURIComponent(metric.provider_id)}`);
                    }}
                  />
                </span>
              </div>
            );
          })}
        </div>
      )}
      <PageNote>{t("operations.tokenHint")}</PageNote>
    </section>
  );
}

export function AccountsSection({ views, hasFilters, onClearFilters }: { views: AccountView[]; hasFilters: boolean; onClearFilters: () => void }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const now = Date.now();
  const counts = { danger: 0, warning: 0 };
  for (const view of views) if (view.tone === "danger" || view.tone === "warning") counts[view.tone] += 1;

  return (
    <section className="op-section" aria-label={t("operations.liveAccounts")}>
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>speed</span>}
        title={t("operations.liveAccounts")}
        subtitle={t("operations.accountsSubtitle", { count: views.length })}
        meta={
          <ToneCounts
            items={[
              { tone: "danger", count: counts.danger, label: t("operations.accountsFailing") },
              { tone: "warning", count: counts.warning, label: t("operations.accountsStrained") },
            ]}
          />
        }
      />
      {views.length === 0 ? (
        <EmptyState hasFilters={hasFilters} title={t("operations.noAccounts")} hint={t("operations.noAccountsHint")} onClearFilters={onClearFilters} />
      ) : (
        <div className="cp-list op-accounts">
          <div className="cp-list-head" aria-hidden>
            <span>{t("operations.accountColumn")}</span>
            <span className="op-num">{t("operations.inFlight")}</span>
            <span className="op-num">{t("operations.waiting")}</span>
            <span className="op-num">{t("operations.samples")}</span>
            <span className="op-num">{t("operations.ewma")}</span>
            <span className="op-num">{t("operations.errors")}</span>
            <span>{t("operations.quota")}</span>
            <span />
          </div>
          {views.map((view) => {
            const { account } = view;
            const quota = account.quota_remaining;
            const errTone = errorTone(account.error_rate, account.samples);
            return (
              <div key={account.credential_id} className={cn("cp-list-row", view.tone && `op-tone-${view.tone}`)}>
                <span className="cp-cell op-entity">
                  <ProviderLogo className="op-logo" providerId={view.providerId || undefined} providerType={view.providerType} />
                  <span className="cp-cell-main">
                    <strong title={view.label}>{view.label}</strong>
                    <small>{view.providerName}</small>
                  </span>
                </span>
                <span className={cn("cp-cell op-num", account.in_flight === 0 && "op-dim")}>{account.in_flight}</span>
                <span className={cn("cp-cell op-num", account.waiting > 0 ? "cp-tone-warning op-toned" : "op-dim")}>{account.waiting}</span>
                <span className="cp-cell op-num">{fmtNumber(account.samples)}</span>
                <span className={cn("cp-cell op-num", !account.latency_ms && "op-dim")}>{account.latency_ms ? fmtMs(account.latency_ms) : "—"}</span>
                <span className={cn("cp-cell op-num", `cp-tone-${errTone}`)}>
                  <span className="op-toned">{fmtPercent(account.error_rate)}</span>
                </span>
                <span className="cp-cell op-quota">
                  {quota === undefined ? (
                    <span className="op-dim op-quota-none">{t("operations.quotaUnknown")}</span>
                  ) : (
                    <>
                      <span className="op-quota-head">
                        <span className={cn("op-toned", `cp-tone-${quotaTone(quota)}`)}>{fmtPercent(quota)}</span>
                        {view.stale ? (
                          <span title={t("operations.staleHint", { age: fmtAge(account.quota_at, now) })}>
                            <Tag tone="warning">{t("operations.stale")}</Tag>
                          </span>
                        ) : null}
                      </span>
                      <span className={cn("cp-meter-track", `cp-tone-${quotaTone(quota)}`)}>
                        <span className="cp-meter-fill" style={{ width: `${Math.max(0, Math.min(1, quota)) * 100}%`, display: "block" }} />
                      </span>
                    </>
                  )}
                </span>
                <span className="cp-cell cp-cell-actions">
                  {view.providerId ? (
                    <IconAction
                      icon="open_in_new"
                      label={t("operations.openProvider")}
                      onClick={() => navigate(`/providers/${encodeURIComponent(view.providerId as string)}`)}
                    />
                  ) : null}
                </span>
              </div>
            );
          })}
        </div>
      )}
      <PageNote>{t("operations.runtimeHint")}</PageNote>
    </section>
  );
}
