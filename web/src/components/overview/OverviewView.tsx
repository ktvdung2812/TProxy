import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { Button } from "../ui";
import {
  GuideToggle,
  LegendItem,
  PageGuide,
  PageStats,
  Toolbar,
  ToolbarEnd,
  ToolbarRow,
  ToolButton,
  usePageGuide,
  type PageStat,
} from "../page-kit";
import type { ApiKeyRecord } from "../apis/types";
import { useAccountMask } from "../../hooks/useAccountMask";
import { poolHealth, type PoolLike } from "../proxy-pools/utils";
import {
  compactNumber,
  credentialIssue,
  formatUsd,
  providerHealth,
  type SnapshotCredential,
  type SnapshotProvider,
  type SnapshotRoute,
} from "../upstreams/health";
import { AttentionPanel } from "./AttentionPanel";
import { GatewaySummary, type SummaryRow } from "./GatewaySummary";
import { OverviewApiKeysCard } from "./OverviewApiKeysCard";
import { buildAttention } from "./attention";

export type OverviewUsage = {
  requests: number;
  errors: number;
  input_tokens: number;
  output_tokens: number;
  tokens_saved: number;
  estimated_cost_usd: number;
};

type Props = {
  secret: string;
  providers: SnapshotProvider[];
  credentials: Record<string, SnapshotCredential[]>;
  proxyPools: PoolLike[];
  models: Array<{ ID: string; DisplayName?: string; Enabled: boolean }>;
  routes: Record<string, SnapshotRoute[]>;
  combos: Array<{ id: string; enabled: boolean }>;
  apiKeys: ApiKeyRecord[];
  usage?: OverviewUsage;
  loading?: boolean;
  onError: (message: string) => void;
  onMutated: () => void;
};

function percent(part: number, total: number) {
  if (!total) return 0;
  const value = (part / total) * 100;
  return value >= 10 ? Math.round(value) : Math.round(value * 10) / 10;
}

