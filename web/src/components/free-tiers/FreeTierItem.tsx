import type { KeyboardEvent, MouseEvent } from "react";
import { useTranslation } from "react-i18next";
import { StatusPill, Tag, ToolButton } from "../page-kit";
import { ProviderLogo } from "../providers/ProviderLogo";
import { cn } from "../ui";
import type { FreeTierItem } from "./api";

export const AUTH_ICONS: Record<string, string> = {
  api_key: "key",
  oauth: "login",
  none: "key_off",
  service_account: "badge",
};

const MAX_MODELS = 3;

type ItemProps = { item: FreeTierItem; onOpen: () => void };

function stop(event: MouseEvent) {
  event.stopPropagation();
}

function onKey(event: KeyboardEvent<HTMLElement>, onOpen: () => void) {
  if (event.target !== event.currentTarget) return;
  if (event.key === "Enter" || event.key === " ") {
    event.preventDefault();
    onOpen();
  }
}

function useLabels(item: FreeTierItem) {
  const { t } = useTranslation();
  const reset = item.reset_window
    ? t(`freeTiers.reset.${item.reset_window.replace(/[^a-z]/gi, "_").toLowerCase()}`, { defaultValue: item.reset_window })
    : "";
  return {
    t,
    auth: t(`freeTiers.auth.${item.auth_type}`, { defaultValue: item.auth_type }),
    category: t(`freeTiers.category.${item.category}`, { defaultValue: item.category }),
    reset,
  };
}

function Models({ models, max = MAX_MODELS }: { models?: string[]; max?: number }) {
  const { t } = useTranslation();
  if (!models?.length) return <span className="ft-dim">—</span>;
  const shown = models.slice(0, max);
  return (
    <span className="ft-models" title={models.join(", ")}>
      {shown.map((model) => (
        <code key={model}>{model}</code>
      ))}
      {models.length > shown.length ? <span className="ft-dim">{t("freeTiers.moreModels", { count: models.length - shown.length })}</span> : null}
    </span>
  );
}

function Actions({ item, onOpen }: ItemProps) {
  const { t } = useTranslation();
  return (
    <span className="cp-cell-actions ft-actions">
      {item.api_key_url ? (
        <a
          className="cp-icon-btn"
          href={item.api_key_url}
          target="_blank"
          rel="noreferrer"
          aria-label={t("freeTiers.getKey")}
          title={t("freeTiers.getKey")}
          onClick={stop}
        >
          <span className="material-symbols-outlined" aria-hidden>vpn_key</span>
        </a>
      ) : null}
      <ToolButton
        icon={item.configured ? "open_in_new" : "add_link"}
        onClick={(event) => {
          event.stopPropagation();
          onOpen();
        }}
      >
        {item.configured ? t("freeTiers.open") : t("freeTiers.setUp")}
      </ToolButton>
    </span>
  );
}

function Status({ item }: { item: FreeTierItem }) {
  const { t } = useTranslation();
  return item.configured ? (
    <StatusPill tone="success">{t("freeTiers.status.configured")}</StatusPill>
  ) : (
    <StatusPill tone="neutral" dot={false}>{t("freeTiers.status.available")}</StatusPill>
  );
}

export function FreeTierCard({ item, onOpen }: ItemProps) {
  const { t, auth, category, reset } = useLabels(item);
  return (
    <article
      className={cn("cp-card ft-card", item.configured && "ft-tone-success")}
      role="button"
      tabIndex={0}
      aria-label={t("freeTiers.openHint", { name: item.name })}
      onClick={onOpen}
      onKeyDown={(event) => onKey(event, onOpen)}
    >
      <div className="cp-card-head">
        <ProviderLogo className="ft-logo" providerId={item.provider_id} providerType={item.provider_id} />
        <div className="cp-card-title">
          <strong title={item.name}>{item.name}</strong>
          <span className="cp-card-tags">
            <Tag tone={item.category === "free" ? "info" : "neutral"}>{category}</Tag>
            <span className="ft-auth">
              <span className="material-symbols-outlined" aria-hidden>{AUTH_ICONS[item.auth_type] || "vpn_key"}</span>
              {auth}
            </span>
          </span>
        </div>
        <Status item={item} />
      </div>
      <div className="cp-card-body">
        <dl className="cp-facts ft-facts">
          <div>
            <dt>{t("freeTiers.dailyLimit")}</dt>
            <dd className={cn(!item.daily_limit && "ft-dim")} title={item.daily_limit}>{item.daily_limit || t("freeTiers.notPublished")}</dd>
          </div>
          <div>
            <dt>{t("freeTiers.resets")}</dt>
            <dd className={cn(!reset && "ft-dim")} title={reset}>{reset || "—"}</dd>
          </div>
        </dl>
        {item.models?.length ? <Models models={item.models} /> : null}
        {item.notes ? <p className="ft-notes">{item.notes}</p> : null}
      </div>
      <div className="cp-card-foot">
        <span className="cp-card-meta ft-id">{item.provider_id}</span>
        <Actions item={item} onOpen={onOpen} />
      </div>
    </article>
  );
}

export function FreeTierListHead() {
  const { t } = useTranslation();
  return (
    <div className="cp-list-head" aria-hidden>
      <span>{t("freeTiers.provider")}</span>
      <span>{t("freeTiers.access")}</span>
      <span>{t("freeTiers.dailyLimit")}</span>
      <span>{t("freeTiers.resets")}</span>
      <span>{t("freeTiers.models")}</span>
      <span>{t("freeTiers.statusLabel")}</span>
      <span />
    </div>
  );
}

export function FreeTierRow({ item, onOpen }: ItemProps) {
  const { t, auth, category, reset } = useLabels(item);
  return (
    <div
      className={cn("cp-list-row", item.configured && "ft-tone-success")}
      role="button"
      tabIndex={0}
      aria-label={t("freeTiers.openHint", { name: item.name })}
      onClick={onOpen}
      onKeyDown={(event) => onKey(event, onOpen)}
    >
      <span className="cp-cell ft-entity">
        <ProviderLogo className="ft-logo" providerId={item.provider_id} providerType={item.provider_id} />
        <span className="cp-cell-main">
          <strong title={item.name}>{item.name}</strong>
          <small>{item.provider_id}</small>
        </span>
      </span>
      <span className="cp-cell cp-cell-main">
        <strong className="ft-auth-strong">{auth}</strong>
        <small>{category}</small>
      </span>
      <span className={cn("cp-cell ft-cell-text", !item.daily_limit && "ft-dim")} title={item.daily_limit}>{item.daily_limit || "—"}</span>
      <span className={cn("cp-cell ft-cell-text", !reset && "ft-dim")} title={reset}>{reset || "—"}</span>
      <span className="cp-cell ft-cell-models">
        <Models models={item.models} max={1} />
      </span>
      <span className="cp-cell">
        <Status item={item} />
      </span>
      <Actions item={item} onOpen={onOpen} />
    </div>
  );
}
