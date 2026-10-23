import type { FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Button, Field, Input, Modal, Toggle } from "../ui";
import { ApiModelAccessField } from "./ApiModelAccessField";
import { ApiKeyCredentialAccessField } from "./ApiKeyCredentialAccessField";
import type { ApiKeyCombo, ApiKeyCredential, ApiKeyFormData, ApiKeyProvider, ApiKeyRoute, ApiModelOption } from "./types";

type FormProps = {
  formData: ApiKeyFormData;
  setFormData: (form: ApiKeyFormData) => void;
};

/** Per-key ceilings; 0 means "inherit what the accounts allow". */
function LimitsFields({ formData, setFormData }: FormProps) {
  const { t } = useTranslation();
  const number = (field: keyof ApiKeyFormData, label: string, step?: string) => (
    <Field label={t(label)}>
      <Input
        type="number"
        min={0}
        step={step}
        value={formData[field] as number}
        onChange={(event) => setFormData({ ...formData, [field]: Number(event.target.value) })}
      />
    </Field>
  );
  return (
    <fieldset className="ap-fieldset">
      <legend>{t("apis.rateLimits")}</legend>
      <p className="ap-fieldset-hint">{t("apis.inheritLimitsHint")}</p>
      <div className="ap-field-grid">
        {number("rpm", "apis.requestsPerMin")}
        {number("streams", "apis.concurrentStreams")}
        {number("max_input_bytes", "apis.maxInputBytes")}
        {number("max_output_tokens", "apis.maxOutputTokens")}
        {number("media_jobs", "apis.mediaJobs")}
        {number("budget_usd_per_day", "apis.dailyBudget", "0.01")}
      </div>
    </fieldset>
  );
}

function SwitchRow({ title, hint, checked, onChange }: { title: string; hint: string; checked: boolean; onChange: (value: boolean) => void }) {
  return (
    <div className="ap-switch-row">
      <div>
        <strong>{title}</strong>
        <p>{hint}</p>
      </div>
      <Toggle label="" checked={checked} onChange={(event) => onChange(event.target.checked)} aria-label={title} />
    </div>
  );
}

type CreateProps = FormProps & {
  open: boolean;
  saving: boolean;
  advanced: boolean;
  modelOptions: ApiModelOption[];
  onToggleAdvanced: () => void;
  onClose: () => void;
  onCreate: () => void;
};

export function ApiKeyCreateModal({ open, saving, advanced, formData, setFormData, modelOptions, onToggleAdvanced, onClose, onCreate }: CreateProps) {
  const { t } = useTranslation();
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("apis.createKeyTitle")}
      subtitle={t("apis.createKeySubtitle")}
      icon="vpn_key"
      size="md"
      footer={
        <>
          <Button type="button" variant="secondary" onClick={onClose}>{t("common.cancel")}</Button>
          <Button type="submit" form="ap-create-key" variant="primary" icon="add" loading={saving} disabled={saving || !formData.name.trim()}>
            {t("common.create")}
          </Button>
        </>
      }
    >
      <form
        id="ap-create-key"
        className="ap-modal-form"
        onSubmit={(event) => {
          event.preventDefault();
          onCreate();
        }}
      >
        <Field label={t("apis.keyName")} hint={t("apis.keyNameHint")} required>
          <Input
            autoFocus
            placeholder={t("apis.keyNamePlaceholder")}
            value={formData.name}
            onChange={(event) => setFormData({ ...formData, name: event.target.value })}
            required
          />
        </Field>

        <button type="button" className="ap-advanced-toggle" aria-expanded={advanced} onClick={onToggleAdvanced}>
          <span className="material-symbols-outlined" aria-hidden>{advanced ? "expand_less" : "expand_more"}</span>
          {t("apis.advancedOptions")}
          <small>{t("apis.advancedOptionsHint")}</small>
        </button>

        {advanced ? (
          <>
            <Field label={t("apis.keyId")} hint={t("apis.keyIdHint")}>
              <Input placeholder="my-app-key" value={formData.id} onChange={(event) => setFormData({ ...formData, id: event.target.value })} />
            </Field>
            <Field label={t("apis.allowedModels")} hint={t("apis.modelsHint")}>
              <ApiModelAccessField value={formData.models} modelOptions={modelOptions} onChange={(models) => setFormData({ ...formData, models })} />
            </Field>
            <div className="ap-field-grid">
              <Field label={t("apis.teamId")} hint={t("apis.optional")}>
                <Input placeholder="team-id" value={formData.team} onChange={(event) => setFormData({ ...formData, team: event.target.value })} />
              </Field>
              <Field label={t("apis.allowedEndpoints")} hint={t("apis.allowedEndpointsHint")}>
                <Input
                  placeholder="/v1/chat/completions, /v1/models"
                  value={formData.endpoints}
                  onChange={(event) => setFormData({ ...formData, endpoints: event.target.value })}
                />
              </Field>
            </div>
            <LimitsFields formData={formData} setFormData={setFormData} />
            <SwitchRow
              title={t("apis.modelMappingTitle")}
              hint={t("apis.modelMappingHint")}
              checked={!formData.disable_model_mapping}
              onChange={(checked) => setFormData({ ...formData, disable_model_mapping: !checked })}
            />
          </>
        ) : null}
      </form>
    </Modal>
  );
}

type CreatedProps = {
  secret: string | null;
  copied: string | null;
  onCopy: (text: string, id: string) => void;
  onClose: () => void;
};

