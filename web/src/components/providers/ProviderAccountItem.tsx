import { useState, type DragEvent, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { Toggle, cn } from "../ui";
import { ActionMenu, IconAction, StatusPill, Tag, type ActionMenuItem } from "../page-kit";
import { formatResetTime } from "../quota/utils";
import { ACCOUNT_STATE_TONE, relativeAge, timeLeft, type AccountState } from "./providerHealth";
import type { QuotaView } from "./useAccountQuotas";
import type { Credential } from "./types";

export type AccountView = {
  credential: Credential;
  label: string;
  /** Email when the label is something else, already masked. */
  secondary: string | null;
  /** Stable account number by first-seen time (#1, #2…). */
  number?: number;
  addedAt: string;
  state: AccountState;
  /** 1-based routing position. */
  rank: number;
  canReorder: boolean;
  supportsQuota: boolean;
  quota?: QuotaView;
  quotaBusy: boolean;
  usageLabel: string | null;
  /** Action currently running for this account, if any. */
  busy: string | null;
  selected: boolean;
  dragging: boolean;
  dragOver: boolean;
  canReAuth: boolean;
};

export type AccountHandlers = {
  edit: (credential: Credential) => void;
  select: (credential: Credential, selected: boolean) => void;
  setEnabled: (credential: Credential, enabled: boolean) => void;
  health: (credential: Credential) => void;
  refreshQuota: (credential: Credential) => void;
  refreshToken: (credential: Credential) => void;
  clearCooldown: (credential: Credential) => void;
  reAuth: (credential: Credential) => void;
  models: (credential: Credential) => void;
  exportOne: (credential: Credential) => void;
  remove: (credential: Credential) => void;
  dragStart: (credential: Credential, event: DragEvent<HTMLElement>) => void;
  dragEnd: () => void;
  dragOver: (credential: Credential, event: DragEvent<HTMLElement>) => void;
  drop: (credential: Credential, event: DragEvent<HTMLElement>) => void;
};

type ItemProps = { view: AccountView; handlers: AccountHandlers; reorderLocked: boolean; now: number };

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

function StatePill({ view, now }: { view: AccountView; now: number }) {
  const { t } = useTranslation();
  const left = view.state === "cooldown" ? timeLeft(view.credential.cooldown_until, now) : null;
  return (
    <StatusPill
      tone={ACCOUNT_STATE_TONE[view.state]}
      title={view.credential.cooldown_until && left ? `${t("providers.cooldownUntil")} ${new Date(view.credential.cooldown_until).toLocaleString()}` : t(`providers.accountStateHint.${view.state}`)}
    >
      {left ? t("providers.account.cooldownFor", { time: left }) : t(`providers.accountState.${view.state}`)}
    </StatusPill>
  );
}

function authLabel(authType: string, t: TFunction) {
  if (authType === "oauth") return "OAuth";
  if (authType === "api_key") return t("providers.authTypeApiKey");
  if (authType === "service_account") return t("providers.authTypeServiceAccount");
  if (authType === "none") return t("providers.authTypeNone");
  return authType;
}

/** The most useful fix for the account's state, shown as its one quick action. */
function primaryAction(view: AccountView, handlers: AccountHandlers, t: TFunction) {
  const { credential } = view;
  if (view.state === "cooldown" && credential.cooldown_until) {
    return { icon: "timer_off", label: t("providers.clearCooldown"), run: () => handlers.clearCooldown(credential), busy: view.busy === "cooldown" };
  }
  if (view.canReAuth) {
    return { icon: "lock_reset", label: t("providers.reAuthenticate"), run: () => handlers.reAuth(credential), busy: false };
  }
  if (view.supportsQuota) {
    return { icon: "refresh", label: t("providers.refreshQuota"), run: () => handlers.refreshQuota(credential), busy: view.quotaBusy };
  }
  return { icon: "monitor_heart", label: t("providers.healthCheckConnection"), run: () => handlers.health(credential), busy: view.busy === "health" };
}

function menuItems(view: AccountView, handlers: AccountHandlers, t: TFunction): ActionMenuItem[] {
  const { credential } = view;
  const busy = view.busy !== null;
  const items: Array<ActionMenuItem | null> = [
    { key: "edit", icon: "tune", label: t("providers.editCredential"), hint: t("providers.account.editHint"), onSelect: () => handlers.edit(credential) },
    { key: "models", icon: "apps", label: t("providers.viewSupportedModels"), onSelect: () => handlers.models(credential) },
    { key: "health", icon: "monitor_heart", label: t("providers.healthCheckConnection"), disabled: busy, onSelect: () => handlers.health(credential) },
    view.supportsQuota
      ? { key: "quota", icon: "donut_large", label: t("providers.refreshQuota"), disabled: view.quotaBusy, onSelect: () => handlers.refreshQuota(credential) }
      : null,
    credential.auth_type === "oauth"
      ? { key: "token", icon: "sync", label: t("providers.refreshToken"), disabled: busy, onSelect: () => handlers.refreshToken(credential) }
      : null,
    view.state === "cooldown" && credential.cooldown_until
      ? { key: "cooldown", icon: "timer_off", label: t("providers.clearCooldown"), disabled: busy, onSelect: () => handlers.clearCooldown(credential) }
      : null,
    view.canReAuth
      ? { key: "reauth", icon: "lock_reset", label: t("providers.reAuthenticate"), onSelect: () => handlers.reAuth(credential) }
      : null,
    { key: "export", icon: "download", label: t("providers.exportAccount"), onSelect: () => handlers.exportOne(credential) },
    { key: "delete", icon: "delete", label: t("providers.deleteCredential"), danger: true, disabled: busy, onSelect: () => handlers.remove(credential) },
  ];
  return items.filter(Boolean) as ActionMenuItem[];
}

function QuickActions({ view, handlers }: Pick<ItemProps, "view" | "handlers">) {
  const { t } = useTranslation();
  const action = primaryAction(view, handlers, t);
  return (
    <div className="pv-actions" onClick={stop}>
      <IconAction icon={action.icon} label={action.label} spinning={action.busy} disabled={action.busy || view.busy !== null} onClick={action.run} />
      <ActionMenu label={t("providers.menu.moreAccount")} items={menuItems(view, handlers, t)} />
    </div>
  );
}

function RankHandle({ view, handlers, reorderLocked }: Pick<ItemProps, "view" | "handlers" | "reorderLocked">) {
  const { t } = useTranslation();
  if (!view.canReorder) {
    return <span className="pv-rank" title={t("providers.account.rankHint", { rank: view.rank })}>{view.rank}</span>;
  }
  return (
    <button
      type="button"
      className="pv-rank pv-rank-handle"
      draggable={!reorderLocked}
      disabled={reorderLocked}
      onClick={stop}
      onDragStart={(event) => handlers.dragStart(view.credential, event)}
      onDragEnd={handlers.dragEnd}
      aria-label={t("providers.account.dragNamed", { label: view.label })}
      title={t("providers.account.rankDragHint", { rank: view.rank })}
    >
      <span className="material-symbols-outlined" aria-hidden>drag_indicator</span>
      <span>{view.rank}</span>
    </button>
  );
}

function EnabledToggle({ view, handlers }: Pick<ItemProps, "view" | "handlers">) {
  const { t } = useTranslation();
  const label = view.credential.enabled ? t("providers.account.turnOff") : t("providers.account.turnOn");
  return (
    <span className="pv-toggle" onClick={stop} title={label}>
      <Toggle
        checked={view.credential.enabled}
        disabled={view.busy !== null}
        onChange={(event) => handlers.setEnabled(view.credential, event.target.checked)}
        aria-label={label}
      />
    </span>
  );
}

function SelectBox({ view, handlers }: Pick<ItemProps, "view" | "handlers">) {
  const { t } = useTranslation();
  return (
    <input
      type="checkbox"
      className="cp-check"
      checked={view.selected}
      onClick={stop}
      onChange={(event) => handlers.select(view.credential, event.target.checked)}
      aria-label={t("providers.selectAccount", { id: view.label })}
    />
  );
}

/** Remaining quota per window; long lists collapse behind "+N more". */
function QuotaList({ view, inline }: { view: AccountView; inline?: boolean }) {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState(false);
  if (!view.supportsQuota) return null;
  if (view.quotaBusy && !view.quota) {
    return (
      <div className={cn("cp-skeleton", inline && "pv-skeleton-inline")} aria-label={t("providers.account.checkingQuota")}>
        <span />
        {!inline ? <span /> : null}
      </div>
    );
  }
  const rows = view.quota?.rows ?? [];
  if (rows.length === 0) {
    const message = view.quota?.message;
    if (!message || inline) return inline && message ? <span className="pv-quota-empty" title={message}>{t("providers.quotaUnavailable")}</span> : null;
    return (
      <p className="pv-note" title={message}>
        <span className="material-symbols-outlined" aria-hidden>data_usage</span>
        <span>{message}</span>
      </p>
    );
  }
  const limit = 3;
  const collapsible = rows.length > limit + 1;
  const ordered = collapsible ? [...rows].sort((a, b) => (a.percent ?? 101) - (b.percent ?? 101)) : rows;
  const shown = collapsible && !expanded ? ordered.slice(0, limit) : ordered;
  return (
    <div className={cn("pv-quota", inline && "is-inline")}>
      {shown.map((row) => {
        const reset = formatResetTime(row.resetAt).replace(/^(\d+d \d+h) \d+m$/, "$1");
        return (
          <div key={row.key} className={cn("pv-quota-row", `cp-tone-${row.tone}`)}>
            <div className="pv-quota-head">
              <span className="pv-quota-name" title={row.name}>{row.name}</span>
              <span className="pv-quota-value">{row.value}</span>
              {!inline && reset !== "-" ? <span className="pv-quota-reset" title={t("providers.account.resetsIn", { time: reset })}>{reset}</span> : null}
            </div>
            {row.percent !== null ? (
              <div className="cp-meter-track" role="meter" aria-label={row.name} aria-valuemin={0} aria-valuemax={100} aria-valuenow={row.percent}>
                <div className="cp-meter-fill" style={{ width: `${row.percent}%` }} />
              </div>
            ) : null}
          </div>
        );
      })}
      {collapsible ? (
        <button
          type="button"
          className="pv-quota-more"
          onClick={(event) => {
            event.stopPropagation();
            setExpanded((value) => !value);
          }}
          aria-expanded={expanded}
        >
          <span className="material-symbols-outlined" aria-hidden>{expanded ? "expand_less" : "expand_more"}</span>
          {expanded ? t("providers.account.showLess") : t("providers.account.showMore", { count: rows.length - shown.length })}
        </button>
      ) : null}
    </div>
  );
}

function itemClasses(view: AccountView, base: "pv-account" | "pv-account-row") {
  return cn(
    base,
    base === "pv-account" ? "cp-card" : "cp-list-row",
    `pv-acc-${view.state}`,
    !view.credential.enabled && "is-off",
    view.selected && "is-selected",
    view.dragging && "is-dragging",
    view.dragOver && "is-drag-over",
    view.busy === "delete" && "is-deleting",
  );
}

export function AccountCard(props: ItemProps) {
  const { view, handlers, now } = props;
  const { t } = useTranslation();
  const { credential } = view;
  const used = relativeAge(credential.last_used_at, now);
  const proxies = credential.proxy_pool_ids?.length ?? 0;
  return (
    <article
      className={itemClasses(view, "pv-account")}
      role="button"
      tabIndex={0}
      aria-label={t("providers.account.editNamed", { label: view.label })}
      onClick={() => handlers.edit(credential)}
      onKeyDown={(event) => openOnKey(event, () => handlers.edit(credential))}
      onDragOver={(event) => handlers.dragOver(credential, event)}
      onDrop={(event) => handlers.drop(credential, event)}
    >
      <header className="cp-card-head">
        <SelectBox view={view} handlers={handlers} />
        <RankHandle {...props} />
        <div className="cp-card-title">
          <strong title={view.label}>{view.label}</strong>
          <div className="cp-card-tags">
            <StatePill view={view} now={now} />
            {view.quota?.plan ? <Tag title={t("providers.account.plan", { plan: view.quota.plan })}>{view.quota.plan}</Tag> : null}
            <Tag tone="neutral">{authLabel(credential.auth_type, t)}</Tag>
            {credential.last_error_code ? <Tag tone="danger" title={credential.last_error || credential.last_error_code}>{credential.last_error_code}</Tag> : null}
          </div>
        </div>
        <EnabledToggle view={view} handlers={handlers} />
      </header>
      <div className="cp-card-body">
        {view.secondary || view.number ? (
          <p className="pv-account-sub" title={t("providers.account.addedOn", { date: view.addedAt })}>
            {view.number ? <span>#{view.number}</span> : null}
            {view.secondary ? <span>{view.secondary}</span> : <span>{t("providers.added", { time: view.addedAt })}</span>}
          </p>
        ) : null}
        <QuotaList view={view} />
        {credential.last_error ? (
          <p className="pv-note pv-note-error" title={credential.last_error}>
            <span className="material-symbols-outlined" aria-hidden>error</span>
            <span>{credential.last_error}</span>
          </p>
        ) : null}
      </div>
      <footer className="cp-card-foot">
        <div className="cp-card-meta">
          {view.usageLabel ? (
            <span title={t("providers.usageThroughTproxy")}>
              <span className="material-symbols-outlined" aria-hidden>swap_vert</span>
              {view.usageLabel}
            </span>
          ) : null}
          {used ? (
            <span title={new Date(credential.last_used_at!).toLocaleString()}>
              <span className="material-symbols-outlined" aria-hidden>history</span>
              {t("providers.account.usedAgo", { time: used })}
            </span>
          ) : null}
          {(credential.consecutive_use_count ?? 0) > 0 ? (
            <span title={t("providers.account.stickyHint")}>
              <span className="material-symbols-outlined" aria-hidden>push_pin</span>
              {credential.consecutive_use_count}
            </span>
          ) : null}
          {proxies > 0 ? (
            <span title={credential.proxy_pool_ids?.join(", ")}>
              <span className="material-symbols-outlined" aria-hidden>lan</span>
              {proxies}
            </span>
          ) : null}
        </div>
        <QuickActions view={view} handlers={handlers} />
      </footer>
    </article>
  );
}

export function AccountRow(props: ItemProps) {
  const { view, handlers, now } = props;
  const { t } = useTranslation();
  const { credential } = view;
  const details = [view.number ? `#${view.number}` : null, view.secondary, view.quota?.plan, authLabel(credential.auth_type, t)].filter(Boolean).join(" · ");
  return (
    <div
      className={itemClasses(view, "pv-account-row")}
      role="button"
      tabIndex={0}
      aria-label={t("providers.account.editNamed", { label: view.label })}
      onClick={() => handlers.edit(credential)}
      onKeyDown={(event) => openOnKey(event, () => handlers.edit(credential))}
      onDragOver={(event) => handlers.dragOver(credential, event)}
      onDrop={(event) => handlers.drop(credential, event)}
    >
      <span className="cp-cell"><SelectBox view={view} handlers={handlers} /></span>
      <span className="cp-cell"><RankHandle {...props} /></span>
      <span className="cp-cell cp-cell-main">
        <strong title={view.label}>{view.label}</strong>
        <small title={credential.last_error || details}>{credential.last_error || details}</small>
      </span>
      <span className="cp-cell"><StatePill view={view} now={now} /></span>
      <span className="cp-cell pv-account-row-quota">{view.supportsQuota ? <QuotaList view={view} inline /> : <span className="cp-cell-mono">—</span>}</span>
      <span className="cp-cell cp-cell-mono">{view.usageLabel ?? "—"}</span>
      <span className="cp-cell"><EnabledToggle view={view} handlers={handlers} /></span>
      <span className="cp-cell cp-cell-actions"><QuickActions view={view} handlers={handlers} /></span>
    </div>
  );
}
