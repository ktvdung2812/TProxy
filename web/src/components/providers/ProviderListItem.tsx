import type { KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { ActionMenu, IconAction, StatusPill, Tag, type ActionMenuItem } from "../page-kit";
import { cn } from "../ui";
import { ProviderLogo } from "./ProviderLogo";
import {
  ACCOUNT_STATE_TONE,
  PROVIDER_HEALTH_TONE,
  relativeAge,
  timeLeft,
  type AccountCounts,
  type AccountState,
  type ProviderHealth,
  type ProviderKind,
} from "./providerHealth";
import type { Provider } from "./types";

/** Generic adapter types share one logo; a preset id names the real brand. */
const GENERIC_LOGO_TYPES = new Set(["openai-compatible", "anthropic-compatible", "image", "video", "tavily", "elevenlabs"]);

export function providerLogoId(providerId: string | undefined, providerType: string, isPreset: boolean) {
  return isPreset && providerId && GENERIC_LOGO_TYPES.has(providerType) ? providerId : undefined;
}

export type ProviderView = {
  provider: Provider;
  name: string;
  kind: ProviderKind;
  health: ProviderHealth;
  counts: AccountCounts;
  noAuth: boolean;
  isPreset: boolean;
  color: string;
  /** Soonest cooldown expiry among the provider's accounts. */
  cooldownUntil: string | null;
  checking: boolean;
};

export type ProviderItemHandlers = {
  open: (provider: Provider) => void;
  check: (provider: Provider) => void;
  copyId: (provider: Provider) => void;
  remove: (provider: Provider) => void;
};

type ItemProps = { view: ProviderView; handlers: ProviderItemHandlers; now: number };

const STATE_ORDER: AccountState[] = ["ready", "degraded", "unknown", "cooldown", "attention", "off"];

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

export function HealthPill({ health, title }: { health: ProviderHealth; title?: string }) {
  const { t } = useTranslation();
  return (
    <StatusPill tone={PROVIDER_HEALTH_TONE[health]} title={title ?? t(`providers.healthHint.${health}`)}>
      {t(`providers.health.${health}`)}
    </StatusPill>
  );
}

/** One segment per account state, so the mix of a provider's accounts reads at a glance. */
export function AccountsBar({ counts, compact }: { counts: AccountCounts; compact?: boolean }) {
  const { t } = useTranslation();
  if (counts.total === 0) return null;
  const parts = STATE_ORDER.filter((state) => counts[state] > 0);
  const label = parts.map((state) => `${counts[state]} ${t(`providers.accountState.${state}`)}`).join(" · ");
  return (
    <div className={cn("pv-bar", compact && "is-compact")} role="img" aria-label={label} title={label}>
      {parts.map((state) => (
        <span
          key={state}
          className={`cp-tone-${ACCOUNT_STATE_TONE[state]}`}
          style={{ flexGrow: counts[state] }}
        />
      ))}
    </div>
  );
}

function AccountSummary({ view, now }: { view: ProviderView; now: number }) {
  const { t } = useTranslation();
  const { counts } = view;
  if (counts.total === 0) {
    return (
      <p className="pv-card-empty">
        <span className="material-symbols-outlined" aria-hidden>{view.noAuth ? "lock_open" : "person_add"}</span>
        {view.noAuth ? t("providers.list.noAuthNeeded") : t("providers.list.noAccountsYet")}
      </p>
    );
  }
  const cooldown = timeLeft(view.cooldownUntil ?? undefined, now);
  return (
    <div className="pv-accounts">
      <div className="pv-accounts-head">
        <span>{t("providers.list.accountsSummary", { count: counts.total, on: counts.on })}</span>
        <span className="pv-accounts-states">
          {counts.cooldown > 0 ? (
            <span className="cp-tone-warning" title={cooldown ? t("providers.list.cooldownClears", { time: cooldown }) : undefined}>
              <span className="material-symbols-outlined" aria-hidden>timer</span>
              {cooldown ? t("providers.list.cooldownFor", { count: counts.cooldown, time: cooldown }) : t("providers.list.cooldownCount", { count: counts.cooldown })}
            </span>
          ) : null}
          {counts.attention > 0 ? (
            <span className="cp-tone-danger">
              <span className="material-symbols-outlined" aria-hidden>error</span>
              {t("providers.list.attentionCount", { count: counts.attention })}
            </span>
          ) : null}
        </span>
      </div>
      <AccountsBar counts={counts} />
    </div>
  );
}

function menuItems(view: ProviderView, handlers: ProviderItemHandlers, t: TFunction): ActionMenuItem[] {
  const { provider } = view;
  return [
    { key: "open", icon: "open_in_new", label: t("providers.menu.open"), hint: t("providers.menu.openHint"), onSelect: () => handlers.open(provider) },
    {
      key: "check",
      icon: "monitor_heart",
      label: t("providers.healthCheck"),
      hint: t("providers.menu.checkHint"),
      disabled: view.checking,
      onSelect: () => handlers.check(provider),
    },
    { key: "copy", icon: "content_copy", label: t("providers.menu.copyId"), hint: provider.ID, onSelect: () => handlers.copyId(provider) },
    { key: "delete", icon: "delete", label: t("providers.deleteProvider"), danger: true, onSelect: () => handlers.remove(provider) },
  ];
}

function QuickActions({ view, handlers }: Omit<ItemProps, "now">) {
  const { t } = useTranslation();
  return (
    <div className="pv-actions" onClick={stop}>
      <IconAction
        icon="monitor_heart"
        label={t("providers.menu.checkNamed", { name: view.name })}
        spinning={view.checking}
        disabled={view.checking}
        onClick={() => handlers.check(view.provider)}
      />
      <ActionMenu label={t("providers.menu.more")} items={menuItems(view, handlers, t)} />
    </div>
  );
}

function Logo({ view, className }: { view: ProviderView; className: string }) {
  return (
    <ProviderLogo
      className={className}
      providerId={view.provider.ID}
      providerType={view.provider.Type}
      logoId={providerLogoId(view.provider.ID, view.provider.Type, view.isPreset)}
      style={{ color: view.color }}
    />
  );
}

function errorNote(view: ProviderView) {
  const error = view.provider.LastError?.trim();
  if (!error || view.health === "off" || view.health === "healthy") return null;
  return error;
}

export function ProviderCard({ view, handlers, now }: ItemProps) {
  const { t } = useTranslation();
  const error = errorNote(view);
  const checked = relativeAge(view.provider.LastChecked, now);
  const pools = view.provider.ProxyPoolIDs?.length ?? 0;
  return (
    <article
      className={cn("cp-card pv-card", `pv-health-${view.health}`, view.health === "off" && "is-off")}
      role="button"
      tabIndex={0}
      aria-label={t("providers.list.openNamed", { name: view.name })}
      onClick={() => handlers.open(view.provider)}
      onKeyDown={(event) => openOnKey(event, () => handlers.open(view.provider))}
    >
      <header className="cp-card-head">
        <Logo view={view} className="pv-card-logo" />
        <div className="cp-card-title">
          <strong title={view.name}>{view.name}</strong>
          <div className="cp-card-tags">
            <HealthPill health={view.health} />
            <Tag tone="neutral">{t(`providers.kind.${view.kind}`)}</Tag>
            {view.provider.ID !== view.name ? <code className="pv-id" title={view.provider.ID}>{view.provider.ID}</code> : null}
          </div>
        </div>
      </header>
      <div className="cp-card-body">
        <AccountSummary view={view} now={now} />
        {error ? (
          <p className="pv-note pv-note-error" title={error}>
            <span className="material-symbols-outlined" aria-hidden>error</span>
            <span>{error}</span>
          </p>
        ) : null}
      </div>
      <footer className="cp-card-foot">
        <div className="cp-card-meta">
          <span title={view.provider.LastChecked ? new Date(view.provider.LastChecked).toLocaleString() : undefined}>
            <span className="material-symbols-outlined" aria-hidden>schedule</span>
            {checked ? t("providers.list.checkedAgo", { time: checked }) : t("providers.list.neverChecked")}
          </span>
          {pools > 0 ? (
            <span title={view.provider.ProxyPoolIDs?.join(", ")}>
              <span className="material-symbols-outlined" aria-hidden>lan</span>
              {t("providers.list.proxyPools", { count: pools })}
            </span>
          ) : null}
        </div>
        <QuickActions view={view} handlers={handlers} />
      </footer>
    </article>
  );
}

export function ProviderRow({ view, handlers, now }: ItemProps) {
  const { t } = useTranslation();
  const error = errorNote(view);
  const checked = relativeAge(view.provider.LastChecked, now);
  const { counts } = view;
  return (
    <div
      className={cn("cp-list-row pv-row", `pv-health-${view.health}`, view.health === "off" && "is-off")}
      role="button"
      tabIndex={0}
      aria-label={t("providers.list.openNamed", { name: view.name })}
      onClick={() => handlers.open(view.provider)}
      onKeyDown={(event) => openOnKey(event, () => handlers.open(view.provider))}
    >
      <span className="cp-cell pv-row-name">
        <Logo view={view} className="pv-row-logo" />
        <span className="cp-cell-main">
          <strong title={view.name}>{view.name}</strong>
          <small title={error || view.provider.BaseURL}>{error || view.provider.ID}</small>
        </span>
      </span>
      <span className="cp-cell"><Tag tone="neutral">{t(`providers.kind.${view.kind}`)}</Tag></span>
      <span className="cp-cell"><HealthPill health={view.health} /></span>
      <span className="cp-cell pv-row-accounts">
        {counts.total > 0 ? (
          <>
            <span className="cp-cell-mono">{t("providers.list.accountsShort", { count: counts.total, on: counts.on })}</span>
            <AccountsBar counts={counts} compact />
          </>
        ) : (
          <span className="cp-cell-mono">{view.noAuth ? t("providers.list.noAuthShort") : "—"}</span>
        )}
      </span>
      <span className="cp-cell cp-cell-mono">{checked ? t("providers.list.checkedAgo", { time: checked }) : "—"}</span>
      <span className="cp-cell cp-cell-actions"><QuickActions view={view} handlers={handlers} /></span>
    </div>
  );
}
