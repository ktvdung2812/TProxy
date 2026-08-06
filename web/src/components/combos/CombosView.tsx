import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";
import { Badge, Button, Card, ConfirmDialog, EmptyState } from "../ui";
import { deleteCombo, saveCombo } from "./api";
import { ComboFormModal } from "./ComboFormModal";
import type { ModelOption } from "../../lib/modelOptions";
import type { ComboFormData, ComboModelOption, ComboRecord } from "./types";
import {
  buildComboPresets,
  clientLabel,
  comboToForm,
  emptyComboForm,
  presetToForm,
} from "./utils";

type Props = {
  secret: string;
  combos: ComboRecord[];
  models: ComboModelOption[];
  modelOptions: ModelOption[];
  onMutated?: () => void;
  onNotice?: (message: string) => void;
  onError?: (message: string) => void;
};

type ConfirmState = {
  title: string;
  message: string;
  confirmText?: string;
  onConfirm: () => void;
};

export function CombosView({ secret, combos, models, modelOptions, onMutated, onNotice, onError }: Props) {
  const { t } = useTranslation();
  const { copied, copy } = useCopyToClipboard();
  const [showFormModal, setShowFormModal] = useState(false);
  const [editingCombo, setEditingCombo] = useState<ComboRecord | null>(null);
  const [formSeed, setFormSeed] = useState<ComboFormData | null>(null);
  const [saving, setSaving] = useState(false);
  const [confirmState, setConfirmState] = useState<ConfirmState | null>(null);

  const existingIds = useMemo(() => combos.map((combo) => combo.id), [combos]);
  const presets = useMemo(() => buildComboPresets(models), [models]);

  const openCreate = () => {
    setEditingCombo(null);
    setFormSeed(emptyComboForm());
    setShowFormModal(true);
  };

  const openEdit = (combo: ComboRecord) => {
    setEditingCombo(combo);
    setFormSeed(comboToForm(combo));
    setShowFormModal(true);
  };

  const openPreset = (presetId: string) => {
    const preset = presets.find((item) => item.id === presetId);
    if (!preset) return;
    const form = presetToForm(preset, existingIds);
    if (!form) {
      onError?.(t("combos.noPresetError"));
      return;
    }
    setEditingCombo(null);
    setFormSeed(form);
    setShowFormModal(true);
  };

  const handleSave = async (form: ComboFormData) => {
    setSaving(true);
    try {
      await saveCombo(secret, form, !!editingCombo);
      onNotice?.(editingCombo
        ? t("combos.comboUpdated", { name: form.display_name || form.id })
        : t("combos.comboCreated", { name: form.display_name || form.id }));
      setShowFormModal(false);
      setEditingCombo(null);
      setFormSeed(null);
      onMutated?.();
    } catch (error) {
      onError?.(error instanceof Error ? error.message : t("combos.failedSaveCombo"));
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = (combo: ComboRecord) => {
    setConfirmState({
      title: t("combos.deleteCombo"),
      message: t("combos.deleteComboMessage", { name: combo.display_name || combo.id }),
      confirmText: t("common.delete"),
      onConfirm: () => {
        void (async () => {
          try {
            await deleteCombo(secret, combo.id);
            onNotice?.(t("combos.comboDeleted", { name: combo.display_name || combo.id }));
            onMutated?.();
          } catch (error) {
            onError?.(error instanceof Error ? error.message : t("combos.failedDeleteCombo"));
          }
        })();
      },
    });
  };

  return (
    <section className="section">
      <div className="section-head">
        <div>
          <p className="eyebrow">{t("combos.eyebrow")}</p>
          <h2>{t("combos.title")}</h2>
          <p>{t("combos.description")}</p>
        </div>
        <div className="combos-head-actions">
          <span className="meta">{t("combos.policiesCount", { count: combos.length })}</span>
          <Button variant="primary" size="sm" icon="add" onClick={openCreate}>
            {t("combos.createCombo")}
          </Button>
        </div>
      </div>

      <Card className="combos-intro-card" pad="md">
        <div className="combos-intro-copy">
          <h3>{t("combos.howCombosRoute")}</h3>
          <ul>
            <li>
              <strong>{t("combos.fallbackLabel")}</strong> — {t("combos.fallbackDesc")}
            </li>
            <li>
              <strong>{t("combos.claudeCodeLabel")}</strong> — {t("combos.claudeCodeDesc")}
            </li>
            <li>
              <strong>{t("combos.coworkLabel")}</strong> — {t("combos.coworkDesc")}
            </li>
          </ul>
        </div>
        <div className="combos-intro-links">
          <Link to="/cli-tools/claude" className="combos-client-link">
            <span className="material-symbols-outlined">terminal</span>
            {t("combos.claudeCodeSetup")}
          </Link>
          <Link to="/cli-tools/cowork" className="combos-client-link">
            <span className="material-symbols-outlined">groups</span>
            {t("combos.coworkSetup")}
          </Link>
        </div>
      </Card>

      <div className="combos-presets">
        <div className="combos-presets-head">
          <h3>{t("combos.quickPresets")}</h3>
          <p>{t("combos.quickPresetsDesc")}</p>
        </div>
        <div className="combos-presets-grid">
          {presets.map((preset) => (
            <Card key={preset.id} className="combos-preset-card" pad="md">
              <div className="combos-preset-top">
                <Badge size="sm">{clientLabel(preset.client)}</Badge>
              </div>
              <h4>{preset.title}</h4>
              <p>{preset.description}</p>
              <p className="combos-preset-models">
                {preset.modelIds.length
                  ? preset.modelIds.map((modelId, index) => (
                      <span key={modelId}>
                        {index > 0 ? " → " : ""}
                        <code>{modelId}</code>
                      </span>
                    ))
                  : t("combos.noVirtualModels")}
              </p>
              <Button
                variant="outline"
                size="sm"
                icon="auto_awesome"
                disabled={preset.modelIds.length === 0}
                onClick={() => openPreset(preset.id)}
              >
                {t("combos.usePreset")}
              </Button>
            </Card>
          ))}
        </div>
      </div>

      {combos.length === 0 ? (
        <EmptyState
          icon="layers"
          text={t("combos.noCombosConfigured")}
          hint={t("combos.noCombosHint")}
        />
      ) : (
        <div className="combos-list">
          {combos.map((combo) => {
            const client = typeof combo.policy?.client === "string" ? combo.policy.client : "";
            return (
              <Card key={combo.id} className={combo.enabled ? "combo-card" : "combo-card is-paused"} pad="md">
                <div className="combo-card-head">
                  <div className="combo-card-title">
                    <span className="combo-card-icon">
                      <span className="material-symbols-outlined">layers</span>
                    </span>
                    <div>
                      <h3>{combo.display_name || combo.id}</h3>
                      <div className="combo-card-id-row">
                        <code>{combo.id}</code>
                        <button
                          type="button"
                          className="endpoint-row-copy small"
                          onClick={() => copy(combo.id, `combo-${combo.id}`)}
                          aria-label={t("combos.copyComboId")}
                        >
                          <span className="material-symbols-outlined">
                            {copied === `combo-${combo.id}` ? "check" : "content_copy"}
                          </span>
                        </button>
                      </div>
                    </div>
                  </div>
                  <div className="combo-card-badges">
                    {combo.enabled ? (
                      <Badge variant="success" size="sm" dot>
                        {t("combos.enabledLabel")}
                      </Badge>
                    ) : (
                      <Badge size="sm">{t("combos.pausedLabel")}</Badge>
                    )}
                    {client ? <Badge size="sm">{clientLabel(client as "claude-code" | "cowork" | "general")}</Badge> : null}
                    <Badge size="sm">{t("combos.stepsCount", { count: (combo.items || []).length })}</Badge>
                  </div>
                </div>

                <div className="combo-card-items">
                  {(combo.items || []).map((item, index) => (
                    <div className="combo-card-item" key={`${combo.id}-${index}`}>
                      <span className="combo-card-item-index">{index + 1}</span>
                      <div className="combo-card-item-main">
                        <strong>
                          {modelOptions.find((option) => option.value === item.public_model_id)?.label ||
                            item.public_model_id}
                        </strong>
                      </div>
                    </div>
                  ))}
                </div>

                <div className="combo-card-actions">
                  <Button variant="outline" size="sm" icon="edit" onClick={() => openEdit(combo)}>
                    {t("combos.editLabel")}
                  </Button>
                  <Button variant="danger" size="sm" icon="delete" onClick={() => handleDelete(combo)}>
                    {t("combos.deleteLabel")}
                  </Button>
                </div>
              </Card>
            );
          })}
        </div>
      )}

      <ComboFormModal
        open={showFormModal}
        editing={!!editingCombo}
        initial={formSeed}
        modelOptions={
          editingCombo
            ? modelOptions.filter((option) => option.value !== editingCombo.id)
            : modelOptions
        }
        existingIds={existingIds}
        saving={saving}
        onClose={() => {
          setShowFormModal(false);
          setEditingCombo(null);
          setFormSeed(null);
        }}
        onSubmit={(form) => void handleSave(form)}
      />

      <ConfirmDialog
        open={confirmState !== null}
        title={confirmState?.title || ""}
        message={confirmState?.message || ""}
        variant="danger"
        confirmText={confirmState?.confirmText || t("common.confirm")}
        onClose={() => setConfirmState(null)}
        onConfirm={() => {
          confirmState?.onConfirm();
          setConfirmState(null);
        }}
      />
    </section>
  );
}
