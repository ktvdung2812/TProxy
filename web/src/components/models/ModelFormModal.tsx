import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Field, Input, Modal, Toggle } from "../ui";
import type { ModelFormData } from "./types";
import { CAPABILITY_OPTIONS, MODEL_ID_REGEX, emptyModelForm, validateModelForm } from "./utils";

type Props = {
  open: boolean;
  editing: boolean;
  initial: ModelFormData | null;
  existingIds: string[];
  saving: boolean;
  onClose: () => void;
  onSubmit: (form: ModelFormData) => void;
};

export function ModelFormModal({
  open,
  editing,
  initial,
  existingIds,
  saving,
  onClose,
  onSubmit,
}: Props) {
  const { t } = useTranslation();
  const [form, setForm] = useState<ModelFormData>(emptyModelForm());
  const [idError, setIdError] = useState("");

  useEffect(() => {
    if (!open) return;
    setForm(initial || emptyModelForm());
    setIdError("");
  }, [open, initial]);

  const validationError = validateModelForm(form, existingIds, editing);

  const handleIdChange = (value: string) => {
    setForm((current) => ({ ...current, id: value }));
    if (!value.trim()) {
      setIdError(t("models.form.idRequired"));
      return;
    }
    if (!MODEL_ID_REGEX.test(value.trim())) {
      setIdError(t("models.form.idAllowed"));
      return;
    }
    if (!editing && existingIds.includes(value.trim())) {
      setIdError(t("models.form.idInUse"));
      return;
    }
    setIdError("");
  };

  const toggleCapability = (capability: string) => {
    setForm((current) => {
      const enabled = current.capabilities.includes(capability);
      return {
        ...current,
        capabilities: enabled
          ? current.capabilities.filter((item) => item !== capability)
          : [...current.capabilities, capability],
      };
    });
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={editing ? t("models.form.editTitle") : t("models.form.createTitle")}
      subtitle={t("models.form.subtitle")}
      icon="apps"
      size="md"
      className="model-form-modal"
      footer={
        <>
          <Button variant="ghost" size="sm" onClick={onClose} disabled={saving}>
            {t("common.cancel")}
          </Button>
          <Button
            variant="primary"
            size="sm"
            icon="save"
            loading={saving}
            disabled={!!validationError || !!idError}
            onClick={() => onSubmit(form)}
          >
            {editing ? t("models.form.saveChanges") : t("models.createModel")}
          </Button>
        </>
      }
    >
      <div className="model-form">
        <div className="model-form-grid">
          <Field label={t("models.modelId")} hint={editing ? t("models.form.idLocked") : t("models.form.idHint")}>
            <Input
              value={form.id}
              disabled={editing}
              onChange={(event) => handleIdChange(event.target.value)}
              placeholder="td-coder-pro"
            />
            {idError ? <p className="field-error">{idError}</p> : null}
          </Field>
          <Field label={t("models.displayName")}>
            <Input
              value={form.display_name}
              onChange={(event) => setForm((current) => ({ ...current, display_name: event.target.value }))}
              placeholder="TD Coder Pro"
            />
          </Field>
        </div>

        <Field label={t("models.aliases")} hint={t("models.form.aliasesHint")}>
          <Input
            value={form.aliases}
            onChange={(event) => setForm((current) => ({ ...current, aliases: event.target.value }))}
            placeholder="coder, gpt-latest"
          />
        </Field>

        <div className="model-form-toggles">
          <Toggle
            checked={form.enabled}
            onChange={(event) => setForm((current) => ({ ...current, enabled: event.target.checked }))}
            label={t("models.form.enabled")}
          />
          <Toggle
            checked={form.rewrite_response_model}
            onChange={(event) =>
              setForm((current) => ({ ...current, rewrite_response_model: event.target.checked }))
            }
            label={t("models.rewriteResponse")}
          />
        </div>

        <Field label={t("models.capabilities")} hint={t("models.form.capabilitiesHint")}>
          <div className="model-form-capabilities">
            {CAPABILITY_OPTIONS.map((capability) => (
              <button
                key={capability}
                type="button"
                className={`model-form-capability${form.capabilities.includes(capability) ? " active" : ""}`}
                onClick={() => toggleCapability(capability)}
              >
                {capability}
              </button>
            ))}
          </div>
        </Field>

        {validationError ? <p className="field-error">{validationError}</p> : null}
      </div>
    </Modal>
  );
}
