import type { DragEvent, KeyboardEvent, ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { Toggle, cn } from "../ui";
import { ActionMenu, IconAction, StatusPill, Tag, type ActionMenuItem, type Tone } from "../page-kit";
import { QuotaMeters, type QuotaMeterRow } from "./QuotaMeters";
import { QuotaStackedBar } from "./QuotaStackedBar";
import type { CredentialQuota } from "./api";
import { formatQuotaName, formatResetTime, type AccountHealth } from "./utils";

export type QuotaCredential = {
  id: string;
  providerId: string;
  providerType: string;
  label?: string;
  email?: string;
  enabled: boolean;
  auth_type: string;
  priority?: number;
  weight?: number;
  proxy_pool_ids?: string[];
  created_at?: string;
  cooldown_until?: string;
};

export type QuotaAccountView = {
  credential: QuotaCredential;
  quotaKey: string;
  label: string;
  secondary: string | null;
  plan?: string;
  health: AccountHealth;
  quota?: CredentialQuota;
  error?: string;
  loading: boolean;
  busy: boolean;
  live: boolean;
  /** 1-based position in the provider's routing order; null when not shown. */
  rank: number | null;
  canReorder: boolean;
  /** Visible windows; the Grok product breakdown is kept for the stacked bar only. */
  rows: QuotaMeterRow[];
  meterRows: QuotaMeterRow[];
  hiddenRows: QuotaMeterRow[];
  stacked: boolean;
  usageLabel: string | null;
  renewalDate: string | null;
  isCodex: boolean;
  resetCredits: number;
  resettingCredit: boolean;
  refreshing: boolean;
  deleting: boolean;
  selected: boolean;
  dragging: boolean;
  dragOver: boolean;
};

export type QuotaAccountHandlers = {
  open: (credential: QuotaCredential) => void;
  select: (credential: QuotaCredential, selected: boolean) => void;
  setEnabled: (credential: QuotaCredential, enabled: boolean) => void;
  refresh: (credential: QuotaCredential) => void;
  consumeResetCredit: (credential: QuotaCredential) => void;
  viewResetCredits: (credential: QuotaCredential) => void;
  edit: (credential: QuotaCredential) => void;
  remove: (credential: QuotaCredential) => void;
  hideRow: (quotaKey: string, row: QuotaMeterRow) => void;
  showRow: (quotaKey: string, row: QuotaMeterRow) => void;
  dragStart: (credential: QuotaCredential, event: DragEvent<HTMLElement>) => void;
  dragEnd: () => void;
  dragOver: (credential: QuotaCredential, event: DragEvent<HTMLElement>) => void;
  drop: (credential: QuotaCredential, event: DragEvent<HTMLElement>) => void;
};

type ItemProps = {
  view: QuotaAccountView;
  handlers: QuotaAccountHandlers;
  /** Routing controls stay locked until the first quota pass has settled. */
  toggleLocked: boolean;
  reorderLocked: boolean;
};

const HEALTH_TONE: Record<AccountHealth, Tone> = {
  ready: "success",
  nodata: "neutral",
  low: "warning",
  exhausted: "danger",
  cooldown: "warning",
  paused: "danger",
  error: "danger",
  off: "neutral",
  loading: "neutral",
};

function healthLabel(view: QuotaAccountView, t: TFunction) {
  if (view.health === "cooldown") {
    const remaining = formatResetTime(view.credential.cooldown_until);
    return remaining !== "-" ? t("quota.health.cooldownFor", { time: remaining }) : t("quota.health.cooldown");
  }
  return t(`quota.health.${view.health}`);
}

function HealthPill({ view }: { view: QuotaAccountView }) {
  const { t } = useTranslation();
  return (
    <StatusPill tone={HEALTH_TONE[view.health]} loading={view.health === "loading"} title={t(`quota.healthHint.${view.health}`)}>
      {healthLabel(view, t)}
    </StatusPill>
  );
}

function stop(event: { stopPropagation: () => void }) {
  event.stopPropagation();
}

/** Only react to keys pressed on the item itself, never on a nested control. */
function openOnKey(event: KeyboardEvent<HTMLElement>, open: () => void) {
  if (event.target !== event.currentTarget) return;
  if (event.key === "Enter" || event.key === " ") {
    event.preventDefault();
    open();
  }
}

function RankHandle({ view, handlers, reorderLocked }: ItemProps) {
  const { t } = useTranslation();
  if (view.rank === null) return null;
  if (!view.canReorder) {
    return <span className="qt-rank" title={t("quota.rankHint", { rank: view.rank })}>{view.rank}</span>;
  }
  return (
    <button
      type="button"
      className="qt-rank qt-rank-handle"
      draggable={!reorderLocked}
      disabled={reorderLocked}
      onClick={stop}
      onDragStart={(event) => handlers.dragStart(view.credential, event)}
      onDragEnd={handlers.dragEnd}
      aria-label={t("quota.dragToReorder", { label: view.label })}
      title={t("quota.rankDragHint", { rank: view.rank })}
    >
      <span className="material-symbols-outlined" aria-hidden>drag_indicator</span>
      <span>{view.rank}</span>
    </button>
  );
}

function EnabledToggle({ view, handlers, toggleLocked }: ItemProps) {
  const { t } = useTranslation();
  return (
    <span
      className="qt-toggle"
      onClick={stop}
      title={toggleLocked ? t("quota.toggleLocked") : view.credential.enabled ? t("quota.disableAccount") : t("quota.enableAccount")}
    >
      <Toggle
        checked={view.credential.enabled}
        disabled={view.busy || toggleLocked}
        onChange={(event) => handlers.setEnabled(view.credential, event.target.checked)}
        aria-label={view.credential.enabled ? t("quota.disableAccount") : t("quota.enableAccount")}
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
      aria-label={t("quota.selectAccount", { label: view.label })}
    />
  );
}

function menuItems(view: QuotaAccountView, handlers: QuotaAccountHandlers, t: TFunction): ActionMenuItem[] {
  const { credential } = view;
  const items: Array<ActionMenuItem | null> = [
    { key: "details", icon: "monitoring", label: t("quota.menu.details"), hint: t("quota.menu.detailsHint"), onSelect: () => handlers.open(credential) },
    {
      key: "refresh",
      icon: view.health === "cooldown" ? "restart_alt" : "refresh",
      label: view.health === "cooldown" ? t("quota.clearCooldownRecheck") : t("quota.refreshQuota"),
      disabled: view.busy || view.refreshing,
      onSelect: () => handlers.refresh(credential),
    },
    view.isCodex
      ? {
          key: "use-credit",
          icon: "bolt",
          label: t("quota.menu.useResetCredit"),
          hint: view.resetCredits > 0 ? t("quota.menu.creditsAvailable", { count: view.resetCredits }) : t("quota.noResetCredits"),
          disabled: view.resetCredits <= 0 || view.busy,
          onSelect: () => handlers.consumeResetCredit(credential),
        }
      : null,
    view.isCodex
      ? { key: "credits", icon: "schedule", label: t("quota.viewResetExpiry"), onSelect: () => handlers.viewResetCredits(credential) }
      : null,
    { key: "edit", icon: "tune", label: t("quota.menu.editProvider"), hint: t("quota.menu.editProviderHint"), disabled: view.busy, onSelect: () => handlers.edit(credential) },
    { key: "delete", icon: "delete", label: t("quota.deleteConnection"), danger: true, disabled: view.busy, onSelect: () => handlers.remove(credential) },
  ];
  return items.filter(Boolean) as ActionMenuItem[];
}

function QuickActions({ view, handlers }: Pick<ItemProps, "view" | "handlers">) {
  const { t } = useTranslation();
  const refreshLabel = view.health === "cooldown" ? t("quota.clearCooldownRecheck") : t("quota.refreshQuota");
  return (
    <div className="qt-actions" onClick={stop}>
      {view.isCodex && view.resetCredits > 0 ? (
        <button
          type="button"
          className="qt-credit-btn"
          disabled={view.busy || view.loading}
          onClick={() => handlers.consumeResetCredit(view.credential)}
          title={t("quota.useResetCredit", { count: view.resetCredits })}
          aria-label={t("quota.useResetCredit", { count: view.resetCredits })}
        >
          <span className={cn("material-symbols-outlined", view.resettingCredit && "animate-spin")} aria-hidden>
            {view.resettingCredit ? "progress_activity" : "bolt"}
          </span>
          {view.resetCredits}
        </button>
      ) : null}
      <IconAction
        icon={view.health === "cooldown" ? "restart_alt" : "refresh"}
        label={refreshLabel}
        spinning={view.refreshing}
        disabled={view.busy || view.refreshing}
        onClick={() => handlers.refresh(view.credential)}
      />
      <ActionMenu label={t("quota.menu.more")} items={menuItems(view, handlers, t)} />
    </div>
  );
}

function QuotaBody({ view, handlers, inline }: Pick<ItemProps, "view" | "handlers"> & { inline?: boolean }) {
  const { t } = useTranslation();
  const hide = (row: QuotaMeterRow) => handlers.hideRow(view.quotaKey, row);
  let content: ReactNode;
  if (view.loading && !view.quota) {
    content = (
      <div className="cp-skeleton" aria-label={t("quota.checkingQuota")}>
        <span />
        <span />
      </div>
    );
  } else if (view.error) {
    content = (
      <div className="qt-body-note qt-body-note-error">
        <span className="material-symbols-outlined" aria-hidden>error</span>
        <span className="qt-body-note-text" title={view.error}>{view.error}</span>
        {!inline ? (
          <button type="button" className="cp-link-btn" onClick={(event) => { event.stopPropagation(); handlers.refresh(view.credential); }}>
            {t("quota.retry")}
          </button>
        ) : null}
      </div>
    );
  } else if (view.quota && view.rows.length === 0 && view.hiddenRows.length === 0) {
    content = (
      <div className="qt-body-note">
        <span className="material-symbols-outlined" aria-hidden>data_usage</span>
        <span className="qt-body-note-text">{view.quota.message || t("quota.noQuotaData")}</span>
      </div>
    );
  } else if (view.rows.length === 0 && view.hiddenRows.length > 0) {
    content = (
      <div className="qt-body-note">
        <span className="material-symbols-outlined" aria-hidden>visibility_off</span>
        <span className="qt-body-note-text">{t("quota.allQuotaRowsHidden")}</span>
      </div>
    );
  } else if (view.stacked && !inline) {
    content = <QuotaStackedBar rows={view.rows} onHide={hide} />;
  } else {
    content = (
      <QuotaMeters
        rows={view.meterRows}
        inline={inline}
        limit={inline ? 3 : 4}
        liveKey={view.live ? "session" : undefined}
        onHide={hide}
      />
    );
  }

  return (
    <>
      {content}
      {!inline && view.hiddenRows.length > 0 ? (
        <div className="qt-hidden-rows" onClick={stop}>
          <span className="material-symbols-outlined" aria-hidden>visibility_off</span>
          <span>{t("quota.hiddenCount", { count: view.hiddenRows.length })}</span>
          {view.hiddenRows.map((row) => (
            <button
              key={row.key}
              type="button"
              className="qt-hidden-chip"
              onClick={() => handlers.showRow(view.quotaKey, row)}
              title={t("quota.showQuotaRow")}
            >
              {formatQuotaName(row.name)}
              <span className="material-symbols-outlined" aria-hidden>visibility</span>
            </button>
          ))}
        </div>
      ) : null}
    </>
  );
}

function itemClasses(view: QuotaAccountView, base: string) {
  return cn(
    base,
    base === "qt-card" ? "cp-card" : "cp-list-row",
    `qt-health-${view.health}`,
    !view.credential.enabled && "is-off",
    view.selected && "is-selected",
    view.dragging && "is-dragging",
    view.dragOver && "is-drag-over",
    view.deleting && "is-deleting",
  );
}

export function QuotaAccountCard(props: ItemProps) {
  const { view, handlers } = props;
  const { t } = useTranslation();
  return (
    <article
      className={itemClasses(view, "qt-card")}
      role="button"
      tabIndex={0}
      aria-label={t("quota.viewDetails", { label: view.label })}
      onClick={() => handlers.open(view.credential)}
      onKeyDown={(event) => openOnKey(event, () => handlers.open(view.credential))}
      onDragOver={(event) => handlers.dragOver(view.credential, event)}
      onDrop={(event) => handlers.drop(view.credential, event)}
    >
      <header className="cp-card-head">
        <SelectBox view={view} handlers={handlers} />
        <RankHandle {...props} />
        <div className="cp-card-title">
          <strong title={view.label}>{view.label}</strong>
          <div className="cp-card-tags">
            <HealthPill view={view} />
            {view.plan ? <Tag title={t("quota.servicePlan", { plan: view.plan })}>{view.plan}</Tag> : null}
            {view.live ? <span className="qt-live">{t("quota.live")}</span> : null}
            {view.secondary ? <span className="qt-secondary" title={view.secondary}>{view.secondary}</span> : null}
          </div>
        </div>
        <EnabledToggle {...props} />
      </header>

      <div className="cp-card-body">
        <QuotaBody view={view} handlers={handlers} />
      </div>

      <footer className="cp-card-foot">
        <div className="cp-card-meta">
          {view.usageLabel ? (
            <span title={t("quota.proxyUsageAllTime")}>
              <span className="material-symbols-outlined" aria-hidden>swap_vert</span>
              {view.usageLabel}
            </span>
          ) : null}
          {view.renewalDate ? (
            <span title={t("quota.renewsOn", { date: view.renewalDate })}>
              <span className="material-symbols-outlined" aria-hidden>event_repeat</span>
              {view.renewalDate}
            </span>
          ) : null}
        </div>
        <QuickActions view={view} handlers={handlers} />
      </footer>
    </article>
  );
}

export function QuotaAccountRow(props: ItemProps) {
  const { view, handlers } = props;
  const { t } = useTranslation();
  const details = [view.plan, view.secondary].filter(Boolean).join(" · ");
  return (
    <div
      className={itemClasses(view, "qt-row")}
      role="button"
      tabIndex={0}
      aria-label={t("quota.viewDetails", { label: view.label })}
      onClick={() => handlers.open(view.credential)}
      onKeyDown={(event) => openOnKey(event, () => handlers.open(view.credential))}
      onDragOver={(event) => handlers.dragOver(view.credential, event)}
      onDrop={(event) => handlers.drop(view.credential, event)}
    >
      <span className="cp-cell"><SelectBox view={view} handlers={handlers} /></span>
      <span className="cp-cell"><RankHandle {...props} /></span>
      <span className="cp-cell cp-cell-main">
        <strong title={view.label}>{view.label}</strong>
        {details ? <small title={details}>{details}</small> : null}
      </span>
      <span className="cp-cell qt-row-status">
        <HealthPill view={view} />
        {view.live ? <span className="qt-live">{t("quota.live")}</span> : null}
      </span>
      <span className="cp-cell qt-row-quota">
        <QuotaBody view={view} handlers={handlers} inline />
      </span>
      <span className="cp-cell cp-cell-mono">{view.usageLabel ?? "—"}</span>
      <span className="cp-cell"><EnabledToggle {...props} /></span>
      <span className="cp-cell"><QuickActions view={view} handlers={handlers} /></span>
    </div>
  );
}
