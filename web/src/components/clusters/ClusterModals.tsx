import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";
import { Button, Field, Input, Modal } from "../ui";
import type { ClusterNode, CreatedJoinToken, CreateTokenBody, NodePatch } from "./types";
import { fmtDateTime, toCount } from "./utils";

/* ------------------------------------------------------------
   Edit a peer's name/owner (hub), or rename this node.
   ------------------------------------------------------------ */
export type NodeEditTarget = { mode: "peer"; node: ClusterNode } | { mode: "self"; name: string };

export function NodeEditModal({
  target,
  saving,
  onSave,
  onClose,
}: {
  target: NodeEditTarget | null;
  saving: boolean;
  onSave: (patch: NodePatch) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState("");
  const [owner, setOwner] = useState("");

  useEffect(() => {
    if (!target) return;
    setName(target.mode === "peer" ? target.node.name : target.name);
    setOwner(target.mode === "peer" ? target.node.owner : "");
  }, [target]);

  const peer = target?.mode === "peer" ? target.node : null;
  const canSave = Boolean(name.trim()) && !saving;
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!canSave) return;
    onSave(peer ? { name: name.trim(), owner: owner.trim() } : { name: name.trim() });
  };

  return (
    <Modal
      open={Boolean(target)}
      onClose={() => {
        if (!saving) onClose();
      }}
      icon={peer ? "edit" : "badge"}
      title={peer ? t("clusters.nodeModal.editTitle") : t("clusters.nodeModal.renameTitle")}
      subtitle={peer ? t("clusters.nodeModal.editSubtitle", { id: peer.id }) : t("clusters.nodeModal.renameSubtitle")}
      size="sm"
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={saving}>{t("common.cancel")}</Button>
          <Button type="submit" form="cl-node-form" loading={saving} disabled={!canSave}>{t("common.save")}</Button>
        </>
      }
    >
      <form id="cl-node-form" className="cl-form" onSubmit={submit}>
        <Field label={t("clusters.nodeModal.name")} required>
          <Input value={name} autoFocus maxLength={80} onChange={(event) => setName(event.target.value)} placeholder={t("clusters.setup.nodeNamePlaceholder")} />
        </Field>
        {peer ? (
          <Field label={t("clusters.nodeModal.owner")} hint={t("clusters.nodeModal.ownerHint")}>
            <Input value={owner} maxLength={80} onChange={(event) => setOwner(event.target.value)} placeholder={t("clusters.setup.ownerPlaceholder")} />
          </Field>
        ) : null}
      </form>
    </Modal>
  );
}

/* ------------------------------------------------------------
   Create a join token
   ------------------------------------------------------------ */
const DEFAULT_TOKEN: CreateTokenBody = { label: "", ttl_hours: 24, max_uses: 1 };

export function TokenCreateModal({
  open,
  saving,
  onCreate,
  onClose,
}: {
  open: boolean;
  saving: boolean;
  onCreate: (body: CreateTokenBody) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [form, setForm] = useState<CreateTokenBody>(DEFAULT_TOKEN);

  useEffect(() => {
    if (open) setForm(DEFAULT_TOKEN);
  }, [open]);

  const canCreate = form.ttl_hours >= 1 && form.max_uses >= 1 && !saving;
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (canCreate) onCreate({ ...form, label: form.label.trim() });
  };

  return (
    <Modal
      open={open}
      onClose={() => {
        if (!saving) onClose();
      }}
      icon="key"
      title={t("clusters.tokens.createTitle")}
      subtitle={t("clusters.tokens.createSubtitle")}
      size="sm"
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={saving}>{t("common.cancel")}</Button>
          <Button type="submit" form="cl-token-form" icon="add" loading={saving} disabled={!canCreate}>{t("clusters.tokens.create")}</Button>
        </>
      }
    >
      <form id="cl-token-form" className="cl-form" onSubmit={submit}>
        <Field label={t("clusters.tokens.label")} hint={t("clusters.tokens.labelHint")}>
          <Input
            value={form.label}
            autoFocus
            maxLength={80}
            onChange={(event) => setForm((current) => ({ ...current, label: event.target.value }))}
            placeholder={t("clusters.tokens.labelPlaceholder")}
          />
        </Field>
        <div className="cl-field-grid">
          <Field label={t("clusters.tokens.ttl")} hint={t("clusters.tokens.ttlHint")} required>
            <Input
              type="number"
              min={1}
              step={1}
              inputMode="numeric"
              value={form.ttl_hours}
              onChange={(event) => setForm((current) => ({ ...current, ttl_hours: toCount(event.target.value) }))}
            />
          </Field>
          <Field label={t("clusters.tokens.maxUses")} hint={t("clusters.tokens.maxUsesHint")} required>
            <Input
              type="number"
              min={1}
              step={1}
              inputMode="numeric"
              value={form.max_uses}
              onChange={(event) => setForm((current) => ({ ...current, max_uses: toCount(event.target.value) }))}
            />
          </Field>
        </div>
      </form>
    </Modal>
  );
}

/* ------------------------------------------------------------
   Show-once: the token and its join command
   ------------------------------------------------------------ */
function SecretBox({ value, copyId, copied, onCopy }: { value: string; copyId: string; copied: string | null; onCopy: (text: string, id: string) => void }) {
  const { t } = useTranslation();
  const isCopied = copied === copyId;
  return (
    <button type="button" className="cl-secret" onClick={() => onCopy(value, copyId)}>
      <code>{value}</code>
      <span className="cp-tool-btn">
        <span className="material-symbols-outlined" aria-hidden>{isCopied ? "check" : "content_copy"}</span>
        <span>{isCopied ? t("common.copied") : t("common.copy")}</span>
      </span>
    </button>
  );
}

export function TokenCreatedModal({ created, onClose }: { created: CreatedJoinToken | null; onClose: () => void }) {
  const { t, i18n } = useTranslation();
  const { copied, copy } = useCopyToClipboard();
  const expires = created ? fmtDateTime(created.expires_at, i18n.language) : "";
  return (
    <Modal
      open={Boolean(created)}
      onClose={onClose}
      icon="key"
      title={t("clusters.tokens.createdTitle")}
      size="md"
      closeOnBackdrop={false}
      footer={<Button variant="primary" onClick={onClose}>{t("clusters.tokens.done")}</Button>}
    >
      {created ? (
        <div className="cl-created">
          <div className="cl-notice cp-tone-warning" role="note">
            <span className="material-symbols-outlined" aria-hidden>warning</span>
            <p>
              <strong>{t("clusters.tokens.saveNow")}</strong> {t("clusters.tokens.saveNowDesc")}
            </p>
          </div>
          <Field label={t("clusters.tokens.tokenLabel")}>
            <SecretBox value={created.token} copyId="token" copied={copied} onCopy={copy} />
          </Field>
          {created.command ? (
            <Field label={t("clusters.tokens.commandLabel")} hint={t("clusters.tokens.commandHint")}>
              <SecretBox value={created.command} copyId="command" copied={copied} onCopy={copy} />
            </Field>
          ) : null}
          {expires ? <p className="cl-muted">{t("clusters.tokens.expiresAt", { time: expires })}</p> : null}
        </div>
      ) : null}
    </Modal>
  );
}
