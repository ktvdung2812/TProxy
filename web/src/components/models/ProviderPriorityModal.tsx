import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Modal } from "../ui";
import { StatusPill } from "../page-kit";
import { ProviderPriorityEditor } from "./ProviderPriorityEditor";
import type { ModelRecord, ProviderOption, RouteFormData } from "./types";

type Props = {
  open: boolean;
  secret: string;
  model: ModelRecord | null;
  routes: RouteFormData[];
  providers: ProviderOption[];
  credentialCounts: Record<string, number>;
  saving: boolean;
  onClose: () => void;
  onSubmit: (routes: RouteFormData[]) => void;
};

export function ProviderPriorityModal({
  open,
  secret,
  model,
  routes,
  providers,
  credentialCounts,
  saving,
  onClose,
  onSubmit,
}: Props) {
  const { t } = useTranslation();
  const [saveActions, setSaveActions] = useState<{ canSave: boolean; dirty: boolean; save: () => void } | null>(null);

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("models.priority.title")}
      subtitle={model ? `${model.DisplayName || model.ID} · ${model.ID}` : t("models.priority.subtitle")}
      icon="route"
      size="lg"
      className="priority-manager-modal"
      footer={
        saveActions ? (
          <>
            {saveActions.dirty ? (
              <StatusPill tone="warning" title={t("models.priority.unsavedHint")}>{t("models.priority.unsaved")}</StatusPill>
            ) : null}
            <Button variant="ghost" size="sm" onClick={onClose} disabled={saving}>
              {t("common.cancel")}
            </Button>
            <Button
              variant="primary"
              size="sm"
              icon="save"
              loading={saving}
              disabled={!saveActions.canSave}
              onClick={saveActions.save}
            >
              {t("models.priority.save")}
            </Button>
          </>
        ) : null
      }
    >
      <ProviderPriorityEditor
        active={open}
        secret={secret}
        model={model}
        routes={routes}
        providers={providers}
        credentialCounts={credentialCounts}
        onSave={onSubmit}
        onNavigateAway={onClose}
        onRegisterSave={setSaveActions}
      />
    </Modal>
  );
}
