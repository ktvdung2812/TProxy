import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Field, Input, Modal, Select, Toggle, cn } from "../ui";
import { IconAction, PageNote, StatusPill, ToolButton } from "../page-kit";
import type { ModelOption } from "../../lib/modelOptions";
import type { ComboFormData } from "./types";
import { COMBO_ID_REGEX, emptyComboForm, validateComboForm } from "./utils";

type Props = {
  open: boolean;
  editing: boolean;
  initial: ComboFormData | null;
  modelOptions: ModelOption[];
  existingIds: string[];
  /** Model and combo IDs that exist but are turned off (they are not offered as options). */
  disabledTargets?: Set<string>;
  saving: boolean;
  onClose: () => void;
  onSubmit: (form: ComboFormData) => void;
};

function moveItem<T>(items: T[], from: number, to: number) {
  if (to < 0 || to >= items.length) return items;
  const next = [...items];
  const [item] = next.splice(from, 1);
  next.splice(to, 0, item);
  return next;
}

function sameForm(left: ComboFormData, right: ComboFormData) {
  return JSON.stringify(left) === JSON.stringify(right);
}

export function ComboFormModal({
  open,
  editing,
  initial,
  modelOptions,
  existingIds,
  disabledTargets,
  saving,
  onClose,
  onSubmit,
}: Props) {
  const { t } = useTranslation();
  const [form, setForm] = useState<ComboFormData>(emptyComboForm());
  const [baseline, setBaseline] = useState<ComboFormData>(emptyComboForm());
  const [idError, setIdError] = useState("");

  useEffect(() => {
    if (!open) return;
    const seed = initial || emptyComboForm();
    setForm(seed);
    setBaseline(seed);
    setIdError("");
  }, [open, initial]);

  const groupedOptions = useMemo(() => {
    const groups: ModelOption["group"][] = [];
    for (const option of modelOptions) {
      if (!groups.includes(option.group)) groups.push(option.group);
    }
    return groups;
  }, [modelOptions]);
  const knownValues = useMemo(() => new Set(modelOptions.map((option) => option.value)), [modelOptions]);

  const validationError = validateComboForm(form, existingIds, editing);
  const dirty = !sameForm(form, baseline);
  const canSubmit = !saving && !validationError && !idError;

  const handleIdChange = (value: string) => {
    setForm((current) => ({ ...current, id: value }));
    if (!value.trim()) {
      setIdError(t("combos.idRequired"));
      return;
    }
    if (!COMBO_ID_REGEX.test(value.trim())) {
      setIdError(t("combos.idAllowed"));
      return;
    }
    if (!editing && existingIds.includes(value.trim())) {
      setIdError(t("combos.idInUse"));
      return;
    }
    setIdError("");
  };

  const addItem = () => {
    // Suggest the first target not already in the chain, so repeated clicks build a useful order.
    const used = new Set(form.items.map((item) => item.public_model_id));
    const next = modelOptions.find((option) => !used.has(option.value))?.value || modelOptions[0]?.value || "";
    setForm((current) => ({ ...current, items: [...current.items, { public_model_id: next }] }));
  };

  const updateItem = (index: number, patch: Partial<ComboFormData["items"][number]>) => {
    setForm((current) => ({
      ...current,
      items: current.items.map((item, itemIndex) => (itemIndex === index ? { ...item, ...patch } : item)),
    }));
  };

  const removeItem = (index: number) => {
    setForm((current) => ({ ...current, items: current.items.filter((_, itemIndex) => itemIndex !== index) }));
  };

  const submit = () => {
    if (canSubmit) onSubmit(form);
  };

  // Only explain a blocked Save once the user has touched something.
  const showValidation = validationError && validationError !== "idRequired" && !idError && (dirty || editing);

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={editing ? t("combos.editCombo") : t("combos.createComboTitle")}
      subtitle={t("combos.comboSubtitle")}
      icon="layers"
      size="lg"
      className="cb-form-modal"
      footer={
        <>
          {dirty && editing ? <StatusPill tone="warning">{t("combos.form.unsaved")}</StatusPill> : null}
          <Button variant="ghost" size="sm" onClick={onClose} disabled={saving}>
            {t("common.cancel")}
          </Button>
          <Button variant="primary" size="sm" icon="save" loading={saving} disabled={!canSubmit} onClick={submit}>
            {editing ? t("combos.saveChanges") : t("combos.createComboBtn")}
          </Button>
        </>
      }
    >
      <form
        className="cb-form"
        onSubmit={(event) => {
          event.preventDefault();
          submit();
        }}
      >
        <div className="cb-form-grid">
          <Field label={t("combos.comboId")} hint={editing ? t("combos.form.idLocked") : t("combos.comboIdHint")} error={idError || undefined}>
            <Input
              value={form.id}
              onChange={(event) => handleIdChange(event.target.value)}
              placeholder={t("combos.comboIdPlaceholder")}
              disabled={editing}
              autoFocus={!editing}
            />
          </Field>
          <Field label={t("combos.displayName")}>
            <Input
              value={form.display_name}
              onChange={(event) => setForm((current) => ({ ...current, display_name: event.target.value }))}
              placeholder={t("combos.displayNamePlaceholder")}
            />
          </Field>
        </div>

        <div className="cb-form-toggles">
          <div className="cb-form-toggle">
            <Toggle
              label={t("combos.form.enabled")}
              checked={form.enabled}
              onChange={(event) => setForm((current) => ({ ...current, enabled: event.target.checked }))}
            />
            <small>{t("combos.form.enabledHint")}</small>
          </div>
          <div className="cb-form-toggle">
            <Toggle
              label={t("combos.rewriteResponseModel")}
              checked={form.rewrite_response_model}
              onChange={(event) => setForm((current) => ({ ...current, rewrite_response_model: event.target.checked }))}
            />
            <small>{t("combos.form.rewriteHint")}</small>
          </div>
        </div>

        <section className="cb-form-steps" aria-label={t("combos.fallbackOrder")}>
          <header className="cb-form-steps-head">
            <div>
              <strong>{t("combos.fallbackOrder")}</strong>
              <small>{t("combos.form.orderHint")}</small>
            </div>
            <ToolButton icon="add" onClick={addItem} disabled={!modelOptions.length}>
              {t("combos.addStep")}
            </ToolButton>
          </header>

          {modelOptions.length === 0 ? (
            <PageNote icon="info">{t("combos.noModelsForStep")}</PageNote>
          ) : form.items.length === 0 ? (
            <button type="button" className="cb-form-empty" onClick={addItem}>
              <span className="material-symbols-outlined" aria-hidden>add</span>
              {t("combos.noStepsYet")}
            </button>
          ) : (
            <ol className="cb-form-list">
              {form.items.map((item, index) => {
                const unknown = Boolean(item.public_model_id) && !knownValues.has(item.public_model_id);
                const off = unknown && Boolean(disabledTargets?.has(item.public_model_id));
                return (
                  <li className={cn("cb-form-step", index === 0 && "is-primary")} key={`${index}-${item.public_model_id}`}>
                    <span className="cb-form-rank">{index + 1}</span>
                    <div className="cb-form-select">
                      <Select
                        value={item.public_model_id}
                        onChange={(event) => updateItem(index, { public_model_id: event.target.value })}
                        aria-label={t("combos.form.stepModel", { index: index + 1 })}
                      >
                        {unknown ? (
                          <option value={item.public_model_id}>
                            {t(off ? "combos.form.offTarget" : "combos.form.unknownTarget", { id: item.public_model_id })}
                          </option>
                        ) : null}
                        {groupedOptions.map((group) => (
                          <optgroup key={group} label={t(`combos.optionGroup.${group}`)}>
                            {modelOptions
                              .filter((option) => option.group === group)
                              .map((option) => (
                                <option key={option.value} value={option.value}>
                                  {option.label}
                                </option>
                              ))}
                          </optgroup>
                        ))}
                      </Select>
                      {index === 0 ? <small>{t("combos.form.firstStep")}</small> : null}
                      {unknown ? (
                        <small className={off ? "cb-form-muted" : "cb-form-warn"}>{t(off ? "combos.form.offHint" : "combos.form.unknownHint")}</small>
                      ) : null}
                    </div>
                    <div className="cb-form-actions">
                      <IconAction
                        icon="arrow_upward"
                        label={t("combos.moveUp")}
                        disabled={index === 0}
                        onClick={() => setForm((current) => ({ ...current, items: moveItem(current.items, index, index - 1) }))}
                      />
                      <IconAction
                        icon="arrow_downward"
                        label={t("combos.moveDown")}
                        disabled={index === form.items.length - 1}
                        onClick={() => setForm((current) => ({ ...current, items: moveItem(current.items, index, index + 1) }))}
                      />
                      <IconAction icon="close" danger label={t("combos.removeStep")} onClick={() => removeItem(index)} />
                    </div>
                  </li>
                );
              })}
            </ol>
          )}
        </section>

        {showValidation ? <PageNote icon="error">{t(`combos.validation.${validationError}`)}</PageNote> : null}
      </form>
    </Modal>
  );
}
