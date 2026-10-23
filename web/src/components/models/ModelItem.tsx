import type { KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { ProviderLogo } from "../providers/ProviderLogo";
import { Toggle, cn } from "../ui";
import { ActionMenu, IconAction, StatusPill, Tag, type ActionMenuItem, type Tone } from "../page-kit";
import { formatRetryIn, type ChainStep, type ChainStepState, type ModelHealth, type ModelView } from "./modelHealth";
import type { ModelRecord } from "./types";

export type ModelItemHandlers = {
  openPriority: (model: ModelRecord) => void;
  editDetails: (model: ModelRecord) => void;
  setEnabled: (model: ModelRecord, enabled: boolean) => void;
  select: (model: ModelRecord, selected: boolean) => void;
  copyId: (model: ModelRecord) => void;
  remove: (model: ModelRecord) => void;
};

type ItemProps = {
  view: ModelView;
  handlers: ModelItemHandlers;
  selected: boolean;
  busy: boolean;
  /** Suggested routes are only known once provider discovery has settled. */
  discovering: boolean;
};

export const HEALTH_TONE: Record<ModelHealth, Tone> = {
  serving: "success",
  degraded: "warning",
  attention: "danger",
  disabled: "muted",
};

const STEP_TONE: Record<ChainStepState, Tone> = {
  ok: "success",
  suggested: "info",
  off: "muted",
  paused: "danger",
  probing: "warning",
  failing: "warning",
  "provider-off": "muted",
  unhealthy: "warning",
};

function stepTone(step: ChainStep): Tone {
  if (step.state === "unhealthy" && step.providerStatus === "auth_required") return "danger";
  return STEP_TONE[step.state];
}

export function stepLabel(step: ChainStep, t: TFunction) {
  switch (step.state) {
    case "paused": {
      const retry = formatRetryIn(step.failover?.retry_in_seconds ?? 0);
      return retry ? t("models.step.pausedFor", { time: retry }) : t("models.step.paused");
    }
    case "failing":
      return t("models.step.failing", { failures: step.failover?.failures ?? 0, threshold: step.failover?.threshold ?? 0 });
    case "unhealthy":
      return t(`models.providerStatus.${step.providerStatus}`, { defaultValue: step.providerStatus || "" });
    default:
      return t(`models.step.${step.state}`);
  }
}

function stepHint(step: ChainStep, t: TFunction) {
  const parts = [t(`models.stepHint.${step.state}`, { provider: step.providerLabel })];
  if (step.failover?.last_error) parts.push(step.failover.last_error);
  parts.push(t("models.accountCount", { count: step.accounts }));
  return parts.join(" · ");
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

export function HealthPill({ health }: { health: ModelHealth }) {
  const { t } = useTranslation();
  return (
    <StatusPill tone={HEALTH_TONE[health]} title={t(`models.healthHint.${health}`)}>
      {t(`models.health.${health}`)}
    </StatusPill>
  );
}

const MAX_CARD_STEPS = 4;

function ChainList({ view, discovering }: { view: ModelView; discovering: boolean }) {
  const { t } = useTranslation();
  const visible = view.steps.slice(0, MAX_CARD_STEPS);
  const hidden = view.steps.length - visible.length;
  return (
    <div className="md-chain">
      <div className="md-chain-head">
        <span>{t("models.chainTitle")}</span>
        {view.usableCount > 0 ? <span>{t("models.chainServing", { count: view.usableCount })}</span> : null}
      </div>
      {view.steps.length === 0 ? (
        <p className={cn("md-chain-empty", view.model.Enabled && !discovering && "is-danger")}>
          <span className="material-symbols-outlined" aria-hidden>{discovering ? "progress_activity" : "link_off"}</span>
          {discovering ? t("models.discoveringProviders") : t("models.noProviderRoutes")}
        </p>
      ) : (
        <ol className="md-steps">
          {visible.map((step) => (
            <li key={step.key} className={cn("md-step", `md-step-${step.state}`)} title={stepHint(step, t)}>
              <span className={cn("md-step-rank", step.rank === 1 && "is-primary")}>
                {step.rank > 0 ? step.rank : step.saved ? "–" : "+"}
              </span>
              <ProviderLogo className="cp-tab-logo md-step-logo" providerId={step.providerId} providerType={step.providerType} />
              <span className="md-step-main">
                <strong>{step.providerLabel}</strong>
                <code>{step.upstreamModel}</code>
              </span>
              <span className={cn("md-step-state", `cp-tone-${stepTone(step)}`)}>{stepLabel(step, t)}</span>
            </li>
          ))}
        </ol>
      )}
      {hidden > 0 || view.hiddenCount > 0 || view.suggestedCount > 0 ? (
        <div className="md-chain-notes">
          {hidden > 0 ? <span>{t("models.moreSteps", { count: hidden })}</span> : null}
          {view.suggestedCount > 0 ? (
            <span className="cp-tone-info" title={t("models.suggestedHint")}>
              {t("models.suggestedRoutes", { count: view.suggestedCount })}
            </span>
          ) : null}
          {view.hiddenCount > 0 ? (
            <span title={t("models.hiddenRoutesHint")}>{t("models.hiddenRoutes", { count: view.hiddenCount })}</span>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

/** Compact chain for list rows: "1 Claude Code › 2 Antigravity › +1". */
function InlineChain({ view }: { view: ModelView }) {
  const { t } = useTranslation();
  const steps = view.steps.filter((step) => step.saved);
  if (steps.length === 0) {
    return <span className="md-inline-empty">{t("models.noProviderRoutes")}</span>;
  }
  const visible = steps.slice(0, 3);
  return (
    <span className="md-inline-chain">
      {visible.map((step) => (
        <span key={step.key} className={cn("md-inline-step", `md-step-${step.state}`)} title={`${step.providerLabel} · ${step.upstreamModel} · ${stepLabel(step, t)}`}>
          <span className={cn("md-inline-dot", `cp-tone-${stepTone(step)}`)} aria-hidden />
          <span className="md-inline-rank">{step.rank > 0 ? step.rank : "–"}</span>
          <span className="md-inline-name">{step.providerLabel}</span>
        </span>
      ))}
      {steps.length > visible.length ? <span className="md-inline-more">+{steps.length - visible.length}</span> : null}
    </span>
  );
}

function menuItems(view: ModelView, handlers: ModelItemHandlers, busy: boolean, t: TFunction): ActionMenuItem[] {
  const { model } = view;
  return [
    { key: "priority", icon: "low_priority", label: t("models.menu.editPriority"), hint: t("models.menu.editPriorityHint"), onSelect: () => handlers.openPriority(model) },
    { key: "details", icon: "edit", label: t("models.menu.editDetails"), hint: t("models.menu.editDetailsHint"), disabled: busy, onSelect: () => handlers.editDetails(model) },
    {
      key: "toggle",
      icon: model.Enabled ? "toggle_off" : "toggle_on",
      label: model.Enabled ? t("models.menu.disable") : t("models.menu.enable"),
      hint: model.Enabled ? t("models.menu.disableHint") : t("models.menu.enableHint"),
      disabled: busy,
      onSelect: () => handlers.setEnabled(model, !model.Enabled),
    },
    { key: "copy", icon: "content_copy", label: t("models.menu.copyId"), hint: model.ID, onSelect: () => handlers.copyId(model) },
    { key: "delete", icon: "delete", label: t("models.deleteModel"), danger: true, disabled: busy, onSelect: () => handlers.remove(model) },
  ];
}

function QuickActions({ view, handlers, busy }: { view: ModelView; handlers: ModelItemHandlers; busy: boolean }) {
  const { t } = useTranslation();
  return (
    <div className="md-actions" onClick={stop}>
      <IconAction icon="low_priority" label={t("models.menu.editPriority")} onClick={() => handlers.openPriority(view.model)} />
      <ActionMenu label={t("models.menu.more")} items={menuItems(view, handlers, busy, t)} />
    </div>
  );
}

function EnabledToggle({ view, handlers, busy }: { view: ModelView; handlers: ModelItemHandlers; busy: boolean }) {
  const { t } = useTranslation();
  const label = view.model.Enabled ? t("models.menu.disable") : t("models.menu.enable");
  return (
    <span className="md-toggle" onClick={stop} title={label}>
      <Toggle
        checked={view.model.Enabled}
        disabled={busy}
        onChange={(event) => handlers.setEnabled(view.model, event.target.checked)}
        aria-label={`${label}: ${view.model.DisplayName || view.model.ID}`}
      />
    </span>
  );
}

function SelectBox({ view, handlers, selected }: { view: ModelView; handlers: ModelItemHandlers; selected: boolean }) {
  const { t } = useTranslation();
  return (
    <input
      type="checkbox"
      className="cp-check"
      checked={selected}
      onClick={stop}
      onChange={(event) => handlers.select(view.model, event.target.checked)}
      aria-label={t("models.selectModel", { name: view.model.DisplayName || view.model.ID })}
    />
  );
}

function AliasMeta({ model }: { model: ModelRecord }) {
  const { t } = useTranslation();
  const aliases = model.Aliases || [];
  return (
    <span title={aliases.length ? t("models.aliasesHint", { aliases: aliases.join(", ") }) : t("models.noAliasesHint")}>
      <span className="material-symbols-outlined" aria-hidden>alternate_email</span>
      {aliases.length ? aliases.join(", ") : t("models.noAliases")}
    </span>
  );
}

function itemClasses(view: ModelView, base: "md-card" | "md-row", selected: boolean, busy: boolean) {
  return cn(
    base,
    base === "md-card" ? "cp-card" : "cp-list-row",
    `md-health-${view.health}`,
    !view.model.Enabled && "is-off",
    selected && "is-selected",
    busy && "is-busy",
  );
}

export function ModelCard({ view, handlers, selected, busy, discovering }: ItemProps) {
  const { t } = useTranslation();
  const { model } = view;
  const name = model.DisplayName || model.ID;
  return (
    <article
      className={itemClasses(view, "md-card", selected, busy)}
      role="button"
      tabIndex={0}
      aria-label={t("models.openPriorityFor", { name })}
      onClick={() => handlers.openPriority(model)}
      onKeyDown={(event) => openOnKey(event, () => handlers.openPriority(model))}
    >
      <header className="cp-card-head">
        <SelectBox view={view} handlers={handlers} selected={selected} />
        <div className="cp-card-title">
          <strong title={name}>{name}</strong>
          <div className="cp-card-tags">
            <HealthPill health={view.health} />
            <code className="md-id" title={model.ID}>{model.ID}</code>
          </div>
        </div>
        <EnabledToggle view={view} handlers={handlers} busy={busy} />
      </header>

      <div className="cp-card-body">
        <ChainList view={view} discovering={discovering} />
      </div>

      <footer className="cp-card-foot">
        <div className="cp-card-meta">
          <AliasMeta model={model} />
          {(model.Capabilities || []).length > 0 ? (
            <span className="md-caps">
              {model.Capabilities.slice(0, 3).map((capability) => (
                <Tag key={capability} tone="neutral">{capability}</Tag>
              ))}
            </span>
          ) : null}
        </div>
        <QuickActions view={view} handlers={handlers} busy={busy} />
      </footer>
    </article>
  );
}

export function ModelRow({ view, handlers, selected, busy }: ItemProps) {
  const { t } = useTranslation();
  const { model } = view;
  const name = model.DisplayName || model.ID;
  const aliases = model.Aliases || [];
  return (
    <div
      className={itemClasses(view, "md-row", selected, busy)}
      role="button"
      tabIndex={0}
      aria-label={t("models.openPriorityFor", { name })}
      onClick={() => handlers.openPriority(model)}
      onKeyDown={(event) => openOnKey(event, () => handlers.openPriority(model))}
    >
      <span className="cp-cell"><SelectBox view={view} handlers={handlers} selected={selected} /></span>
      <span className="cp-cell cp-cell-main">
        <strong title={name}>{name}</strong>
        <small title={model.ID}>{model.ID}</small>
      </span>
      <span className="cp-cell"><HealthPill health={view.health} /></span>
      <span className="cp-cell"><InlineChain view={view} /></span>
      <span className="cp-cell cp-cell-mono" title={aliases.join(", ")}>{aliases.length ? aliases.join(", ") : "—"}</span>
      <span className="cp-cell"><EnabledToggle view={view} handlers={handlers} busy={busy} /></span>
      <span className="cp-cell"><QuickActions view={view} handlers={handlers} busy={busy} /></span>
    </div>
  );
}
