import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Field, Input, Toggle, cn } from "../ui";
import { LinkButton, PageNote, SectionHead, Segmented } from "../page-kit";
import type { BorrowingPolicy, BorrowMode, LendingLimits, LendingPolicy, ModelOption } from "./types";
import { cleanLending, fmtBytes, toAmount, toCount, useDraft } from "./utils";

/* ------------------------------------------------------------
   Model multi-select: chips that toggle; none selected = all.
   ------------------------------------------------------------ */
function ModelPicker({
  options,
  value,
  onChange,
  disabled,
}: {
  options: ModelOption[];
  value: string[];
  onChange: (next: string[]) => void;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  const selected = new Set(value);
  // Ids that are no longer offered stay visible so they can be removed.
  const known = new Set(options.map((option) => option.id));
  const all = [...options, ...value.filter((id) => !known.has(id)).map((id) => ({ id, label: id }))];
  const toggle = (id: string) => {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    onChange(all.map((option) => option.id).filter((optionId) => next.has(optionId)));
  };

  return (
    <div className="cl-picker">
      <div className="cl-picker-head">
        <span className="field-label">{t("clusters.lending.models")}</span>
        <small>{value.length ? t("clusters.lending.modelsSelected", { count: value.length }) : t("clusters.lending.modelsAll")}</small>
        {value.length ? (
          <LinkButton disabled={disabled} onClick={() => onChange([])}>{t("clusters.lending.modelsClear")}</LinkButton>
        ) : null}
      </div>
      {all.length === 0 ? (
        <p className="cl-muted">{t("clusters.lending.noModels")}</p>
      ) : (
        <div className="cl-chips" role="group" aria-label={t("clusters.lending.models")}>
          {all.map((option) => {
            const on = selected.has(option.id);
            return (
              <button
                key={option.id}
                type="button"
                className={cn("cl-chip", on && "is-on", !known.has(option.id) && "is-missing")}
                aria-pressed={on}
                disabled={disabled}
                title={option.label !== option.id ? `${option.label} · ${option.id}` : option.id}
                onClick={() => toggle(option.id)}
              >
                <span className="material-symbols-outlined" aria-hidden>{on ? "check_box" : "check_box_outline_blank"}</span>
                <span>{option.id}</span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

/* ------------------------------------------------------------
   Lending form — inline in the setup cards and on Policies.
   ------------------------------------------------------------ */
export function LendingFields({
  value,
  onChange,
  modelOptions,
  disabled,
  compact,
}: {
  value: LendingPolicy;
  onChange: (next: LendingPolicy) => void;
  modelOptions: ModelOption[];
  disabled?: boolean;
  /** Setup cards: RPM and streams up front, the other limits behind a link. */
  compact?: boolean;
}) {
  const { t, i18n } = useTranslation();
  const [showMore, setShowMore] = useState(false);
  const limitsDisabled = disabled || !value.enabled;
  const setLimit = (field: keyof LendingLimits, next: number) => onChange({ ...value, limits: { ...value.limits, [field]: next } });
  const expanded = !compact || showMore;
  const bytesHint = value.limits.max_input_bytes
    ? t("clusters.lending.maxInputBytesValue", { size: fmtBytes(value.limits.max_input_bytes, i18n.language) })
    : t("clusters.lending.zeroUnlimited");

  return (
    <div className={cn("cl-lending", !value.enabled && "is-off")}>
      <Toggle
        className="cl-consent"
        checked={value.enabled}
        disabled={disabled}
        onChange={(event) => onChange({ ...value, enabled: event.target.checked })}
        label={
          <span className="cl-consent-copy">
            <strong>{t("clusters.lending.consent")}</strong>
            <small>{value.enabled ? t("clusters.lending.consentOn") : t("clusters.lending.consentOff")}</small>
          </span>
        }
      />

      <div className="cl-field-grid">
        <Field label={t("clusters.lending.rpm")} hint={t("clusters.lending.perNodeHint")}>
          <Input
            type="number"
            min={0}
            step={1}
            inputMode="numeric"
            disabled={limitsDisabled}
            value={value.limits.requests_per_minute}
            onChange={(event) => setLimit("requests_per_minute", toCount(event.target.value))}
          />
        </Field>
        <Field label={t("clusters.lending.streams")} hint={t("clusters.lending.perNodeHint")}>
          <Input
            type="number"
            min={0}
            step={1}
            inputMode="numeric"
            disabled={limitsDisabled}
            value={value.limits.concurrent_streams}
            onChange={(event) => setLimit("concurrent_streams", toCount(event.target.value))}
          />
        </Field>
        {expanded ? (
          <>
            <Field label={t("clusters.lending.maxInputBytes")} hint={bytesHint}>
              <Input
                type="number"
                min={0}
                step={1024}
                inputMode="numeric"
                disabled={limitsDisabled}
                value={value.limits.max_input_bytes}
                onChange={(event) => setLimit("max_input_bytes", toCount(event.target.value))}
              />
            </Field>
            <Field label={t("clusters.lending.budget")} hint={t("clusters.lending.zeroUnlimited")}>
              <Input
                type="number"
                min={0}
                step={0.5}
                inputMode="decimal"
                disabled={limitsDisabled}
                value={value.limits.budget_usd_per_day}
                onChange={(event) => setLimit("budget_usd_per_day", toAmount(event.target.value))}
              />
            </Field>
          </>
        ) : null}
      </div>

      {expanded ? (
        <ModelPicker options={modelOptions} value={value.models} disabled={limitsDisabled} onChange={(models) => onChange({ ...value, models })} />
      ) : null}

      {compact ? (
        <LinkButton className="cl-more-link" onClick={() => setShowMore((current) => !current)} aria-expanded={showMore}>
          {showMore ? t("clusters.lending.fewerLimits") : t("clusters.lending.moreLimits")}
        </LinkButton>
      ) : null}
    </div>
  );
}

/* ------------------------------------------------------------
   Policies tab
   ------------------------------------------------------------ */
const MODES: BorrowMode[] = ["fallback", "shared"];

export function SaveBar({ dirty, saving, onSave, onReset }: { dirty: boolean; saving: boolean; onSave: () => void; onReset: () => void }) {
  const { t } = useTranslation();
  return (
    <div className="cl-savebar">
      <span className={cn("cl-savebar-state", dirty && "is-dirty")}>
        {dirty ? t("clusters.unsavedChanges") : t("clusters.allSaved")}
      </span>
      <Button variant="secondary" size="sm" onClick={onReset} disabled={!dirty || saving}>{t("common.reset")}</Button>
      <Button size="sm" icon="save" onClick={onSave} loading={saving} disabled={!dirty}>{t("common.save")}</Button>
    </div>
  );
}

export function ClusterPolicies({
  lending,
  borrowing,
  modelOptions,
  busy,
  onSaveLending,
  onSaveBorrowing,
}: {
  lending: LendingPolicy;
  borrowing: BorrowingPolicy;
  modelOptions: ModelOption[];
  busy: string | null;
  onSaveLending: (policy: LendingPolicy) => Promise<boolean>;
  onSaveBorrowing: (policy: BorrowingPolicy) => Promise<boolean>;
}) {
  const { t } = useTranslation();
  const lendingDraft = useDraft(lending);
  const borrowingDraft = useDraft(borrowing);
  const savingLending = busy === "lending";
  const savingBorrowing = busy === "borrowing";

  const saveLending = async () => {
    if (await onSaveLending(cleanLending(lendingDraft.draft))) lendingDraft.markSaved();
  };
  const saveBorrowing = async () => {
    if (await onSaveBorrowing(borrowingDraft.draft)) borrowingDraft.markSaved();
  };

  const mode = borrowingDraft.draft.mode;
  return (
    <div className="cl-policies">
      <section className="cp-panel cl-panel" aria-labelledby="cl-lending-title">
        <SectionHead
          leading={<span className="material-symbols-outlined cp-section-icon" aria-hidden>volunteer_activism</span>}
          title={<span id="cl-lending-title">{t("clusters.lending.title")}</span>}
          subtitle={t("clusters.lending.subtitle")}
        />
        <LendingFields
          value={lendingDraft.draft}
          onChange={lendingDraft.setDraft}
          modelOptions={modelOptions}
          disabled={savingLending}
        />
        <SaveBar dirty={lendingDraft.dirty} saving={savingLending} onSave={() => void saveLending()} onReset={lendingDraft.reset} />
      </section>

      <section className="cp-panel cl-panel" aria-labelledby="cl-borrowing-title">
        <SectionHead
          leading={<span className="material-symbols-outlined cp-section-icon" aria-hidden>call_received</span>}
          title={<span id="cl-borrowing-title">{t("clusters.borrowing.title")}</span>}
          subtitle={t("clusters.borrowing.subtitle")}
        />
        <Toggle
          className="cl-consent"
          checked={borrowingDraft.draft.enabled}
          disabled={savingBorrowing}
          onChange={(event) => borrowingDraft.setDraft({ ...borrowingDraft.draft, enabled: event.target.checked })}
          label={
            <span className="cl-consent-copy">
              <strong>{t("clusters.borrowing.enabled")}</strong>
              <small>{borrowingDraft.draft.enabled ? t("clusters.borrowing.enabledOn") : t("clusters.borrowing.enabledOff")}</small>
            </span>
          }
        />
        <div className={cn("cl-mode", !borrowingDraft.draft.enabled && "is-off")}>
          <span className="field-label">{t("clusters.borrowing.mode")}</span>
          <Segmented
            label={t("clusters.borrowing.mode")}
            value={mode}
            onChange={(next) => borrowingDraft.setDraft({ ...borrowingDraft.draft, mode: next })}
            items={MODES.map((value) => ({ value, label: t(`clusters.borrowing.modes.${value}`) }))}
          />
          <ul className="cl-mode-list">
            {MODES.map((value) => (
              <li key={value} className={cn(value === mode && "is-active")}>
                <strong>{t(`clusters.borrowing.modes.${value}`)}</strong>
                <span>{t(`clusters.borrowing.modeHint.${value}`)}</span>
              </li>
            ))}
          </ul>
        </div>
        <PageNote icon="receipt_long">{t("clusters.borrowing.ledgerNote")}</PageNote>
        <SaveBar dirty={borrowingDraft.dirty} saving={savingBorrowing} onSave={() => void saveBorrowing()} onReset={borrowingDraft.reset} />
      </section>
    </div>
  );
}
