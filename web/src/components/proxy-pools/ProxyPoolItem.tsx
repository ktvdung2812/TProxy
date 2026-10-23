import type { KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { Toggle, cn } from "../ui";
import { ActionMenu, IconAction, StatusPill, Tag, type ActionMenuItem } from "../page-kit";
import type { ProxyPoolRow, ProxyPoolTestResult } from "./api";
import { POOL_HEALTH_TONE, type PoolHealth } from "./utils";

export type ProxyPoolView = {
  pool: ProxyPoolRow;
  name: string;
  health: PoolHealth;
  scheme: string;
  host: string;
  /** Providers and accounts that route through this pool, when the snapshot knows them. */
  usedBy: string[];
  lastTested: string | null;
  lastResult?: ProxyPoolTestResult;
  testing: boolean;
  busy: boolean;
  selected: boolean;
};

export type ProxyPoolHandlers = {
  edit: (pool: ProxyPoolRow) => void;
  select: (pool: ProxyPoolRow, selected: boolean) => void;
  setEnabled: (pool: ProxyPoolRow) => void;
  test: (pool: ProxyPoolRow) => void;
  copyId: (pool: ProxyPoolRow) => void;
  remove: (pool: ProxyPoolRow) => void;
};

type ItemProps = { view: ProxyPoolView; handlers: ProxyPoolHandlers };

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

function HealthPill({ view }: { view: ProxyPoolView }) {
  const { t } = useTranslation();
  return (
    <StatusPill tone={POOL_HEALTH_TONE[view.health]} loading={view.testing} title={view.pool.last_error || t(`proxyPools.healthHint.${view.health}`)}>
      {view.testing ? t("proxyPools.testing") : t(`proxyPools.health.${view.health}`)}
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
      onChange={(event) => handlers.select(view.pool, event.target.checked)}
      aria-label={t("proxyPools.selectPool", { name: view.name })}
    />
  );
}

function EnabledToggle({ view, handlers }: ItemProps) {
  const { t } = useTranslation();
  const label = view.pool.enabled ? t("proxyPools.turnOff") : t("proxyPools.turnOn");
  return (
    <span className="pp-toggle" onClick={stop} title={label}>
      <Toggle checked={view.pool.enabled} disabled={view.busy} onChange={() => handlers.setEnabled(view.pool)} aria-label={label} />
    </span>
  );
}

function usedByText(view: ProxyPoolView, t: TFunction) {
  const count = view.pool.usage_count || 0;
  if (count === 0) return t("proxyPools.notBound");
  if (view.usedBy.length === 0) return t("proxyPools.boundCount", { count });
  const extra = Math.max(0, count - 1);
  return extra > 0 ? t("proxyPools.usedByMore", { name: view.usedBy[0], count: extra }) : view.usedBy[0];
}

function lastTestText(view: ProxyPoolView, t: TFunction) {
  if (!view.lastTested) return t("proxyPools.neverTested");
  const latency = view.lastResult?.elapsed_ms;
  return latency !== undefined && view.lastResult?.ok
    ? t("proxyPools.testedAgoLatency", { time: view.lastTested, ms: latency })
    : t("proxyPools.testedAgo", { time: view.lastTested });
}

function menuItems(view: ProxyPoolView, handlers: ProxyPoolHandlers, t: TFunction): ActionMenuItem[] {
  const bound = (view.pool.usage_count || 0) > 0;
  return [
    { key: "edit", icon: "edit", label: t("proxyPools.edit"), hint: t("proxyPools.editHint"), disabled: view.busy, onSelect: () => handlers.edit(view.pool) },
    { key: "copy", icon: "content_copy", label: t("proxyPools.copyId"), hint: view.pool.id, onSelect: () => handlers.copyId(view.pool) },
    {
      key: "delete",
      icon: "delete",
      label: t("proxyPools.deletePool"),
      // The server refuses to delete a bound pool, so say why up front.
      hint: bound ? t("proxyPools.deleteBoundHint", { count: view.pool.usage_count }) : undefined,
      danger: true,
      disabled: view.busy || bound,
      onSelect: () => handlers.remove(view.pool),
    },
  ];
}

function QuickActions({ view, handlers }: ItemProps) {
  const { t } = useTranslation();
  return (
    <div className="pp-actions" onClick={stop}>
      <IconAction
        icon="network_check"
        label={t("proxyPools.testProxy")}
        spinning={view.testing}
        disabled={view.busy || view.testing}
        onClick={() => handlers.test(view.pool)}
      />
      <ActionMenu label={t("proxyPools.moreActions")} items={menuItems(view, handlers, t)} />
    </div>
  );
}

function ErrorNote({ view }: { view: ProxyPoolView }) {
  const error = view.pool.last_error?.trim();
  if (!error || view.health !== "failing") return null;
  return (
    <div className="pp-note">
      <span className="material-symbols-outlined" aria-hidden>error</span>
      <span title={error}>{error}</span>
    </div>
  );
}

function itemClass(view: ProxyPoolView, base: string) {
  return cn(base, base === "pp-card" ? "cp-card" : "cp-list-row", `pp-health-${view.health}`, !view.pool.enabled && "is-off", view.selected && "is-selected");
}

export function ProxyPoolCard({ view, handlers }: ItemProps) {
  const { t } = useTranslation();
  return (
    <article
      className={itemClass(view, "pp-card")}
      role="button"
      tabIndex={0}
      aria-label={t("proxyPools.editNamed", { name: view.name })}
      onClick={() => handlers.edit(view.pool)}
      onKeyDown={(event) => openOnKey(event, () => handlers.edit(view.pool))}
    >
      <header className="cp-card-head">
        <SelectBox view={view} handlers={handlers} />
        <div className="cp-card-title">
          <strong title={view.name}>{view.name}</strong>
          <div className="cp-card-tags">
            <HealthPill view={view} />
            {view.scheme ? <Tag tone="neutral">{view.scheme.toUpperCase()}</Tag> : null}
          </div>
        </div>
        <EnabledToggle view={view} handlers={handlers} />
      </header>
      <div className="cp-card-body">
        <code className="pp-endpoint" title={view.pool.url}>{view.host}</code>
        <dl className="cp-facts">
          <div>
            <dt>{t("proxyPools.columns.usedBy")}</dt>
            <dd title={view.usedBy.join(", ") || undefined} className={cn((view.pool.usage_count || 0) === 0 && "pp-dim")}>{usedByText(view, t)}</dd>
          </div>
          <div>
            <dt>{t("proxyPools.columns.lastTest")}</dt>
            <dd className={cn(!view.lastTested && "pp-dim")}>{lastTestText(view, t)}</dd>
          </div>
        </dl>
        <ErrorNote view={view} />
      </div>
      <footer className="cp-card-foot">
        <div className="cp-card-meta">
          <span title={t("proxyPools.poolId")}>
            <span className="material-symbols-outlined" aria-hidden>tag</span>
            {view.pool.id}
          </span>
        </div>
        <QuickActions view={view} handlers={handlers} />
      </footer>
    </article>
  );
}

export function ProxyPoolListRow({ view, handlers }: ItemProps) {
  const { t } = useTranslation();
  return (
    <div
      className={itemClass(view, "pp-row")}
      role="button"
      tabIndex={0}
      aria-label={t("proxyPools.editNamed", { name: view.name })}
      onClick={() => handlers.edit(view.pool)}
      onKeyDown={(event) => openOnKey(event, () => handlers.edit(view.pool))}
    >
      <span className="cp-cell pp-cell-check"><SelectBox view={view} handlers={handlers} /></span>
      <span className="cp-cell cp-cell-main pp-cell-name">
        <strong title={view.name}>{view.name}</strong>
        <small>{view.pool.id}</small>
      </span>
      <span className="cp-cell pp-cell-status">
        <HealthPill view={view} />
        {view.health === "failing" && view.pool.last_error ? <small className="pp-row-error" title={view.pool.last_error}>{view.pool.last_error}</small> : null}
      </span>
      <span className="cp-cell cp-cell-mono pp-cell-endpoint" title={view.pool.url}>
        {view.scheme ? <span className="pp-scheme">{view.scheme}</span> : null}
        {view.host}
      </span>
      <span className="cp-cell cp-cell-mono pp-cell-used" data-label={t("proxyPools.columns.usedBy")} title={view.usedBy.join(", ") || undefined}>
        {usedByText(view, t)}
      </span>
      <span className="cp-cell cp-cell-mono pp-cell-tested" data-label={t("proxyPools.columns.lastTest")}>{lastTestText(view, t)}</span>
      <span className="cp-cell cp-keep-bright pp-cell-toggle"><EnabledToggle view={view} handlers={handlers} /></span>
      <span className="cp-cell cp-keep-bright pp-cell-actions"><QuickActions view={view} handlers={handlers} /></span>
    </div>
  );
}
