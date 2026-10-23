import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  AutoRefreshToggle,
  EmptyPanel,
  FilterTabs,
  GuideToggle,
  LegendItem,
  PageGuide,
  PageStats,
  SearchField,
  SelectField,
  Toolbar,
  ToolbarEnd,
  ToolbarRow,
  usePageGuide,
  useStoredState,
} from "../page-kit";
import { getProviderTypeInfo } from "../providers/catalog";
import { ProviderLogo } from "../providers/ProviderLogo";
import type { Credential } from "../providers/types";
import { Button } from "../ui";
import { maskAccountText } from "../../lib/accountMask";
import { useAccountMask } from "../../hooks/useAccountMask";
import { fetchOperations, type OperationsSnapshot } from "./api";
import { OperationsAlerts, type AlertView } from "./OperationsAlerts";
import { AccountsSection, MetricsSection, type AccountView, type MetricView } from "./OperationsTables";
import {
  POLL_INTERVAL_MS,
  accountTone,
  alertFix,
  alertSeverity,
  alertSubject,
  buildCredentialIndex,
  errorRate,
  errorTone,
  fmtClock,
  fmtMs,
  fmtNumber,
  fmtPercent,
  isQuotaStale,
  type AlertSeverity,
  type OperationsSort,
} from "./utils";

type ProviderItem = { ID: string; Type: string; Name: string };

type Props = {
  secret: string;
  providers?: ProviderItem[] | null;
  credentials?: Record<string, Credential[]>;
};

type SeverityFilter = "all" | AlertSeverity;

const SORTS: OperationsSort[] = ["attention", "busiest", "slowest", "name"];
const SEVERITY_RANK: Record<AlertSeverity, number> = { critical: 0, warning: 1 };

