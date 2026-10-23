import type { KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { Toggle, cn } from "../ui";
import { ActionMenu, IconAction, StatusPill, Tag, type ActionMenuItem, type Tone } from "../page-kit";
import type { ComboHealth, ComboStep, ComboStepState, ComboView } from "./comboHealth";
import type { ComboRecord } from "./types";

export type ComboItemHandlers = {
  edit: (combo: ComboRecord) => void;
  duplicate: (combo: ComboRecord) => void;
  setEnabled: (combo: ComboRecord, enabled: boolean) => void;
  select: (combo: ComboRecord, selected: boolean) => void;
  copyId: (combo: ComboRecord) => void;
  remove: (combo: ComboRecord) => void;
};

type ItemProps = {
  view: ComboView;
  handlers: ComboItemHandlers;
  selected: boolean;
  busy: boolean;
  copied: boolean;
};

export const HEALTH_TONE: Record<ComboHealth, Tone> = {
  ready: "success",
  partial: "warning",
  broken: "danger",
  paused: "muted",
};

export const STEP_TONE: Record<ComboStepState, Tone> = {
  ok: "success",
  direct: "info",
  off: "muted",
  missing: "danger",
};

const MAX_CARD_STEPS = 5;

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

export function ComboHealthPill({ health }: { health: ComboHealth }) {
  const { t } = useTranslation();
  return (
    <StatusPill tone={HEALTH_TONE[health]} title={t(`combos.healthHint.${health}`)}>
      {t(`combos.health.${health}`)}
    </StatusPill>
  );
}

export function ClientTag({ client }: { client: ComboView["client"] }) {
  const { t } = useTranslation();
  return (
    <Tag tone={client === "general" ? "neutral" : "primary"} title={t(`combos.clientHint.${client}`)}>
      {t(`combos.client.${client}`)}
    </Tag>
  );
}

function stepTitle(step: ComboStep, t: TFunction) {
  return [step.id, t(`combos.stepHint.${step.state}`)].join(" · ");
}

function StepList({ view }: { view: ComboView }) {
  const { t } = useTranslation();
  const visible = view.steps.slice(0, MAX_CARD_STEPS);
  const hidden = view.steps.length - visible.length;
  return (
    <div className="cb-chain">
      <div className="cb-chain-head">
        <span>{t("combos.chainTitle")}</span>
        {view.steps.length > 0 ? (
          <span className={cn(view.usableCount < view.steps.length && "is-short")}>
            {t("combos.chainUsable", { usable: view.usableCount, count: view.steps.length })}
          </span>
        ) : null}
      </div>
      {view.steps.length === 0 ? (
        <p className={cn("cb-chain-empty", view.combo.enabled && "is-danger")}>
          <span className="material-symbols-outlined" aria-hidden>link_off</span>
          {t("combos.noSteps")}
        </p>
      ) : (
        <ol className="cb-steps">
          {visible.map((step, index) => (
            <li key={step.key} className={cn("cb-step", `cb-step-${step.state}`)} title={stepTitle(step, t)}>
              <span className={cn("cb-step-rank", index + 1 === view.firstUsable && view.combo.enabled && "is-primary")}>{index + 1}</span>
              <span className="cb-step-main">
                <strong>{step.label}</strong>
                {step.detail ? <code>{step.detail}</code> : null}
              </span>
              <span className="cb-step-kind">{t(`combos.stepKind.${step.kind}`)}</span>
              <span className={cn("cb-step-state", `cp-tone-${STEP_TONE[step.state]}`)}>{t(`combos.stepState.${step.state}`)}</span>
            </li>
          ))}
        </ol>
      )}
      {hidden > 0 ? <span className="cb-chain-more">{t("combos.moreSteps", { count: hidden })}</span> : null}
    </div>
  );
}

/** Compact chain for list rows: "1 Claude Sonnet › 2 GPT-5.5 › +1". */
function InlineChain({ view }: { view: ComboView }) {
  const { t } = useTranslation();
  if (view.steps.length === 0) return <span className="cb-inline-empty">{t("combos.noSteps")}</span>;
  const visible = view.steps.slice(0, 3);
  return (
    <span className="cb-inline-chain">
      {visible.map((step, index) => (
        <span key={step.key} className={cn("cb-inline-step", `cb-step-${step.state}`)} title={stepTitle(step, t)}>
          <span className={cn("cb-inline-dot", `cp-tone-${STEP_TONE[step.state]}`)} aria-hidden />
          <span className="cb-inline-rank">{index + 1}</span>
          <span className="cb-inline-name">{step.label}</span>
        </span>
      ))}
      {view.steps.length > visible.length ? <span className="cb-inline-more">+{view.steps.length - visible.length}</span> : null}
    </span>
  );
}

function menuItems(view: ComboView, handlers: ComboItemHandlers, busy: boolean, t: TFunction): ActionMenuItem[] {
  const { combo } = view;
  return [
    { key: "edit", icon: "edit", label: t("combos.menu.edit"), hint: t("combos.menu.editHint"), disabled: busy, onSelect: () => handlers.edit(combo) },
    { key: "duplicate", icon: "content_copy", label: t("combos.menu.duplicate"), hint: t("combos.menu.duplicateHint"), onSelect: () => handlers.duplicate(combo) },
    {
      key: "toggle",
      icon: combo.enabled ? "toggle_off" : "toggle_on",
      label: combo.enabled ? t("combos.menu.disable") : t("combos.menu.enable"),
      hint: combo.enabled ? t("combos.menu.disableHint") : t("combos.menu.enableHint"),
      disabled: busy,
      onSelect: () => handlers.setEnabled(combo, !combo.enabled),
    },
    { key: "copy", icon: "tag", label: t("combos.copyComboId"), hint: combo.id, onSelect: () => handlers.copyId(combo) },
    { key: "delete", icon: "delete", label: t("combos.deleteCombo"), danger: true, disabled: busy, onSelect: () => handlers.remove(combo) },
  ];
}

function QuickActions({ view, handlers, busy, copied }: { view: ComboView; handlers: ComboItemHandlers; busy: boolean; copied: boolean }) {
  const { t } = useTranslation();
  return (
    <div className="cb-actions" onClick={stop}>
      <IconAction
        icon={copied ? "check" : "content_copy"}
        active={copied}
        label={copied ? t("combos.copied") : t("combos.copyComboId")}
        onClick={() => handlers.copyId(view.combo)}
      />
      <ActionMenu label={t("combos.menu.more")} items={menuItems(view, handlers, busy, t)} />
    </div>
  );
}

function EnabledToggle({ view, handlers, busy }: { view: ComboView; handlers: ComboItemHandlers; busy: boolean }) {
  const { t } = useTranslation();
  const label = view.combo.enabled ? t("combos.menu.disable") : t("combos.menu.enable");
  return (
    <span className="cb-toggle" onClick={stop} title={label}>
      <Toggle
        checked={view.combo.enabled}
        disabled={busy}
        onChange={(event) => handlers.setEnabled(view.combo, event.target.checked)}
        aria-label={`${label}: ${view.name}`}
      />
    </span>
  );
}

function SelectBox({ view, handlers, selected }: { view: ComboView; handlers: ComboItemHandlers; selected: boolean }) {
  const { t } = useTranslation();
  return (
    <input
      type="checkbox"
      className="cp-check"
      checked={selected}
      onClick={stop}
      onChange={(event) => handlers.select(view.combo, event.target.checked)}
      aria-label={t("combos.selectCombo", { name: view.name })}
    />
  );
}

function itemClasses(view: ComboView, base: "cb-card" | "cb-row", selected: boolean, busy: boolean) {
  return cn(
    base,
    base === "cb-card" ? "cp-card" : "cp-list-row",
    `cb-health-${view.health}`,
    !view.combo.enabled && "is-off",
    selected && "is-selected",
    busy && "is-busy",
  );
}

export function ComboCard({ view, handlers, selected, busy, copied }: ItemProps) {
  const { t } = useTranslation();
  const { combo } = view;
  return (
    <article
      className={itemClasses(view, "cb-card", selected, busy)}
      role="button"
      tabIndex={0}
      aria-label={t("combos.openCombo", { name: view.name })}
      onClick={() => handlers.edit(combo)}
      onKeyDown={(event) => openOnKey(event, () => handlers.edit(combo))}
    >
      <header className="cp-card-head">
        <SelectBox view={view} handlers={handlers} selected={selected} />
        <div className="cp-card-title">
          <strong title={view.name}>{view.name}</strong>
          <div className="cp-card-tags">
            <ComboHealthPill health={view.health} />
            <ClientTag client={view.client} />
            <code className="cb-id" title={combo.id}>{combo.id}</code>
          </div>
        </div>
        <EnabledToggle view={view} handlers={handlers} busy={busy} />
      </header>

      <div className="cp-card-body">
        <StepList view={view} />
      </div>

      <footer className="cp-card-foot">
        <div className="cp-card-meta">
          <span title={t("combos.routingHint")}>
            <span className="material-symbols-outlined" aria-hidden>low_priority</span>
            {t("combos.routingOrdered")}
          </span>
          <span title={combo.rewrite_response_model !== false ? t("combos.rewriteOnHint") : t("combos.rewriteOffHint")}>
            <span className="material-symbols-outlined" aria-hidden>{combo.rewrite_response_model !== false ? "badge" : "swap_horiz"}</span>
            {combo.rewrite_response_model !== false ? t("combos.rewriteOn") : t("combos.rewriteOff")}
          </span>
        </div>
        <QuickActions view={view} handlers={handlers} busy={busy} copied={copied} />
      </footer>
    </article>
  );
}

export function ComboRow({ view, handlers, selected, busy, copied }: ItemProps) {
  const { t } = useTranslation();
  const { combo } = view;
  return (
    <div
      className={itemClasses(view, "cb-row", selected, busy)}
      role="button"
      tabIndex={0}
      aria-label={t("combos.openCombo", { name: view.name })}
      onClick={() => handlers.edit(combo)}
      onKeyDown={(event) => openOnKey(event, () => handlers.edit(combo))}
    >
      <span className="cp-cell"><SelectBox view={view} handlers={handlers} selected={selected} /></span>
      <span className="cp-cell cp-cell-main">
        <strong title={view.name}>{view.name}</strong>
        <small title={combo.id}>{combo.id}</small>
      </span>
      <span className="cp-cell"><ComboHealthPill health={view.health} /></span>
      <span className="cp-cell"><ClientTag client={view.client} /></span>
      <span className="cp-cell"><InlineChain view={view} /></span>
      <span className="cp-cell"><EnabledToggle view={view} handlers={handlers} busy={busy} /></span>
      <span className="cp-cell"><QuickActions view={view} handlers={handlers} busy={busy} copied={copied} /></span>
    </div>
  );
}
