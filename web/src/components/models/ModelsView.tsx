import { useMemo, useState } from "react";
import { Badge, Button, Card, ConfirmDialog, EmptyState } from "../ui";
import { deleteModel, saveModel } from "./api";
import { ModelSelectModal } from "./ModelSelectModal";
import { ProviderPriorityModal } from "./ProviderPriorityModal";
import type { ModelFormData, ModelRecord, ProviderOption, RouteFormData, RouteRecord } from "./types";
import { modelToForm, sortRoutes } from "./utils";

type Props = {
  secret: string;
  models: ModelRecord[];
  routesByModel: Record<string, RouteRecord[]>;
  providers: ProviderOption[];
  credentialCounts: Record<string, number>;
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

export function ModelsView({
  secret,
  models,
  routesByModel,
  providers,
  credentialCounts,
  onMutated,
  onNotice,
  onError,
}: Props) {
  const [showSelectModal, setShowSelectModal] = useState(false);
  const [showPriorityModal, setShowPriorityModal] = useState(false);
  const [priorityModel, setPriorityModel] = useState<ModelRecord | null>(null);
  const [saving, setSaving] = useState(false);
  const [confirmState, setConfirmState] = useState<ConfirmState | null>(null);

  const existingIds = useMemo(() => models.map((model) => model.ID), [models]);

  const openCreate = () => {
    setShowSelectModal(true);
  };

  const openPriority = (model: ModelRecord) => {
    setPriorityModel(model);
    setShowPriorityModal(true);
  };

  const handleCreateModel = async (form: ModelFormData) => {
    setSaving(true);
    try {
      await saveModel(secret, form, false);
      onNotice?.(`Model "${form.display_name || form.id}" created`);
      setShowSelectModal(false);
      const createdModel: ModelRecord = {
        ID: form.id.trim(),
        DisplayName: form.display_name.trim() || form.id.trim(),
        Aliases: [],
        Enabled: form.enabled,
        RewriteResponseModel: form.rewrite_response_model,
        Capabilities: form.capabilities,
      };
      onMutated?.();
      setPriorityModel(createdModel);
      setShowPriorityModal(true);
    } catch (error) {
      onError?.(error instanceof Error ? error.message : "Failed to create model");
    } finally {
      setSaving(false);
    }
  };

  const handleSavePriority = async (routes: RouteFormData[]) => {
    if (!priorityModel) return;
    setSaving(true);
    try {
      const form = modelToForm(priorityModel, routesByModel[priorityModel.ID] || []);
      form.routes = routes;
      await saveModel(secret, form, true);
      onNotice?.(`Provider priority saved for "${priorityModel.DisplayName || priorityModel.ID}"`);
      setShowPriorityModal(false);
      setPriorityModel(null);
      onMutated?.();
    } catch (error) {
      onError?.(error instanceof Error ? error.message : "Failed to save provider priority");
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = (model: ModelRecord) => {
    setConfirmState({
      title: "Delete model",
      message: `Delete "${model.DisplayName || model.ID}"? Clients using this model ID will stop working immediately.`,
      confirmText: "Delete",
      onConfirm: () => {
        void (async () => {
          try {
            await deleteModel(secret, model.ID);
            onNotice?.(`Model "${model.DisplayName || model.ID}" deleted`);
            onMutated?.();
          } catch (error) {
            onError?.(error instanceof Error ? error.message : "Failed to delete model");
          }
        })();
      },
    });
  };

  return (
    <section className="section">
      <div className="section-head">
        <div>
          <p className="eyebrow">Public surface</p>
          <h2>Provider Priority Manager</h2>
          <p>Map providers to stable model IDs and control fallback order.</p>
        </div>
        <div className="models-head-actions">
          <span className="meta">{models.length} models</span>
          <Button variant="primary" size="sm" icon="add" onClick={openCreate}>
            Create model
          </Button>
        </div>
      </div>

      <div className="model-grid">
        {models.map((model) => {
          const routes = sortRoutes(routesByModel[model.ID] || []);
          const enabledRoutes = routes.filter((route) => route.Enabled);
          const visibleRoutes = routes.slice(0, 3);
          const hiddenRouteCount = routes.length - visibleRoutes.length;
          return (
            <Card key={model.ID} pad="md" className="model-card">
              <div className="model-title">
                <span className="model-icon">M</span>
                <div>
                  <h3>{model.DisplayName || model.ID}</h3>
                  <code>{model.ID}</code>
                </div>
                {model.Enabled ? (
                  <Badge variant="success" size="sm" dot>
                    active
                  </Badge>
                ) : (
                  <Badge size="sm">off</Badge>
                )}
              </div>
              {(model.Capabilities || []).length > 0 && (
                <div className="tags">
                  {model.Capabilities.map((capability) => (
                    <span key={capability}>{capability}</span>
                  ))}
                </div>
              )}
              <div className="route-list">
                {visibleRoutes.map((route) => {
                  const enabledPosition = route.Enabled
                    ? enabledRoutes.findIndex((item) => item.ID === route.ID) + 1
                    : 0;
                  return (
                    <div
                      className={`route-row${route.Enabled ? "" : " is-disabled"}${enabledPosition === 1 ? " is-primary" : ""}`}
                      key={route.ID}
                    >
                      <b>{route.Enabled && enabledPosition > 0 ? `P${enabledPosition}` : "—"}</b>
                      <span>{route.ProviderID}</span>
                      <code>{route.UpstreamModel}</code>
                      <small>{route.Enabled ? `priority ${route.Priority}` : "disabled"}</small>
                    </div>
                  );
                })}
                {hiddenRouteCount > 0 ? (
                  <p className="model-route-more">
                    +{hiddenRouteCount} more provider{hiddenRouteCount === 1 ? "" : "s"}
                  </p>
                ) : null}
              </div>
              <div className="model-card-actions">
                <Button variant="primary" size="sm" icon="route" onClick={() => openPriority(model)}>
                  Manage priority
                </Button>
                <Button
                  variant="danger"
                  size="sm"
                  icon="delete"
                  className="btn-icon-only"
                  aria-label="Delete model"
                  title="Delete model"
                  onClick={() => handleDelete(model)}
                />
              </div>
            </Card>
          );
        })}
        {models.length === 0 && <EmptyState icon="route" text="No models configured yet." />}
      </div>

      <ModelSelectModal
        open={showSelectModal}
        secret={secret}
        providers={providers}
        models={models}
        routesByModel={routesByModel}
        existingIds={existingIds}
        saving={saving}
        onClose={() => setShowSelectModal(false)}
        onSubmit={(form) => void handleCreateModel(form)}
      />

      <ProviderPriorityModal
        open={showPriorityModal}
        secret={secret}
        model={priorityModel}
        routes={priorityModel ? modelToForm(priorityModel, routesByModel[priorityModel.ID] || []).routes : []}
        providers={providers}
        credentialCounts={credentialCounts}
        saving={saving}
        onClose={() => {
          setShowPriorityModal(false);
          setPriorityModel(null);
        }}
        onSubmit={(routes) => void handleSavePriority(routes)}
      />

      <ConfirmDialog
        open={!!confirmState}
        onClose={() => setConfirmState(null)}
        onConfirm={() => confirmState?.onConfirm()}
        title={confirmState?.title || "Confirm"}
        message={confirmState?.message || ""}
        confirmText={confirmState?.confirmText}
        variant="danger"
      />
    </section>
  );
}
