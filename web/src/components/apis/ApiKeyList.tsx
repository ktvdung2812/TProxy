import type { KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { maskApiKeySecret } from "../../lib/apiKeySecrets";
import { Toggle, cn } from "../ui";
import { ActionMenu, IconAction, StatusPill, Tag, type ActionMenuItem, type Tone } from "../page-kit";
import type { ApiKeyRecord, ApiKeyUsage } from "./types";
import { hasOwnLimits, type ApiKeyHealth } from "./utils";

export type ApiKeyRowView = {
  key: ApiKeyRecord;
  usage?: ApiKeyUsage;
  health: ApiKeyHealth;
  /** Secret saved in this browser, if any. */
  secret?: string;
  selected: boolean;
  busy: boolean;
};

export type ApiKeyRowHandlers = {
  open: (key: ApiKeyRecord) => void;
  select: (key: ApiKeyRecord, selected: boolean) => void;
  setEnabled: (key: ApiKeyRecord, enabled: boolean) => void;
  copy: (text: string, id: string) => void;
  showCapacity: (key: ApiKeyRecord) => void;
  useInExample: (key: ApiKeyRecord) => void;
  revoke: (key: ApiKeyRecord) => void;
};

export const HEALTH_TONE: Record<ApiKeyHealth, Tone> = {
  active: "success",
  nearBudget: "warning",
  paused: "neutral",
  noModels: "danger",
  overBudget: "danger",
  noAccounts: "danger",
};

function usd(value: number, language: string) {
  return new Intl.NumberFormat(language, { style: "currency", currency: "USD", maximumFractionDigits: value >= 100 ? 0 : 2 }).format(value || 0);
}

function compact(value: number, language: string) {
  return new Intl.NumberFormat(language, { notation: "compact", maximumFractionDigits: 1 }).format(value || 0);
}

/** Translated "60 req/min · $20/day"; keys without their own limits inherit account capacity. */
export function limitSummary(key: ApiKeyRecord, t: TFunction): string {
  const limits = key.policy?.limits;
  if (!limits || !hasOwnLimits(key)) return t("apis.inheritedLimits");
  const parts: string[] = [];
  if (limits.requests_per_minute) parts.push(t("apis.limitShort.rpm", { value: limits.requests_per_minute }));
  if (limits.concurrent_streams) parts.push(t("apis.limitShort.streams", { value: limits.concurrent_streams }));
  if (limits.max_input_bytes) parts.push(t("apis.limitShort.inputBytes", { value: limits.max_input_bytes }));
  if (limits.max_output_tokens) parts.push(t("apis.limitShort.outputTokens", { value: limits.max_output_tokens }));
  if (limits.media_jobs) parts.push(t("apis.limitShort.mediaJobs", { value: limits.media_jobs }));
  if (limits.budget_usd_per_day) parts.push(t("apis.limitShort.budget", { value: limits.budget_usd_per_day }));
  return parts.join(" · ");
}

export function modelAccessLabel(key: ApiKeyRecord, t: TFunction): string {
  if (key.models?.includes("*")) return t("apis.allModels");
  if (key.models?.length) return t("apis.selectedModelsShort", { count: key.models.length });
  return t("apis.noModelAccess");
}

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

function menuItems(view: ApiKeyRowView, handlers: ApiKeyRowHandlers, t: TFunction): ActionMenuItem[] {
  const { key } = view;
  const items: Array<ActionMenuItem | null> = [
    { key: "edit", icon: "tune", label: t("apis.menu.edit"), hint: t("apis.menu.editHint"), onSelect: () => handlers.open(key) },
    view.usage?.effective_limits
      ? { key: "capacity", icon: "speed", label: t("apis.menu.capacity"), hint: t("apis.sharedCapacity", { count: view.usage.eligible_account_count ?? 0 }), onSelect: () => handlers.showCapacity(key) }
      : null,
    {
      key: "example",
      icon: "terminal",
      label: t("apis.menu.example"),
      hint: t("apis.menu.exampleHint"),
      disabled: !key.enabled,
      onSelect: () => handlers.useInExample(key),
    },
    { key: "copy-id", icon: "fingerprint", label: t("apis.menu.copyId"), hint: key.id, onSelect: () => handlers.copy(key.id, `id_${key.id}`) },
    { key: "revoke", icon: "delete", label: t("apis.revokeKey"), danger: true, disabled: view.busy, onSelect: () => handlers.revoke(key) },
  ];
  return items.filter(Boolean) as ActionMenuItem[];
}

function ApiKeyRow({ view, handlers }: { view: ApiKeyRowView; handlers: ApiKeyRowHandlers }) {
  const { t, i18n } = useTranslation();
  const { key, usage, health } = view;
  const name = key.name || key.id;
  const budget = usage?.budget_usd_per_day || key.policy?.limits?.budget_usd_per_day || 0;
  const spent = usage?.cost_usd_today || 0;
  const share = budget > 0 ? Math.min(1, spent / budget) : 0;
  const access = [
    modelAccessLabel(key, t),
    key.policy?.credential_ids?.length ? t("apis.accountsShort", { count: key.policy.credential_ids.length }) : null,
    key.policy?.endpoints?.length ? t("apis.endpointsShort", { count: key.policy.endpoints.length }) : null,
  ].filter(Boolean).join(" · ");
  const copyId = `secret_${key.id}`;

  return (
    <div
      className={cn("cp-list-row ap-key-row", `ap-health-${health}`, !key.enabled && "is-off", view.selected && "is-selected")}
      style={{ ["--cp-row-tone" as string]: `var(--color-${HEALTH_TONE[health] === "neutral" ? "border" : HEALTH_TONE[health]})` }}
      role="button"
      tabIndex={0}
      aria-label={t("apis.openKey", { name })}
      onClick={() => handlers.open(key)}
      onKeyDown={(event) => openOnKey(event, () => handlers.open(key))}
    >
      <span className="cp-cell ap-key-check cp-keep-bright">
        <input
          type="checkbox"
          className="cp-check"
          checked={view.selected}
          onClick={stop}
          onChange={(event) => handlers.select(key, event.target.checked)}
          aria-label={t("apis.selectKey", { name })}
        />
      </span>
      <span className="cp-cell cp-cell-main ap-key-name">
        <strong title={name}>{name}</strong>
        <small title={key.id}>
          {key.id}
          {key.policy?.team ? ` · ${t("apis.teamShort", { team: key.policy.team })}` : ""}
        </small>
      </span>
      <span className="cp-cell ap-key-status cp-keep-bright">
        <StatusPill tone={HEALTH_TONE[health]} title={t(`apis.healthHint.${health}`)}>{t(`apis.health.${health}`)}</StatusPill>
        {hasOwnLimits(key) ? <Tag tone="info" title={t("apis.ownLimitsHint")}>{t("apis.ownLimitsTag")}</Tag> : null}
      </span>
      <span className="cp-cell ap-key-secret" onClick={stop}>
        {view.secret ? (
          <>
            <code title={t("apis.secretSavedHint")}>{maskApiKeySecret(view.secret)}</code>
            <IconAction
              icon={/* quick action 1 */ "content_copy"}
              label={t("apis.copySecret")}
              className={cn("ap-copy", view.secret && "is-ready")}
              onClick={() => handlers.copy(view.secret || "", copyId)}
            />
          </>
        ) : (
          <button type="button" className="ap-secret-missing" onClick={() => handlers.open(key)} title={t("apis.secretNotSavedHint")}>
            <span className="material-symbols-outlined" aria-hidden>key_off</span>
            {t("apis.secretNotSavedShort")}
          </button>
        )}
      </span>
      <span className="cp-cell cp-cell-main ap-key-access">
        <span className={cn("ap-key-models", health === "noModels" && "is-danger", key.models?.includes("*") && "is-all")} title={access}>{access}</span>
        <small title={limitSummary(key, t)}>{limitSummary(key, t)}</small>
      </span>
      <span className="cp-cell ap-key-usage">
        {usage ? (
          <>
            <span className="ap-key-usage-line">
              <strong>{compact(usage.requests_today, i18n.language)}</strong> {t("apis.requestsShort")}
              <span aria-hidden> · </span>
              {usd(spent, i18n.language)}
            </span>
            {budget > 0 ? (
              <span className="ap-budget" title={t("apis.budgetUsed", { spent: usd(spent, i18n.language), budget: usd(budget, i18n.language) })}>
                <span className="cp-meter-track" style={{ ["--cp-tone" as string]: `var(--color-${share >= 1 ? "danger" : share >= 0.8 ? "warning" : "primary"})` }}>
                  <span className="cp-meter-fill" style={{ width: `${share * 100}%`, display: "block" }} />
                </span>
                <small>{t("apis.ofBudget", { budget: usd(budget, i18n.language) })}</small>
              </span>
            ) : null}
          </>
        ) : (
          <span className="cp-skeleton ap-key-usage-skeleton" aria-label={t("apis.loadingUsage")}><span /></span>
        )}
      </span>
      <span
        className="cp-cell ap-key-toggle cp-keep-bright"
        onClick={stop}
        title={key.enabled ? t("apis.pauseKey") : t("apis.resumeKey")}
      >
        <Toggle
          checked={key.enabled}
          disabled={view.busy}
          onChange={(event) => handlers.setEnabled(key, event.target.checked)}
          aria-label={key.enabled ? t("apis.pauseKey") : t("apis.resumeKey")}
        />
      </span>
      <span className="cp-cell cp-cell-actions cp-keep-bright" onClick={stop}>
        <ActionMenu label={t("apis.menu.more", { name })} items={menuItems(view, handlers, t)} />
      </span>
    </div>
  );
}

type ListProps = {
  rows: ApiKeyRowView[];
  handlers: ApiKeyRowHandlers;
  allSelected: boolean;
  someSelected: boolean;
  onSelectAll: (selected: boolean) => void;
};

export function ApiKeyList({ rows, handlers, allSelected, someSelected, onSelectAll }: ListProps) {
  const { t } = useTranslation();
  return (
    <div className="cp-list ap-key-list">
      <div className="cp-list-head">
        <span>
          <input
            type="checkbox"
            className="cp-check"
            checked={allSelected}
            ref={(element) => {
              if (element) element.indeterminate = someSelected && !allSelected;
            }}
            onChange={(event) => onSelectAll(event.target.checked)}
            aria-label={t("apis.selectAllVisible")}
          />
        </span>
        <span>{t("apis.columns.key")}</span>
        <span>{t("apis.columns.status")}</span>
        <span>{t("apis.columns.secret")}</span>
        <span>{t("apis.columns.access")}</span>
        <span>{t("apis.columns.today")}</span>
        <span>{t("apis.columns.on")}</span>
        <span />
      </div>
      {rows.map((view) => (
        <ApiKeyRow key={view.key.id} view={view} handlers={handlers} />
      ))}
    </div>
  );
}
