import { useMemo, type Dispatch, type SetStateAction } from "react";
import { useTranslation } from "react-i18next";
import { Button, Field, Input, Modal, Textarea, Toggle } from "../ui";
import { PageNote } from "../page-kit";
import type { ProxyPoolRow } from "./api";
import { parseProxyLine, suggestPoolId } from "./utils";

export type ProxyPoolFormData = {
  id: string;
  name: string;
  url: string;
  enabled: boolean;
};

export function normalizeFormData(pool?: ProxyPoolRow): ProxyPoolFormData {
  return {
    id: pool?.id || "",
    name: pool?.name || "",
    // Stored URLs are encrypted and redacted; editing only replaces them.
    url: "",
    enabled: pool?.enabled !== false,
  };
}

export function ProxyPoolFormModal({
  open,
  editingPool,
  formData,
  setFormData,
  saving,
  onSave,
  onClose,
}: {
  open: boolean;
  editingPool: ProxyPoolRow | null;
  formData: ProxyPoolFormData;
  setFormData: Dispatch<SetStateAction<ProxyPoolFormData>>;
  saving: boolean;
  onSave: () => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const canSave = Boolean(formData.name.trim()) && (Boolean(editingPool) || (Boolean(formData.url.trim()) && Boolean(formData.id.trim())));
  return (
    <Modal
      open={open}
      onClose={onClose}
      icon={editingPool ? "edit" : "add"}
      title={editingPool ? t("proxyPools.editProxyPool") : t("proxyPools.addProxyPoolTitle")}
      subtitle={editingPool ? t("proxyPools.form.editSubtitle", { id: editingPool.id }) : t("proxyPools.form.createSubtitle")}
      size="md"
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={saving}>{t("proxyPools.cancel")}</Button>
          <Button onClick={onSave} loading={saving} disabled={!canSave}>
            {editingPool ? t("proxyPools.save") : t("proxyPools.form.create")}
          </Button>
        </>
      }
    >
      <form
        className="proxy-pools-form pp-form"
        onSubmit={(event) => {
          event.preventDefault();
          if (canSave && !saving) onSave();
        }}
      >
        <Field label={t("proxyPools.name")} required>
          <Input
            value={formData.name}
            autoFocus
            onChange={(event) => {
              const name = event.target.value;
              setFormData((prev) => ({
                ...prev,
                name,
                id: !editingPool && (!prev.id || prev.id === suggestPoolId(prev.name)) ? suggestPoolId(name) : prev.id,
              }));
            }}
            placeholder={t("proxyPools.namePlaceholder")}
            required
          />
        </Field>
        {!editingPool ? (
          <Field label={t("proxyPools.poolId")} required hint={t("proxyPools.poolIdHint")}>
            <Input
              value={formData.id}
              onChange={(event) => setFormData((prev) => ({ ...prev, id: event.target.value }))}
              placeholder={t("proxyPools.poolIdPlaceholder")}
              required
            />
          </Field>
        ) : null}
        <Field
          label={t("proxyPools.proxyUrl")}
          required={!editingPool}
          hint={editingPool ? t("proxyPools.proxyUrlHintEdit") : t("proxyPools.proxyUrlHintCreate")}
        >
          <Input
            type="password"
            autoComplete="off"
            value={formData.url}
            onChange={(event) => setFormData((prev) => ({ ...prev, url: event.target.value }))}
            placeholder={editingPool ? t("proxyPools.proxyUrlPlaceholderEdit") : t("proxyPools.proxyUrlPlaceholderCreate")}
          />
        </Field>
        <div className="proxy-pools-toggle-row">
          <div>
            <strong>{t("proxyPools.activeField")}</strong>
            <p>{t("proxyPools.activeFieldHint")}</p>
          </div>
          <Toggle
            checked={formData.enabled}
            onChange={(event) => setFormData((prev) => ({ ...prev, enabled: event.target.checked }))}
            disabled={saving}
            aria-label={t("proxyPools.activeField")}
          />
        </div>
        {/* Lets Enter submit the form without a visible duplicate button. */}
        <button type="submit" hidden aria-hidden tabIndex={-1} />
      </form>
    </Modal>
  );
}

export function ProxyPoolImportModal({
  open,
  text,
  setText,
  importing,
  onImport,
  onClose,
}: {
  open: boolean;
  text: string;
  setText: (value: string) => void;
  importing: boolean;
  onImport: () => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  // Same parser as the import itself, so the preview never disagrees with the result.
  const preview = useMemo(() => {
    let valid = 0;
    let invalid = 0;
    for (const line of text.split(/\r?\n/)) {
      if (!line.trim()) continue;
      try {
        if (parseProxyLine(line)) valid += 1;
      } catch {
        invalid += 1;
      }
    }
    return { valid, invalid };
  }, [text]);

  return (
    <Modal
      open={open}
      onClose={() => {
        if (!importing) onClose();
      }}
      icon="upload"
      title={t("proxyPools.batchImportTitle")}
      subtitle={t("proxyPools.form.importSubtitle")}
      size="md"
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={importing}>{t("proxyPools.cancel")}</Button>
          <Button onClick={onImport} loading={importing} disabled={!text.trim()}>
            {preview.valid > 0 ? t("proxyPools.form.importCount", { count: preview.valid }) : t("proxyPools.import")}
          </Button>
        </>
      }
    >
      <div className="pp-form">
        <Field label={t("proxyPools.pasteProxyList")} hint={t("proxyPools.pasteProxyListHint")}>
          <Textarea
            value={text}
            onChange={(event) => setText(event.target.value)}
            placeholder={t("proxyPools.pasteProxyPlaceholder")}
            rows={8}
            spellCheck={false}
          />
        </Field>
        {text.trim() ? (
          <PageNote icon={preview.invalid > 0 ? "warning" : "check_circle"}>
            {preview.invalid > 0
              ? t("proxyPools.form.previewInvalid", { valid: preview.valid, invalid: preview.invalid })
              : t("proxyPools.form.previewValid", { count: preview.valid })}
          </PageNote>
        ) : null}
      </div>
    </Modal>
  );
}