export function ApiKeyCreatedModal({ secret, copied, onCopy, onClose }: CreatedProps) {
  const { t } = useTranslation();
  const isCopied = copied === "created_key";
  return (
    <Modal
      open={Boolean(secret)}
      onClose={onClose}
      title={t("apis.keyCreatedTitle")}
      icon="key"
      size="md"
      closeOnBackdrop={false}
      footer={<Button variant="primary" onClick={onClose}>{t("apis.done")}</Button>}
    >
      <div className="ap-created">
        <div className="ap-warning cp-tone-warning" role="note">
          <span className="material-symbols-outlined" aria-hidden>warning</span>
          <p>
            <strong>{t("apis.saveKeyNow")}</strong> {t("apis.saveKeyDesc")}
          </p>
        </div>
        <button type="button" className="ap-secret-box" onClick={() => secret && onCopy(secret, "created_key")}>
          <code>{secret}</code>
          <span className="cp-tool-btn">
            <span className="material-symbols-outlined" aria-hidden>{isCopied ? "check" : "content_copy"}</span>
            <span>{isCopied ? t("common.copied") : t("common.copy")}</span>
          </span>
        </button>
        <p className="ap-fieldset-hint">{t("apis.secretKeptInBrowser")}</p>
      </div>
    </Modal>
  );
}

type EditProps = FormProps & {
  open: boolean;
  saving: boolean;
  keyId: string;
  editSecret: string;
  setEditSecret: (value: string) => void;
  modelOptions: ApiModelOption[];
  providers: ApiKeyProvider[];
  credentials: Record<string, ApiKeyCredential[]>;
  routes: Record<string, ApiKeyRoute[]>;
  combos: ApiKeyCombo[];
  onClose: () => void;
  onSubmit: (event: FormEvent) => void;
};

export function ApiKeyEditModal({
  open,
  saving,
  keyId,
  formData,
  setFormData,
  editSecret,
  setEditSecret,
  modelOptions,
  providers,
  credentials,
  routes,
  combos,
  onClose,
  onSubmit,
}: EditProps) {
  const { t } = useTranslation();
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("apis.editKeyTitle")}
      subtitle={keyId}
      icon="tune"
      size="lg"
      className="api-key-edit-modal"
      footer={
        <div className="api-key-edit-footer">
          <Button type="button" variant="secondary" onClick={onClose}>{t("common.cancel")}</Button>
          <Button type="submit" form="api-key-edit-form" variant="primary" icon="save" loading={saving} disabled={saving || !formData.name.trim()}>
            {t("apis.saveChanges")}
          </Button>
        </div>
      }
    >
      <form id="api-key-edit-form" className="api-key-edit-form" onSubmit={onSubmit}>
        <section className="api-key-edit-pane api-key-edit-info custom-scrollbar">
          <div className="api-key-edit-section-head">
            <span className="material-symbols-outlined">key</span>
            <div>
              <h4>{t("apis.apiInformation")}</h4>
              <p>{t("apis.apiInformationHint")}</p>
            </div>
          </div>

          <div className="ap-modal-form">
            <Field label={t("apis.keyName")} required>
              <Input value={formData.name} onChange={(event) => setFormData({ ...formData, name: event.target.value })} required />
            </Field>
            <Field label={t("apis.apiKeySecret")} hint={t("apis.apiKeySecretHint")}>
              <Input
                type="password"
                autoComplete="off"
                placeholder={t("apis.secretPlaceholder")}
                value={editSecret}
                onChange={(event) => setEditSecret(event.target.value)}
              />
            </Field>
            <div className="ap-field-grid">
              <Field label={t("apis.teamId")} hint={t("apis.optional")}>
                <Input value={formData.team} onChange={(event) => setFormData({ ...formData, team: event.target.value })} />
              </Field>
              <Field label={t("apis.allowedEndpoints")} hint={t("apis.allowedEndpointsHint")}>
                <Input value={formData.endpoints} onChange={(event) => setFormData({ ...formData, endpoints: event.target.value })} />
              </Field>
            </div>
            <LimitsFields formData={formData} setFormData={setFormData} />
            <SwitchRow
              title={t("apis.keyEnabled")}
              hint={t("apis.keyEnabledHint")}
              checked={formData.enabled}
              onChange={(checked) => setFormData({ ...formData, enabled: checked })}
            />
            <SwitchRow
              title={t("apis.modelMappingTitle")}
              hint={t("apis.modelMappingHint")}
              checked={!formData.disable_model_mapping}
              onChange={(checked) => setFormData({ ...formData, disable_model_mapping: !checked })}
            />
          </div>
        </section>

        <section className="api-key-edit-pane api-key-edit-models">
          <div className="api-key-edit-section-head">
            <span className="material-symbols-outlined">view_in_ar</span>
            <div>
              <h4>{t("apis.modelAccess")}</h4>
              <p>{t("apis.modelAccessHint")}</p>
            </div>
          </div>
          <Field hint={t("apis.modelsHint")}>
            <ApiModelAccessField value={formData.models} modelOptions={modelOptions} onChange={(models) => setFormData({ ...formData, models })} />
          </Field>
          <ApiKeyCredentialAccessField
            value={formData.credential_ids}
            models={formData.models}
            providers={providers}
            credentials={credentials}
            routes={routes}
            combos={combos}
            onChange={(credentialIDs) => setFormData({ ...formData, credential_ids: credentialIDs })}
          />
        </section>
      </form>
    </Modal>
  );
}
