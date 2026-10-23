import { memo, useEffect, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { EmptyPanel, SectionHead, Segmented, Tag } from "../page-kit";
import { ProviderLogo } from "../providers/ProviderLogo";
import { cn } from "../ui";
import type { UsageActiveRequest, UsageRecentRequest } from "./api";
import type { ProviderItem } from "./ProviderUsageList";
import type { RecentFilter } from "./UsageView";
import { fmt, fmtCompact, fmtTime, isOkStatus, recentRequestKey } from "./utils";

type Props = {
  requests: UsageRecentRequest[];
  activeRequests: UsageActiveRequest[];
  providers: ProviderItem[];
  filter: RecentFilter;
  onFilter: (value: RecentFilter) => void;
  streamPill: ReactNode;
};

/** Live activity from the usage stream: what is in flight now and the last finished requests. */
function RecentRequestsPanel({ requests, activeRequests, providers, filter, onFilter, streamPill }: Props) {
  const { t } = useTranslation();
  const [, setTick] = useState(0);

  // Re-render every 30 s so "2m ago" stays honest without new data.
  useEffect(() => {
    const timer = window.setInterval(() => setTick((value) => value + 1), 30_000);
    return () => window.clearInterval(timer);
  }, []);

  const resolve = useMemo(() => {
    const byKey = new Map<string, ProviderItem>();
    for (const provider of providers) {
      byKey.set(provider.id.toLowerCase(), provider);
      if (provider.name) byKey.set(provider.name.toLowerCase(), provider);
    }
    return (value?: string) => (value ? byKey.get(value.toLowerCase()) : undefined);
  }, [providers]);

  const inFlight = useMemo(() => {
    const counts = new Map<string, { label: string; id: string; type: string; count: number }>();
    for (const item of activeRequests) {
      const provider = resolve(item.provider);
      const key = provider?.id || item.provider || "?";
      const current = counts.get(key) ?? { label: provider?.name || item.provider || "?", id: key, type: provider?.type || key, count: 0 };
      current.count += 1;
      counts.set(key, current);
    }
    return [...counts.values()].sort((a, b) => b.count - a.count);
  }, [activeRequests, resolve]);

  const errors = requests.filter((item) => !isOkStatus(item.status)).length;
  const visible = filter === "errors" ? requests.filter((item) => !isOkStatus(item.status)) : requests;

  return (
    <section className="us-section" aria-label={t("usage.live.title")}>
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>bolt</span>}
        title={t("usage.live.title")}
        titleExtra={streamPill}
        subtitle={t("usage.live.subtitle", { count: requests.length })}
        actions={
          <Segmented
            label={t("usage.live.filter")}
            value={filter}
            onChange={onFilter}
            items={[
              { value: "all", label: t("usage.live.all") },
              { value: "errors", label: t("usage.live.errors", { count: errors }) },
            ]}
          />
        }
      />
      <div className="us-panel us-live">
        <div className="us-inflight" aria-live="polite">
          <span className="us-inflight-label">{t("usage.live.inFlight")}</span>
          {inFlight.length === 0 ? (
            <span className="us-dim">{t("usage.live.nothingInFlight")}</span>
          ) : (
            inFlight.map((item) => (
              <span key={item.id} className="us-inflight-item">
                <ProviderLogo className="us-logo is-small" providerId={item.id} providerType={item.type} />
                <span>{item.label}</span>
                <Tag tone="success">×{item.count}</Tag>
              </span>
            ))
          )}
        </div>
        {visible.length === 0 ? (
          <EmptyPanel
            compact
            icon={filter === "errors" ? "check_circle" : "hourglass_empty"}
            title={filter === "errors" ? t("usage.live.noErrors") : t("usage.noRequestsYet")}
            description={filter === "errors" ? undefined : t("usage.live.emptyHint")}
            action={filter === "errors" ? <button type="button" className="cp-link-btn" onClick={() => onFilter("all")}>{t("usage.live.showAll")}</button> : undefined}
          />
        ) : (
          <div className="us-live-list custom-scrollbar">
            {visible.map((item) => {
              const ok = isOkStatus(item.status);
              const provider = resolve(item.provider);
              return (
                <div key={recentRequestKey(item)} className={cn("us-live-row", !ok && "is-error")}>
                  <span className={cn("us-dot", ok ? "cp-tone-success" : "cp-tone-danger")} title={ok ? t("usage.guide.legendOk") : t("usage.guide.legendError")} aria-label={ok ? t("usage.guide.legendOk") : t("usage.guide.legendError")} />
                  <span className="us-live-main">
                    <strong title={item.model}>{item.model}</strong>
                    <small>
                      {provider?.name || item.provider}
                      {!ok ? <span className="us-live-status"> · {item.status}</span> : null}
                    </small>
                  </span>
                  <span className="us-live-io" title={`${t("usage.input")} ${fmt(item.promptTokens)} · ${t("usage.output")} ${fmt(item.completionTokens)}`}>
                    <span>{fmtCompact(item.promptTokens)}<i aria-hidden>↑</i></span>
                    <span>{fmtCompact(item.completionTokens)}<i aria-hidden>↓</i></span>
                  </span>
                  <span className="us-live-when" title={new Date(item.timestamp).toLocaleString()}>{fmtTime(item.timestamp, t)}</span>
                </div>
              );
            })}
          </div>
        )}
      </div>
    </section>
  );
}

export const RecentRequests = memo(RecentRequestsPanel);
