import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Modal } from "../ui";
import { BulkBar, BulkButton } from "../page-kit";
import type { ModelOption } from "../../lib/modelOptions";
import { ModelTargetCombobox } from "./ModelTargetCombobox";

/**
 * Floating "unsaved changes" bar for the active editor. It shares the bottom
 * dock with the bulk bar so the two stack instead of covering each other.
 */
export function SaveBar({
  count,
  sectionLabel,
  otherDirty,
  saving,
  onDiscard,
  onSave,
}: {
  count: number;
  sectionLabel: string;
  /** Other sections that also hold unsaved edits, e.g. "Cursor (2)". */
  otherDirty: string[];
  saving: boolean;
  onDiscard: () => void;
  onSave: () => void;
}) {
  const { t } = useTranslation();
  if (count === 0) return null;
  return (
    <div className="cp-bulkbar mp-savebar" role="region" aria-label={t("mapping.save.region")}>
      <span className="material-symbols-outlined mp-savebar-icon" aria-hidden>edit_note</span>
      <div className="mp-savebar-copy">
        <strong>{t("mapping.save.unsaved", { count })}</strong>
        <small>
          {sectionLabel}
          {otherDirty.length ? ` · ${t("mapping.save.alsoUnsaved", { list: otherDirty.join(", ") })}` : ""}
        </small>
      </div>
      <span className="cp-bulkbar-divider" aria-hidden />
      <div className="cp-bulkbar-actions mp-savebar-actions">
        <BulkButton icon="undo" disabled={saving} onClick={onDiscard}>
          {t("mapping.save.discard")}
        </BulkButton>
        <Button variant="primary" size="sm" icon="save" loading={saving} onClick={onSave} title={t("mapping.save.shortcut")}>
          {saving ? t("mapping.saving") : t("mapping.save.save")}
        </Button>
      </div>
    </div>
  );
}

export function PairBulkBar({
  count,
  visibleCount,
  allVisibleSelected,
  onSelectAll,
  onClear,
  onSetTarget,
  onRemove,
}: {
  count: number;
  visibleCount: number;
  allVisibleSelected: boolean;
  onSelectAll: () => void;
  onClear: () => void;
  onSetTarget: () => void;
  onRemove: () => void;
}) {
  const { t } = useTranslation();
  return (
    <BulkBar
      count={count}
      onClear={onClear}
      selectAll={allVisibleSelected ? null : { label: t("mapping.selectAllVisible", { count: visibleCount }), onSelect: onSelectAll }}
    >
      <BulkButton icon="alt_route" onClick={onSetTarget}>
        {t("mapping.bulk.setTarget")}
      </BulkButton>
      <BulkButton icon="delete" tone="danger" onClick={onRemove}>
        {t("mapping.bulk.remove")}
      </BulkButton>
    </BulkBar>
  );
}

/** Points every selected source at one target; applied locally until Save. */
export function BulkTargetModal({
  open,
  count,
  options,
  onApply,
  onClose,
}: {
  open: boolean;
  count: number;
  options: ModelOption[];
  onApply: (target: string) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [target, setTarget] = useState("");
  const close = () => {
    setTarget("");
    onClose();
  };
  return (
    <Modal
      open={open}
      onClose={close}
      title={t("mapping.bulk.setTargetTitle", { count })}
      subtitle={t("mapping.bulk.setTargetHint")}
      icon="alt_route"
      size="sm"
      className="mp-bulk-modal"
      footer={
        <>
          <Button variant="secondary" onClick={close}>{t("mapping.cancel")}</Button>
          <Button
            variant="primary"
            icon="check"
            disabled={!target.trim()}
            onClick={() => {
              onApply(target.trim());
              setTarget("");
            }}
          >
            {t("mapping.bulk.apply", { count })}
          </Button>
        </>
      }
    >
      <div className="mp-field">
        <span>{t("mapping.columns.target")}</span>
        <ModelTargetCombobox
          aria-label={t("mapping.columns.target")}
          value={target}
          placeholder={t("mapping.virtualModelOrProvider")}
          options={options}
          onChange={setTarget}
        />
      </div>
    </Modal>
  );
}
