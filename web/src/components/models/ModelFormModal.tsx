import { useEffect, useState } from "react";
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
      setIdError("Model ID is required");
      return;
    }
    if (!MODEL_ID_REGEX.test(value.trim())) {
      setIdError("Only letters, numbers, -, _, and . allowed");
      return;
    }
    if (!editing && existingIds.includes(value.trim())) {
      setIdError("This ID is already in use");
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
      title={editing ? "Edit virtual model" : "Create virtual model"}
      subtitle="Expose one stable model ID to clients. Configure provider priority separately."
      icon="apps"
      size="md"
      className="model-form-modal"
      footer={
        <>
          <Button variant="ghost" size="sm" onClick={onClose} disabled={saving}>
            Cancel
          </Button>
          <Button
            variant="primary"
            size="sm"
            icon="save"
            loading={saving}
            disabled={!!validationError || !!idError}
            onClick={() => onSubmit(form)}
          >
            {editing ? "Save changes" : "Create model"}
          </Button>
        </>
      }
    >
      <div className="model-form">
        <div className="model-form-grid">
          <Field label="Model ID" hint="Stable ID clients call, e.g. td-coder-pro">
            <Input
              value={form.id}
              disabled={editing}
              onChange={(event) => handleIdChange(event.target.value)}
              placeholder="td-coder-pro"
            />
            {idError ? <p className="field-error">{idError}</p> : null}
          </Field>
          <Field label="Display name">
            <Input
              value={form.display_name}
              onChange={(event) => setForm((current) => ({ ...current, display_name: event.target.value }))}
              placeholder="TD Coder Pro"
            />
          </Field>
        </div>

        <Field label="Aliases" hint="Comma-separated alternate IDs, e.g. coder, gpt-latest">
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
            label="Enabled"
          />
          <Toggle
            checked={form.rewrite_response_model}
            onChange={(event) =>
              setForm((current) => ({ ...current, rewrite_response_model: event.target.checked }))
            }
            label="Rewrite response model"
          />
        </div>

        <Field label="Capabilities">
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
