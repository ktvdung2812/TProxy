import { useMemo, useState } from "react";
import { Badge, Button, Card, ConfirmDialog, EmptyState } from "../ui";
import { deleteModel, saveModel } from "./api";
import { ModelSelectModal } from "./ModelSelectModal";
import { ProviderPriorityModal } from "./ProviderPriorityModal";
import type { ModelFormData, ModelRecord, ProviderOption, RouteFormData, RouteRecord } from "./types";
import { useProviderModelDiscovery } from "./useProviderModelDiscovery";
import { buildModelCardRoutes, modelToForm, providerDisplayLabel, sortRoutes } from "./utils";

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

  const { modelsByProvider, loading: loadingDiscovery } = useProviderModelDiscovery(secret, providers, true);

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
          <p>
            The routing gateway for every public model ID. Clients send a stable model name; tproxy resolves it here
            to decide which provider and upstream model handle the request, with automatic fallback down the priority
            chain.
          </p>
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
          const savedRoutes = sortRoutes(routesByModel[model.ID] || []);
          const displayRoutes = loadingDiscovery
            ? savedRoutes.map((route) => {
                const enabledRoutes = savedRoutes.filter((item) => item.Enabled);
                const enabledPosition = route.Enabled
                  ? enabledRoutes.findIndex((item) => item.ID === route.ID) + 1
                  : 0;
                return {
                  key: route.ID,
                  provider: route.ProviderID,
                  providerLabel: providerDisplayLabel(providers, route.ProviderID),
                  upstreamModel: route.UpstreamModel,
                  enabled: route.Enabled,
                  priority: route.Priority,
                  saved: true,
                  enabledPosition,
                  accountCount: credentialCounts[route.ProviderID] ?? 0,
                };
              })
            : buildModelCardRoutes(model, savedRoutes, providers, modelsByProvider).map((route) => ({
                ...route,
                accountCount: credentialCounts[route.provider] ?? 0,
              }));
          const visibleRoutes = displayRoutes.slice(0, 4);
          const hiddenRouteCount = displayRoutes.length - visibleRoutes.length;
          const suggestedCount = displayRoutes.filter((route) => !route.saved).length;
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
                {visibleRoutes.length === 0 ? (
                  <p className="model-route-empty">
                    {loadingDiscovery ? "Discovering compatible providers…" : "No provider routes yet"}
                  </p>
                ) : null}
                {visibleRoutes.map((route) => (
                  <div
                    className={`route-row${route.enabled ? "" : " is-disabled"}${route.enabledPosition === 1 ? " is-primary" : ""}${route.saved ? "" : " is-suggested"}`}
                    key={route.key}
                  >
                    <b>{route.enabled && route.enabledPosition > 0 ? `P${route.enabledPosition}` : "—"}</b>
                    <span title={route.provider}>{route.providerLabel}</span>
                    <small>
                      {route.saved ? `priority ${route.priority}` : "new"}
                    </small>
                  </div>
                ))}
                {hiddenRouteCount > 0 ? (
                  <p className="model-route-more">
                    +{hiddenRouteCount} more provider{hiddenRouteCount === 1 ? "" : "s"}
                  </p>
                ) : null}
                {suggestedCount > 0 ? (
                  <p className="model-route-hint">
                    {suggestedCount} new provider{suggestedCount === 1 ? "" : "s"} at the bottom — open Manage priority to save.
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
