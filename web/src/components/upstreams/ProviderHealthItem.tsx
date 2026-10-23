import type { KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { cn } from "../ui";
import { ActionMenu, IconAction, StatusPill, Tag, type ActionMenuItem } from "../page-kit";
import { ProviderLogo } from "../providers/ProviderLogo";
import { PROVIDER_HEALTH_TONE, type ProviderHealth, type SnapshotProvider } from "./health";

export type ProviderHealthView = {
  provider: SnapshotProvider;
  name: string;
  typeName: string;
  health: ProviderHealth;
  accounts: number;
  accountsOn: number;
  accountIssues: number;
  routes: number;
  proxyPools: number;
  lastChecked: string | null;
  discovered?: Array<{ id: string; name?: string }>;
  checking: boolean;
  discovering: boolean;
  busy: boolean;
  deleting: boolean;
  selected: boolean;
};

export type ProviderHealthHandlers = {
  open: (provider: SnapshotProvider) => void;
  select: (provider: SnapshotProvider, selected: boolean) => void;
  check: (provider: SnapshotProvider) => void;
  discover: (provider: SnapshotProvider) => void;
  remove: (provider: SnapshotProvider) => void;
};

type ItemProps = { view: ProviderHealthView; handlers: ProviderHealthHandlers };

const MODEL_PREVIEW = 6;

function stop(event: { stopPropagation: () => void }) {
  event.stopPropagation();
}

function openOnKey(event: KeyboardEvent<HTMLElement>, open: () => void) {
  if (event.target !== event.currentTarget) return;
  if (event.key === "Enter" || event.key === " ") {
    event.preventDefault();
    open();
  }
}

function HealthPill({ view }: { view: ProviderHealthView }) {
  const { t } = useTranslation();
  return (
    <StatusPill
      tone={PROVIDER_HEALTH_TONE[view.health]}
      loading={view.checking}
      title={view.provider.LastError || t(`upstreams.healthHint.${view.health}`)}
    >
      {view.checking ? t("upstreams.checking") : t(`upstreams.health.${view.health}`)}
    </StatusPill>
  );
}

function SelectBox({ view, handlers }: ItemProps) {
  const { t } = useTranslation();
  return (
    <input
      type="checkbox"
      className="cp-check"
      checked={view.selected}
      onClick={stop}
      onChange={(event) => handlers.select(view.provider, event.target.checked)}
      aria-label={t("upstreams.selectProvider", { name: view.name })}
    />
  );
}

function menuItems(view: ProviderHealthView, handlers: ProviderHealthHandlers, t: TFunction): ActionMenuItem[] {
  return [
    {
      key: "discover",
      icon: "travel_explore",
      label: t("upstreams.discover"),
      hint: t("upstreams.discoverHint"),
      disabled: view.busy || view.discovering,
      onSelect: () => handlers.discover(view.provider),
    },
    {
      key: "open",
      icon: "tune",
      label: t("upstreams.openProvider"),
      hint: t("upstreams.openProviderHint"),
      onSelect: () => handlers.open(view.provider),
    },
    {
      key: "delete",
      icon: "delete",
      label: t("upstreams.deleteProvider"),
      danger: true,
      disabled: view.busy,
      onSelect: () => handlers.remove(view.provider),
    },
  ];
}

function QuickActions({ view, handlers }: ItemProps) {
  const { t } = useTranslation();
  return (
    <div className="hl-actions" onClick={stop}>
      <IconAction
        icon="monitor_heart"
        label={t("upstreams.checkHealth")}
        spinning={view.checking}
        disabled={view.busy || view.checking}
        onClick={() => handlers.check(view.provider)}
      />
      <IconAction icon="arrow_forward" label={t("upstreams.openProvider")} onClick={() => handlers.open(view.provider)} />
      <ActionMenu label={t("upstreams.moreActions")} items={menuItems(view, handlers, t)} />
    </div>
  );
}

/** Discovered model ids as compact chips; the full list stays in a tooltip. */
function DiscoveredModels({ view }: { view: ProviderHealthView }) {
  const { t } = useTranslation();
  if (view.discovering && !view.discovered) {
    return (
      <div className="hl-models is-loading">
        <span className="material-symbols-outlined animate-spin" aria-hidden>progress_activity</span>
        <span>{t("upstreams.discovering")}</span>
      </div>
    );
  }
  if (!view.discovered) return null;
  const models = view.discovered;
  const extra = models.length - MODEL_PREVIEW;
  return (
    <div className="hl-models" onClick={stop} title={models.map((model) => model.id).join("\n")}>
      <span className="hl-models-label">
        <span className="material-symbols-outlined" aria-hidden>travel_explore</span>
        {t("upstreams.modelsFound", { count: models.length })}
      </span>
      {models.slice(0, MODEL_PREVIEW).map((model) => (
        <code key={model.id}>{model.id}</code>
      ))}
      {extra > 0 ? <span className="hl-models-more">+{extra}</span> : null}
    </div>
  );
}

function ErrorNote({ view }: { view: ProviderHealthView }) {
  const error = view.provider.LastError?.trim();
  if (!error || view.health === "healthy" || view.health === "disabled") return null;
  return (
    <div className={cn("hl-note", view.health === "auth_required" ? "cp-tone-danger" : "cp-tone-warning")}>
      <span className="material-symbols-outlined" aria-hidden>{view.health === "auth_required" ? "key_off" : "error"}</span>
      <span title={error}>{error}</span>
    </div>
  );
}

function accountsText(view: ProviderHealthView, t: TFunction) {
  if (view.accounts === 0) return t("upstreams.noAccounts");
  return t("upstreams.accountsOn", { count: view.accounts, on: view.accountsOn });
}

function itemClass(view: ProviderHealthView, base: string) {
  return cn(
    base,
    base === "hl-card" ? "cp-card" : "cp-list-row",
    `hl-health-${view.health}`,
    !view.provider.Enabled && "is-off",
    view.selected && "is-selected",
    view.deleting && "is-deleting",
  );
}

export function ProviderHealthCard({ view, handlers }: ItemProps) {
  const { t, i18n } = useTranslation();
  return (
    <article
      className={itemClass(view, "hl-card")}
      role="button"
      tabIndex={0}
      aria-label={t("upstreams.openNamed", { name: view.name })}
      onClick={() => handlers.open(view.provider)}
      onKeyDown={(event) => openOnKey(event, () => handlers.open(view.provider))}
    >
      <header className="cp-card-head">
        <SelectBox view={view} handlers={handlers} />
        <ProviderLogo className="cp-section-logo" providerId={view.provider.ID} providerType={view.provider.Type} />
        <div className="cp-card-title">
          <strong title={view.name}>{view.name}</strong>
          <div className="cp-card-tags">
            <HealthPill view={view} />
            <Tag tone="neutral" title={t("upstreams.typeHint", { type: view.provider.Type })}>{view.typeName}</Tag>
          </div>
        </div>
      </header>
      <div className="cp-card-body">
        <code className="hl-url" title={view.provider.BaseURL}>{view.provider.BaseURL || t("upstreams.localTransport")}</code>
        <dl className="cp-facts hl-facts">
          <div>
            <dt>{t("upstreams.columns.accounts")}</dt>
            <dd className={cn(view.accountIssues > 0 && "hl-warn")}>
              {accountsText(view, t)}
              {view.accountIssues > 0 ? ` · ${t("upstreams.accountIssues", { count: view.accountIssues })}` : ""}
            </dd>
          </div>
          <div>
            <dt>{t("upstreams.columns.routes")}</dt>
            <dd>{view.routes > 0 ? t("upstreams.routesCount", { count: view.routes }) : t("upstreams.noRoutes")}</dd>
          </div>
        </dl>
        <ErrorNote view={view} />
        <DiscoveredModels view={view} />
      </div>
      <footer className="cp-card-foot">
        <div className="cp-card-meta">
          <span title={view.provider.LastChecked ? new Date(view.provider.LastChecked).toLocaleString(i18n.language) : undefined}>
            <span className="material-symbols-outlined" aria-hidden>schedule</span>
            {view.lastChecked ? t("upstreams.checkedAgo", { time: view.lastChecked }) : t("upstreams.neverChecked")}
          </span>
          {view.proxyPools > 0 ? (
            <span title={(view.provider.ProxyPoolIDs || []).join(", ")}>
              <span className="material-symbols-outlined" aria-hidden>lan</span>
              {t("upstreams.proxyCount", { count: view.proxyPools })}
            </span>
          ) : null}
        </div>
        <QuickActions view={view} handlers={handlers} />
      </footer>
    </article>
  );
}

export function ProviderHealthRow({ view, handlers }: ItemProps) {
  const { t, i18n } = useTranslation();
  return (
    <div
      className={itemClass(view, "hl-row")}
      role="button"
      tabIndex={0}
      aria-label={t("upstreams.openNamed", { name: view.name })}
      onClick={() => handlers.open(view.provider)}
      onKeyDown={(event) => openOnKey(event, () => handlers.open(view.provider))}
    >
      <span className="cp-cell hl-cell-check"><SelectBox view={view} handlers={handlers} /></span>
      <span className="cp-cell hl-cell-provider">
        <ProviderLogo className="cp-section-logo" providerId={view.provider.ID} providerType={view.provider.Type} />
        <span className="cp-cell-main">
          <strong title={view.name}>{view.name}</strong>
          <small title={view.provider.BaseURL}>{view.provider.BaseURL || t("upstreams.localTransport")}</small>
        </span>
      </span>
      <span className="cp-cell hl-cell-status">
        <HealthPill view={view} />
        {view.provider.LastError && view.health !== "healthy" && view.health !== "disabled" ? (
          <small className={cn("hl-row-error", view.health === "auth_required" ? "cp-tone-danger" : "cp-tone-warning")} title={view.provider.LastError}>
            {view.provider.LastError}
          </small>
        ) : null}
      </span>
      <span className="cp-cell cp-cell-mono hl-cell-accounts" data-label={t("upstreams.columns.accounts")}>
        <span className={cn(view.accountIssues > 0 && "hl-warn")} title={view.accountIssues > 0 ? t("upstreams.accountIssues", { count: view.accountIssues }) : undefined}>
          {accountsText(view, t)}
        </span>
      </span>
      <span className="cp-cell cp-cell-mono hl-cell-routes" data-label={t("upstreams.columns.routes")}>
        {view.routes > 0 ? t("upstreams.routesCount", { count: view.routes }) : "—"}
      </span>
      <span
        className="cp-cell cp-cell-mono hl-cell-checked"
        data-label={t("upstreams.columns.lastCheck")}
        title={view.provider.LastChecked ? new Date(view.provider.LastChecked).toLocaleString(i18n.language) : undefined}
      >
        {view.lastChecked ?? t("upstreams.never")}
      </span>
      <span className="cp-cell hl-cell-actions"><QuickActions view={view} handlers={handlers} /></span>
      {view.discovered || view.discovering ? (
        <span className="cp-cell hl-cell-models"><DiscoveredModels view={view} /></span>
      ) : null}
    </div>
  );
}
