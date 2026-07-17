import { Button, Modal } from "../ui";
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
  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Provider Priority Manager"
      subtitle={model ? `${model.DisplayName || model.ID} · ${model.ID}` : "Configure provider fallback order"}
      icon="route"
      size="lg"
      className="priority-manager-modal"
      footer={
        <Button variant="ghost" size="sm" onClick={onClose} disabled={saving}>
          Close
        </Button>
      }
    >
      <ProviderPriorityEditor
        active={open}
        secret={secret}
        model={model}
        routes={routes}
        providers={providers}
        credentialCounts={credentialCounts}
        saving={saving}
        onSave={onSubmit}
        onNavigateAway={onClose}
      />
    </Modal>
  );
}