export function OperationsView({ secret, providers, credentials = {} }: Props) {
  const { t } = useTranslation();
  const { masked } = useAccountMask();
  const guide = usePageGuide("operations");
  const [data, setData] = useState<OperationsSnapshot>();
  const [error, setError] = useState("");
  const [refreshing, setRefreshing] = useState(false);
  const [reloadToken, setReloadToken] = useState(0);
  const [loadedAt, setLoadedAt] = useState(0);
  const [autoRefresh, setAutoRefresh] = useStoredState<"on" | "off">("tproxy.operations.autoRefresh", "on", ["on", "off"]);
  const [sort, setSort] = useStoredState<OperationsSort>("tproxy.operations.sort", "attention", SORTS);
  const [query, setQuery] = useState("");
  const [providerFilter, setProviderFilter] = useState("all");
  const [severityFilter, setSeverityFilter] = useState<SeverityFilter>("all");

  useEffect(() => {
    const controller = new AbortController();
    setRefreshing(true);
    fetchOperations(secret, controller.signal)
      .then((body) => {
        if (controller.signal.aborted) return;
        setData(body);
        setError("");
      })
      .catch((cause) => {
        if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : String(cause));
      })
      .finally(() => {
        if (controller.signal.aborted) return;
        setRefreshing(false);
        setLoadedAt(Date.now());
      });
    return () => controller.abort();
  }, [secret, reloadToken]);

  // Poll 15 s after each settled load, success or not, like the previous view.
  useEffect(() => {
    if (autoRefresh !== "on" || !loadedAt) return;
    const timer = window.setTimeout(() => setReloadToken((value) => value + 1), Math.max(0, loadedAt + POLL_INTERVAL_MS - Date.now()));
    return () => window.clearTimeout(timer);
  }, [autoRefresh, loadedAt]);

  const reload = useCallback(() => setReloadToken((value) => value + 1), []);

  const providerList = useMemo(
    () => (providers || []).map((provider) => ({ id: provider.ID, type: provider.Type, name: provider.Name || provider.ID })),
    [providers],
  );
  const providerById = useMemo(() => new Map(providerList.map((provider) => [provider.id, provider])), [providerList]);
  const credentialIndex = useMemo(() => buildCredentialIndex(credentials, providerList), [credentials, providerList]);
  const providerName = useCallback((id: string) => providerById.get(id)?.name || getProviderTypeInfo(id).name || id, [providerById]);
  const providerType = useCallback((id: string) => providerById.get(id)?.type || id, [providerById]);
  const accountLabel = useCallback(
    (credentialId: string) => maskAccountText(credentialIndex.get(credentialId)?.label || credentialId, masked),
    [credentialIndex, masked],
  );

  const metricViews = useMemo<MetricView[]>(
    () =>
      (data?.metrics || []).map((metric) => {
        const rate = errorRate(metric);
        return {
          key: `${metric.provider_id}/${metric.model_id}/${metric.credential_id}`,
          metric,
          rate,
          tone: errorTone(rate, metric.requests),
          providerName: providerName(metric.provider_id),
          providerType: providerType(metric.provider_id),
          accountLabel: accountLabel(metric.credential_id),
        };
      }),
    [data, providerName, providerType, accountLabel],
  );

  const accountViews = useMemo<AccountView[]>(
    () =>
      (data?.accounts || []).map((account) => {
        const ref = credentialIndex.get(account.credential_id);
        return {
          account,
          tone: accountTone(account),
          label: accountLabel(account.credential_id),
          providerId: ref?.providerId ?? null,
          providerName: ref ? providerName(ref.providerId) : "—",
          providerType: ref?.providerType || "",
          stale: account.quota_remaining !== undefined && isQuotaStale(account),
        };
      }),
    [data, credentialIndex, accountLabel, providerName],
  );

  const alertViews = useMemo<AlertView[]>(
    () =>
      (data?.alerts || [])
        .map((alert) => {
          const subject = alertSubject(alert, credentialIndex);
          const severity = alertSeverity(alert.kind);
          const provider = subject.providerId ? providerName(subject.providerId) : "";
          let resourceLabel = alert.resource_id;
          let resourceDetail = provider;
          let filterQuery: string | null = null;
          if (alert.kind === "high_error_rate") {
            resourceLabel = subject.credentialId ? accountLabel(subject.credentialId) : alert.resource_id;
            resourceDetail = [provider, subject.modelId].filter(Boolean).join(" · ");
            filterQuery = subject.credentialId || subject.modelId;
          } else if (alert.kind === "route_unavailable") {
            resourceDetail = t("operations.modelResource");
          } else {
            resourceLabel = accountLabel(alert.resource_id);
            filterQuery = alert.resource_id;
          }
          return { alert, severity, subject, fix: alertFix(alert, subject, credentialIndex), resourceLabel, resourceDetail, filterQuery };
        })
        .sort((left, right) => SEVERITY_RANK[left.severity] - SEVERITY_RANK[right.severity] || Date.parse(left.alert.first_seen) - Date.parse(right.alert.first_seen)),
    [data, credentialIndex, providerName, accountLabel, t],
  );

  // Providers present in this window, busiest first — the main facet.
  const providerTabs = useMemo(() => {
    const attempts = new Map<string, number>();
    for (const view of metricViews) attempts.set(view.metric.provider_id, (attempts.get(view.metric.provider_id) || 0) + view.metric.requests);
    for (const view of accountViews) if (view.providerId && !attempts.has(view.providerId)) attempts.set(view.providerId, 0);
    for (const view of alertViews) if (view.subject.providerId && !attempts.has(view.subject.providerId)) attempts.set(view.subject.providerId, 0);
    return [...attempts.entries()].sort((left, right) => right[1] - left[1] || left[0].localeCompare(right[0]));
  }, [metricViews, accountViews, alertViews]);

  const activeProvider = providerFilter !== "all" && providerTabs.some(([id]) => id === providerFilter) ? providerFilter : "all";
  const inProvider = (id: string | null) => activeProvider === "all" || id === activeProvider;
  const needle = query.trim().toLowerCase();
  const matches = (...parts: Array<string | null | undefined>) => !needle || parts.filter(Boolean).join(" ").toLowerCase().includes(needle);

  const scopedMetrics = metricViews.filter((view) => inProvider(view.metric.provider_id));
  const scopedAccounts = accountViews.filter((view) => inProvider(view.providerId));
  const scopedAlerts = alertViews.filter((view) => inProvider(view.subject.providerId));

  const visibleMetrics = scopedMetrics
    .filter((view) => matches(view.metric.provider_id, view.metric.model_id, view.metric.credential_id, view.providerName, view.accountLabel))
    .sort((left, right) => {
      switch (sort) {
        case "busiest":
          return right.metric.requests - left.metric.requests;
        case "slowest":
          return right.metric.p95_ms - left.metric.p95_ms;
        case "name":
          return left.metric.model_id.localeCompare(right.metric.model_id) || left.accountLabel.localeCompare(right.accountLabel);
        default:
          return right.rate - left.rate || right.metric.errors - left.metric.errors || right.metric.requests - left.metric.requests;
      }
    });
  const toneRank = (tone: string | null) => (tone === "danger" ? 0 : tone === "warning" ? 1 : 2);
  const visibleAccounts = scopedAccounts
    .filter((view) => matches(view.account.credential_id, view.label, view.providerName, view.providerId))
    .sort((left, right) => {
      switch (sort) {
        case "busiest":
          return right.account.in_flight + right.account.waiting - (left.account.in_flight + left.account.waiting) || right.account.samples - left.account.samples;
        case "slowest":
          return right.account.latency_ms - left.account.latency_ms;
        case "name":
          return left.label.localeCompare(right.label);
        default:
          return toneRank(left.tone) - toneRank(right.tone) || right.account.error_rate - left.account.error_rate || right.account.waiting - left.account.waiting;
      }
    });
  const visibleAlerts = scopedAlerts.filter(
    (view) =>
      (severityFilter === "all" || view.severity === severityFilter)
      && matches(view.alert.kind, t(`operations.alerts.${view.alert.kind}`, { defaultValue: view.alert.kind }), view.alert.resource_id, view.resourceLabel, view.resourceDetail),
  );

  const totals = {
    critical: scopedAlerts.filter((view) => view.severity === "critical").length,
    warning: scopedAlerts.filter((view) => view.severity === "warning").length,
  };
  const attempts = scopedMetrics.reduce((sum, view) => sum + view.metric.requests, 0);
  const errors = scopedMetrics.reduce((sum, view) => sum + view.metric.errors, 0);
  const fallbacks = scopedMetrics.reduce((sum, view) => sum + view.metric.fallbacks, 0);
  const slowest = scopedMetrics.reduce<MetricView | null>((worst, view) => (!worst || view.metric.p95_ms > worst.metric.p95_ms ? view : worst), null);
  const inFlight = scopedAccounts.reduce((sum, view) => sum + view.account.in_flight, 0);
  const waiting = scopedAccounts.reduce((sum, view) => sum + view.account.waiting, 0);
  const rate = attempts > 0 ? errors / attempts : 0;

  const hasFilters = activeProvider !== "all" || needle !== "" || severityFilter !== "all";
  const clearFilters = () => {
    setQuery("");
    setProviderFilter("all");
    setSeverityFilter("all");
  };
  const severityTile = (severity: AlertSeverity) => ({
    active: severityFilter === severity,
    onClick: () => setSeverityFilter((current) => (current === severity ? "all" : severity)),
    title: t("operations.stats.filterHint"),
  });
  const copy = (text: string) => {
    void navigator.clipboard?.writeText(text).catch(() => undefined);
  };

  if (!data) {
    return (
      <section className="cp-page op-page">
        {error ? (
          <EmptyPanel
            icon="cloud_off"
            title={t("operations.loadFailed")}
            description={<span role="alert">{error}</span>}
            action={<Button variant="secondary" icon="refresh" loading={refreshing} onClick={reload}>{t("operations.retry")}</Button>}
          />
        ) : (
          <EmptyPanel loading title={t("operations.loading")} />
        )}
      </section>
    );
  }

  return (
    <section className="cp-page op-page">
      <PageStats
        label={t("operations.statsLabel")}
        items={[
          {
            key: "critical",
            icon: "error",
            tone: "danger",
            label: t("operations.stats.critical"),
            value: totals.critical,
            hint: totals.critical ? t("operations.stats.criticalHint") : t("operations.stats.criticalNone"),
            muted: totals.critical === 0,
            ...severityTile("critical"),
          },
          {
            key: "warning",
            icon: "warning",
            tone: "warning",
            label: t("operations.stats.warning"),
            value: totals.warning,
            hint: totals.warning ? t("operations.stats.warningHint") : t("operations.stats.warningNone"),
            muted: totals.warning === 0,
            ...severityTile("warning"),
          },
          {
            key: "attempts",
            icon: "swap_vert",
            label: t("operations.stats.attempts"),
            value: fmtNumber(attempts),
            hint: t("operations.stats.attemptsHint", { errors: fmtNumber(errors), fallbacks: fmtNumber(fallbacks) }),
          },
          {
            key: "error-rate",
            icon: "percent",
            tone: attempts ? errorTone(rate, attempts) : "neutral",
            label: t("operations.stats.errorRate"),
            value: attempts ? fmtPercent(rate) : "—",
            hint: t("operations.stats.errorRateHint"),
            muted: attempts === 0,
          },
          {
            key: "p95",
            icon: "timer",
            label: t("operations.stats.p95"),
            value: slowest ? fmtMs(slowest.metric.p95_ms) : "—",
            hint: slowest ? `${slowest.metric.model_id} · ${slowest.providerName}` : t("operations.stats.p95None"),
            title: slowest ? `${slowest.metric.model_id} · ${slowest.providerName} · ${slowest.accountLabel}` : undefined,
            mono: true,
          },
          {
            key: "load",
            icon: "bolt",
            tone: waiting > 0 ? "warning" : "neutral",
            label: t("operations.stats.load"),
            value: inFlight,
            hint: t("operations.stats.loadHint", { waiting, count: scopedAccounts.length }),
          },
        ]}
      />

      <Toolbar>
        <ToolbarRow>
          <FilterTabs
            label={t("operations.filterByProvider")}
            value={activeProvider}
            onChange={(value) => setProviderFilter(value === activeProvider ? "all" : value)}
            items={[
              { value: "all", icon: "apps", label: t("operations.allProviders"), count: providerTabs.reduce((sum, [, count]) => sum + count, 0) },
              ...providerTabs.map(([id, count]) => {
                const type = providerType(id);
                const info = getProviderTypeInfo(type);
                return {
                  value: id,
                  label: providerName(id),
                  count,
                  title: t("operations.attemptsTab", { count }),
                  logo: <ProviderLogo className="cp-tab-logo" providerId={id} providerType={type} style={{ color: info.color }} />,
                };
              }),
            ]}
          />
          <ToolbarEnd>
            <span className="op-updated" title={t("operations.updated")}>
              {t("operations.updatedAt", { time: fmtClock(data.evaluated_at) })}
            </span>
            <AutoRefreshToggle
              enabled={autoRefresh === "on"}
              nextAt={loadedAt + POLL_INTERVAL_MS}
              title={t("operations.autoRefreshHint")}
              onToggle={() => setAutoRefresh((value) => (value === "on" ? "off" : "on"))}
            />
            <Button variant="primary" size="sm" icon="refresh" loading={refreshing} onClick={reload}>
              {t("common.refresh")}
            </Button>
          </ToolbarEnd>
        </ToolbarRow>
        <ToolbarRow>
          <SearchField value={query} onChange={setQuery} placeholder={t("operations.filter")} />
          <ToolbarEnd>
            <SelectField
              label={t("operations.sortBy")}
              value={sort}
              onChange={setSort}
              options={SORTS.map((value) => ({ value, label: t(`operations.sort.${value}`) }))}
            />
            <GuideToggle open={guide.open} onToggle={guide.toggle} />
          </ToolbarEnd>
        </ToolbarRow>
      </Toolbar>

      <PageGuide
        open={guide.open}
        onDismiss={guide.dismiss}
        title={t("operations.guide.title")}
        steps={[
          { icon: "notifications_active", text: t("operations.guide.alerts") },
          { icon: "build", text: t("operations.guide.fix") },
          { icon: "query_stats", text: t("operations.guide.metrics") },
        ]}
        legend={
          <>
            <LegendItem tone="danger">{t("operations.guide.legendCritical")}</LegendItem>
            <LegendItem tone="warning">{t("operations.guide.legendWarning")}</LegendItem>
            <LegendItem tone="success">{t("operations.guide.legendHealthy")}</LegendItem>
          </>
        }
      />

      {error ? (
        <p role="alert" className="op-error">
          <span className="material-symbols-outlined" aria-hidden>sync_problem</span>
          <span>{t("operations.loadFailedStale", { time: fmtClock(data.evaluated_at) })}</span>
          <code title={error}>{error}</code>
          <button type="button" className="cp-link-btn" onClick={reload}>{t("operations.retry")}</button>
        </p>
      ) : null}

      <OperationsAlerts
        views={visibleAlerts}
        totals={totals}
        evaluatedAt={data.evaluated_at}
        hasFilters={hasFilters}
        onClearFilters={clearFilters}
        onFilterMetrics={(value) => {
          setSeverityFilter("all");
          setQuery(value);
        }}
        onCopy={copy}
      />
      <MetricsSection views={visibleMetrics} hasFilters={hasFilters} onClearFilters={clearFilters} />
      <AccountsSection views={visibleAccounts} hasFilters={hasFilters} onClearFilters={clearFilters} />
    </section>
  );
}