export function OverviewView({
  secret,
  providers,
  credentials,
  proxyPools,
  models,
  routes,
  combos,
  apiKeys,
  usage,
  loading,
  onError,
  onMutated,
}: Props) {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const { masked } = useAccountMask();
  const guide = usePageGuide("overview");

  const attention = useMemo(
    () => buildAttention({ providers, credentials, proxyPools, models, routes }, t, { masked, language: i18n.language }),
    [providers, credentials, proxyPools, models, routes, t, masked, i18n.language],
  );

  const accounts = useMemo(() => {
    const enabledProviders = new Set(providers.filter((provider) => provider.Enabled).map((provider) => provider.ID));
    const result = { total: 0, on: 0, ok: 0, warn: 0, auth: 0 };
    for (const [providerId, list] of Object.entries(credentials || {})) {
      for (const credential of list || []) {
        result.total += 1;
        if (!credential.enabled) continue;
        result.on += 1;
        const issue = enabledProviders.has(providerId) ? credentialIssue(credential) : null;
        if (issue === "auth") result.auth += 1;
        else if (issue) result.warn += 1;
        else result.ok += 1;
      }
    }
    return result;
  }, [providers, credentials]);

  const summaryRows = useMemo<SummaryRow[]>(() => {
    const health = { healthy: 0, degraded: 0, auth_required: 0, disabled: 0, unchecked: 0 };
    for (const provider of providers) health[providerHealth(provider)] += 1;
    const routeCount = Object.values(routes || {}).reduce((sum, list) => sum + (list || []).filter((route) => route.Enabled).length, 0);
    const pools = { healthy: 0, failing: 0, inUse: 0, on: 0 };
    for (const pool of proxyPools) {
      const state = poolHealth(pool);
      if (state === "healthy") pools.healthy += 1;
      if (state === "failing") pools.failing += 1;
      if (pool.enabled) pools.on += 1;
      if ((pool.usage_count || 0) > 0) pools.inUse += 1;
    }
    return [
      {
        key: "providers",
        icon: "dns",
        label: t("overview.summary.providers"),
        value: providers.length,
        detail: t("overview.summary.onCount", { count: providers.length - health.disabled }),
        to: "/upstreams",
        counts: [
          { tone: "success", count: health.healthy, label: t("upstreams.health.healthy") },
          { tone: "warning", count: health.degraded, label: t("upstreams.health.degraded") },
          { tone: "danger", count: health.auth_required, label: t("upstreams.health.auth_required") },
        ],
      },
      {
        key: "accounts",
        icon: "account_circle",
        label: t("overview.summary.accounts"),
        value: accounts.total,
        detail: t("overview.summary.onCount", { count: accounts.on }),
        to: "/quota",
        counts: [
          { tone: "success", count: accounts.ok, label: t("overview.summary.accountsOk") },
          { tone: "warning", count: accounts.warn, label: t("overview.summary.accountsWarn") },
          { tone: "danger", count: accounts.auth, label: t("overview.summary.accountsAuth") },
        ],
      },
      {
        key: "models",
        icon: "deployed_code",
        label: t("overview.summary.models"),
        value: models.length,
        detail: t("overview.summary.modelsDetail", { on: models.filter((model) => model.Enabled).length, routes: routeCount }),
        to: "/models",
      },
      {
        key: "combos",
        icon: "stacks",
        label: t("overview.summary.combos"),
        value: combos.length,
        detail: t("overview.summary.onCount", { count: combos.filter((combo) => combo.enabled).length }),
        to: "/combos",
      },
      {
        key: "pools",
        icon: "lan",
        label: t("overview.summary.proxyPools"),
        value: proxyPools.length,
        detail: t("overview.summary.poolsDetail", { on: pools.on, inUse: pools.inUse }),
        to: "/proxy-pools",
        counts: [
          { tone: "success", count: pools.healthy, label: t("proxyPools.health.healthy") },
          { tone: "danger", count: pools.failing, label: t("proxyPools.health.failing") },
        ],
      },
      {
        key: "keys",
        icon: "vpn_key",
        label: t("overview.summary.apiKeys"),
        value: apiKeys.length,
        detail: t("overview.summary.onCount", { count: apiKeys.filter((key) => key.enabled).length }),
        to: "/apis",
      },
    ];
  }, [providers, routes, proxyPools, models, combos, apiKeys, accounts, t]);

  const requests = usage?.requests || 0;
  const errors = usage?.errors || 0;
  const inputTokens = usage?.input_tokens || 0;
  const saved = usage?.tokens_saved || 0;
  const errorRate = percent(errors, requests);
  const attentionAccounts = accounts.warn + accounts.auth;

  const link = (to: string, page: string) => ({ onClick: () => navigate(to), title: t("overview.stats.open", { page }) });
  const stats: PageStat[] = [
    {
      key: "requests",
      icon: "bolt",
      label: t("overview.requests"),
      value: compactNumber(requests),
      hint: t("overview.stats.tokensHint", { input: compactNumber(inputTokens), output: compactNumber(usage?.output_tokens || 0) }),
      ...link("/usage", t("nav.usage")),
    },
    {
      key: "errors",
      icon: "error",
      label: t("overview.errorAttempts"),
      value: compactNumber(errors),
      tone: errors === 0 ? "success" : errorRate >= 5 ? "danger" : "warning",
      muted: errors === 0,
      hint: errors === 0 ? t("overview.stats.noErrors") : t("overview.stats.errorRate", { rate: errorRate }),
      ...link("/logs", t("nav.logs")),
    },
    {
      key: "saved",
      icon: "savings",
      label: t("overview.tokensSaved"),
      value: compactNumber(saved),
      tone: saved > 0 ? "success" : "neutral",
      muted: saved === 0,
      hint: saved > 0 ? t("overview.stats.savedShare", { rate: percent(saved, inputTokens + saved) }) : t("overview.stats.savedNone"),
      ...link("/token-saver", t("nav.tokenSaver")),
    },
    {
      key: "cost",
      icon: "payments",
      label: t("overview.estimatedCost"),
      value: formatUsd(usage?.estimated_cost_usd || 0),
      hint: t("overview.estimatedCostHint"),
      mono: true,
      ...link("/usage", t("nav.usage")),
    },
    {
      key: "accounts",
      icon: "account_circle",
      label: t("overview.stats.activeAccounts"),
      value: accounts.on,
      tone: accounts.auth > 0 ? "danger" : attentionAccounts > 0 ? "warning" : "neutral",
      hint: attentionAccounts > 0
        ? t("overview.stats.accountsAttention", { total: accounts.total, count: attentionAccounts })
        : t("overview.stats.accountsHint", { total: accounts.total }),
      ...link("/quota", t("nav.quotaTracker")),
    },
  ];

  return (
    <section className="cp-page ov-page">
      <PageStats label={t("overview.stats.label")} items={stats} />

      <Toolbar>
        <ToolbarRow>
          <div className="ov-quick" role="group" aria-label={t("overview.quickStart")}>
            <span className="ov-kicker">{t("overview.quickStart")}</span>
            <ToolButton icon="vpn_key" onClick={() => navigate("/apis")}>{t("overview.keys.create")}</ToolButton>
            <ToolButton icon="terminal" onClick={() => navigate("/cli-tools")}>{t("overview.quick.cliTools")}</ToolButton>
            <ToolButton icon="chat" onClick={() => navigate("/chat")}>{t("overview.quick.chat")}</ToolButton>
            <ToolButton icon="receipt_long" onClick={() => navigate("/logs")}>{t("overview.quick.logs")}</ToolButton>
          </div>
          <ToolbarEnd>
            <GuideToggle open={guide.open} onToggle={guide.toggle} />
            <Button variant="primary" size="sm" icon="add_link" onClick={() => navigate("/providers")}>
              {t("overview.connectProvider")}
            </Button>
          </ToolbarEnd>
        </ToolbarRow>
      </Toolbar>

      <PageGuide
        open={guide.open}
        onDismiss={guide.dismiss}
        title={t("overview.guide.title")}
        steps={[
          { icon: "add_link", text: t("overview.guide.connect") },
          { icon: "vpn_key", text: t("overview.guide.keys") },
          { icon: "notification_important", text: t("overview.guide.watch") },
        ]}
        legend={
          <>
            <LegendItem tone="success">{t("overview.guide.legendOk")}</LegendItem>
            <LegendItem tone="warning">{t("overview.guide.legendWarn")}</LegendItem>
            <LegendItem tone="danger">{t("overview.guide.legendDanger")}</LegendItem>
          </>
        }
      />

      <div className="ov-grid">
        <AttentionPanel items={attention} loading={loading} empty={!loading && providers.length === 0} />
        <GatewaySummary rows={summaryRows} />
      </div>

      <OverviewApiKeysCard secret={secret} apiKeys={apiKeys} onError={onError} onMutated={onMutated} />
    </section>
  );
}
