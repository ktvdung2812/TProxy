import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Field, Input, Modal, Select, Toggle } from "../ui";
import { modelOptionGroupLabel } from "../../lib/modelOptions";
import type { ModelOption } from "../../lib/modelOptions";
import type { ComboFormData } from "./types";
import { COMBO_ID_REGEX, emptyComboForm, validateComboForm } from "./utils";

type Props = {
  open: boolean;
  editing: boolean;
  initial: ComboFormData | null;
  modelOptions: ModelOption[];
  existingIds: string[];
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

export function ComboFormModal({
  open,
  editing,
  initial,
  modelOptions,
  existingIds,
  saving,
  onClose,
  onSubmit,
}: Props) {
  const { t } = useTranslation();
  const [form, setForm] = useState<ComboFormData>(emptyComboForm());
  const [idError, setIdError] = useState("");

  useEffect(() => {
    if (!open) return;
    setForm(initial || emptyComboForm());
    setIdError("");
  }, [open, initial]);

  const groupedOptions = useMemo(() => {
    const groups: ModelOption["group"][] = [];
    for (const option of modelOptions) {
      if (!groups.includes(option.group)) groups.push(option.group);
    }
    return groups;
  }, [modelOptions]);

  const validationError = validateComboForm(form, existingIds, editing);

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
    const firstModel = modelOptions[0]?.value || "";
    setForm((current) => ({
      ...current,
      items: [...current.items, { public_model_id: firstModel }],
    }));
  };

  const updateItem = (index: number, patch: Partial<ComboFormData["items"][number]>) => {
    setForm((current) => ({
      ...current,
      items: current.items.map((item, itemIndex) => (itemIndex === index ? { ...item, ...patch } : item)),
    }));
  };

  const removeItem = (index: number) => {
    setForm((current) => ({
      ...current,
      items: current.items.filter((_, itemIndex) => itemIndex !== index),
    }));
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={editing ? t("combos.editCombo") : t("combos.createCombo")}
      subtitle={t("combos.comboSubtitle")}
      icon="layers"
      size="lg"
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            icon="save"
            disabled={saving || !!validationError || !!idError}
            onClick={() => onSubmit(form)}
          >
            {saving ? t("combos.saving") : editing ? t("combos.saveChanges") : t("combos.createCombo")}
          </Button>
        </>
      }
    >
      <form
        className="combo-form"
        onSubmit={(event) => {
          event.preventDefault();
          if (!validationError && !idError) onSubmit(form);
        }}
      >
        <div className="combo-form-grid">
          <Field label={t("combos.comboId")} hint={t("combos.comboIdHint")}>
            <Input
              value={form.id}
              onChange={(event) => handleIdChange(event.target.value)}
              placeholder="claude-code-fallback"
              disabled={editing}
            />
            {idError ? <p className="field-error">{idError}</p> : null}
          </Field>
          <Field label={t("combos.displayName")}>
            <Input
              value={form.display_name}
              onChange={(event) => setForm((current) => ({ ...current, display_name: event.target.value }))}
              placeholder="Claude Code Fallback"
            />
          </Field>
        </div>

        <div className="combo-form-toggles">
          <Toggle
            label="Enabled"
            checked={form.enabled}
            onChange={(event) => setForm((current) => ({ ...current, enabled: event.target.checked }))}
          />
          <Toggle
            label="Rewrite response model"
            checked={form.rewrite_response_model}
            onChange={(event) =>
              setForm((current) => ({ ...current, rewrite_response_model: event.target.checked }))
            }
          />
        </div>

        <div className="combo-form-items">
          <div className="combo-form-items-head">
            <p className="combo-form-section-title">Fallback order</p>
            <Button type="button" variant="outline" size="sm" icon="add" onClick={addItem} disabled={!modelOptions.length}>
              Add step
            </Button>
          </div>

          {modelOptions.length === 0 ? (
            <p className="combo-form-empty">Create a virtual model or configure mapping targets first.</p>
          ) : form.items.length === 0 ? (
            <p className="combo-form-empty">No steps yet. Add models in priority order.</p>
          ) : (
            <div className="combo-form-item-list">
              {form.items.map((item, index) => (
                <div className="combo-form-item" key={`${item.public_model_id}-${index}`}>
                    <span className="combo-form-item-index">{index + 1}</span>
                    <div className="combo-form-item-fields">
                      <Field label="Model">
                        <Select
                          value={item.public_model_id}
                          onChange={(event) => updateItem(index, { public_model_id: event.target.value })}
                        >
                          {groupedOptions.map((group) => (
                            <optgroup key={group} label={modelOptionGroupLabel(group)}>
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
                      </Field>
                    </div>
                    <div className="combo-form-item-actions">
                      <button
                        type="button"
                        className="combo-form-icon-btn"
                        disabled={index === 0}
                        onClick={() => setForm((current) => ({ ...current, items: moveItem(current.items, index, index - 1) }))}
                        aria-label="Move up"
                      >
                        <span className="material-symbols-outlined">arrow_upward</span>
                      </button>
                      <button
                        type="button"
                        className="combo-form-icon-btn"
                        disabled={index === form.items.length - 1}
                        onClick={() => setForm((current) => ({ ...current, items: moveItem(current.items, index, index + 1) }))}
                        aria-label="Move down"
                      >
                        <span className="material-symbols-outlined">arrow_downward</span>
                      </button>
                      <button
                        type="button"
                        className="combo-form-icon-btn combo-form-icon-btn-danger"
                        onClick={() => removeItem(index)}
                        aria-label="Remove step"
                      >
                        <span className="material-symbols-outlined">close</span>
                      </button>
                    </div>
                  </div>
              ))}
            </div>
          )}
        </div>
      </form>
    </Modal>
  );
}
