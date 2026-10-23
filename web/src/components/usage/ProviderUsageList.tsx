import { useMemo, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { EmptyPanel, SectionHead, Segmented, StatusPill, useStoredState } from "../page-kit";
import { getProviderTypeInfo } from "../providers/catalog";
import { ProviderLogo } from "../providers/ProviderLogo";
import { cn } from "../ui";
import type { UsageStats } from "./api";
import { ProviderTopology } from "./ProviderTopology";
import { fmt, fmtCompact, fmtCost, fmtTime, type UsageValueMode } from "./utils";

export type ProviderItem = { id: string; name: string; type: string; enabled: boolean };

type ProviderRow = {
  id: string;
  name: string;
  type: string;
  requests: number;
  tokens: number;
  cost: number;
  lastUsed?: string;
  inFlight: number;
  failing: boolean;
};

type Props = {
  stats: UsageStats;
  providers: ProviderItem[];
  valueMode: UsageValueMode;
  selected: string;
  onSelect: (id: string) => void;
};

const same = (left: string | undefined, right: string | undefined) => Boolean(left && right && left.toLowerCase() === right.toLowerCase());

/**
 * Where the period's traffic went: one row per provider with its share of the
 * selected measure, plus live in-flight and failure markers from the stream.
 * The map view keeps the previous live routing graph.
 */
export function ProviderUsagePanel({ stats, providers, valueMode, selected, onSelect }: Props) {
  const { t } = useTranslation();
  const [view, setView] = useStoredState<"list" | "map">("tproxy.usage.providerView", "list", ["list", "map"]);
  const active = useMemo(() => stats.activeRequests ?? [], [stats.activeRequests]);

  const rows = useMemo<ProviderRow[]>(() => {
    const byId = new Map<string, ProviderRow>();
    const known = new Map(providers.map((provider) => [provider.id, provider]));
    const make = (id: string, name: string, type: string): ProviderRow => ({
      id,
      name,
      type,
      requests: 0,
      tokens: 0,
      cost: 0,
      inFlight: 0,
      failing: false,
    });
    for (const [id, bucket] of Object.entries(stats.byProvider || {})) {
      const provider = known.get(id);
      const row = make(id, provider?.name || bucket.provider || getProviderTypeInfo(id).name || id, provider?.type || id);
      row.requests = bucket.requests || 0;
      row.tokens = (bucket.promptTokens || 0) + (bucket.completionTokens || 0);
      row.cost = bucket.cost || 0;
      row.lastUsed = bucket.lastUsed;
      byId.set(id, row);
    }
    // Connected providers without traffic still show, so "idle" is visible too.
    for (const provider of providers) {
      if (!byId.has(provider.id)) byId.set(provider.id, make(provider.id, provider.name || provider.id, provider.type));
    }
    for (const row of byId.values()) {
      row.inFlight = active.filter((item) => same(item.provider, row.id) || same(item.provider, row.name)).length;
      row.failing = same(stats.errorProvider, row.id) || same(stats.errorProvider, row.name);
    }
    const measure = (row: ProviderRow) => (valueMode === "costs" ? row.cost : row.tokens);
    return [...byId.values()].sort((a, b) => measure(b) - measure(a) || b.requests - a.requests || a.name.localeCompare(b.name));
  }, [stats.byProvider, stats.errorProvider, active, providers, valueMode]);

  const total = rows.reduce((sum, row) => sum + (valueMode === "costs" ? row.cost : row.tokens), 0);
  const withTraffic = rows.filter((row) => row.requests > 0).length;

  const onKey = (event: KeyboardEvent<HTMLDivElement>, id: string) => {
    if (event.target !== event.currentTarget) return;
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      onSelect(id);
    }
  };

  return (
    <section className="us-section" aria-label={t("usage.providers.title")}>
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>hub</span>}
        title={t("usage.providers.title")}
        subtitle={t("usage.providers.subtitle", { count: withTraffic })}
        actions={
          <Segmented
            label={t("usage.providers.view")}
            value={view}
            onChange={setView}
            items={[
              { value: "list", label: t("usage.providers.asList"), icon: "view_list" },
              { value: "map", label: t("usage.providers.asMap"), icon: "hub" },
            ]}
          />
        }
      />
      {view === "map" ? (
        <div className="us-panel us-map">
          <ProviderTopology
            providers={providers}
            activeRequests={active}
            lastProvider={stats.recentRequests?.[0]?.provider || ""}
            errorProvider={stats.errorProvider || ""}
          />
        </div>
      ) : rows.length === 0 ? (
        <div className="us-panel">
          <EmptyPanel compact icon="hub" title={t("usage.noProvidersWithConnections")} description={t("usage.providers.emptyHint")} />
        </div>
      ) : (
        <div className="cp-list us-providers">
          <div className="cp-list-head" aria-hidden>
            <span>{t("usage.provider")}</span>
            <span>{t(valueMode === "costs" ? "usage.providers.shareCost" : "usage.providers.shareTokens")}</span>
            <span className="us-num">{t(valueMode === "costs" ? "usage.cost" : "usage.tokens")}</span>
            <span className="us-num">{t("usage.requests")}</span>
          </div>
          {rows.map((row) => {
            const value = valueMode === "costs" ? row.cost : row.tokens;
            const share = total > 0 ? value / total : 0;
            const isSelected = selected === row.id;
            return (
              <div
                key={row.id}
                role="button"
                tabIndex={0}
                aria-pressed={isSelected}
                title={isSelected ? t("usage.providers.clearFilter") : t("usage.providers.filterHint", { name: row.name })}
                className={cn("cp-list-row", isSelected && "is-selected", row.failing && "us-tone-danger", row.requests === 0 && "us-idle")}
                onClick={() => onSelect(row.id)}
                onKeyDown={(event) => onKey(event, row.id)}
              >
                <span className="cp-cell us-entity">
                  <ProviderLogo className="us-logo" providerId={row.id} providerType={row.type} />
                  <span className="cp-cell-main">
                    <strong title={row.name}>{row.name}</strong>
                    <small className="us-provider-sub">
                      {row.inFlight > 0 ? (
                        <StatusPill tone="success" loading>{t("usage.providers.inFlight", { count: row.inFlight })}</StatusPill>
                      ) : null}
                      {row.failing ? <StatusPill tone="danger">{t("usage.providers.lastFailed")}</StatusPill> : null}
                      {row.inFlight === 0 && !row.failing ? (row.requests > 0 ? fmtTime(row.lastUsed, t) : t("usage.providers.idle")) : null}
                    </small>
                  </span>
                </span>
                <span className="cp-cell us-share" title={`${(share * 100).toFixed(1)}%`}>
                  <span className="us-share-track">
                    <span className="us-share-fill" style={{ width: `${Math.max(share > 0 ? 2 : 0, share * 100)}%` }} />
                  </span>
                  <span className="us-share-pct">{share > 0 ? `${Math.round(share * 100)}%` : "—"}</span>
                </span>
                <span className="cp-cell us-num" title={valueMode === "costs" ? fmtCost(row.cost) : fmt(row.tokens)}>
                  {valueMode === "costs" ? fmtCost(row.cost) : fmtCompact(row.tokens)}
                </span>
                <span className={cn("cp-cell us-num", row.requests === 0 && "us-dim")}>{fmtCompact(row.requests)}</span>
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}
