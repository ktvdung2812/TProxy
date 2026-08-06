import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { discoverProviderModels, type DiscoveredModel } from "../providers/api";
import { Button, Input, Modal } from "../ui";
import type { ModelFormData, ModelRecord, ProviderOption, RouteRecord } from "./types";
import { buildModelFormFromSelection, isProviderModelMapped } from "./utils";

type Props = {
  open: boolean;
  secret: string;
  providers: ProviderOption[];
  models: ModelRecord[];
  routesByModel: Record<string, RouteRecord[]>;
  existingIds: string[];
  credentialCounts?: Record<string, number>;
  saving: boolean;
  onClose: () => void;
  onSubmit: (form: ModelFormData) => void;
};

type SelectableModel = {
  providerId: string;
  providerLabel: string;
  model: DiscoveredModel;
};

type Selection = {
  providerId: string;
  upstreamModel: string;
};

function selectionKey(selection: Selection): string {
  return `${selection.providerId}::${selection.upstreamModel}`;
}

export function ModelSelectModal({
  open,
  secret,
  providers,
  models,
  routesByModel,
  existingIds,
  credentialCounts,
  saving,
  onClose,
  onSubmit,
}: Props) {
  const [modelsByProvider, setModelsByProvider] = useState<Record<string, DiscoveredModel[]>>({});
  const [errorsByProvider, setErrorsByProvider] = useState<Record<string, string>>({});
  const [loadingModels, setLoadingModels] = useState(false);
  const [modelsError, setModelsError] = useState<string | null>(null);
  const [searchQuery, setSearchQuery] = useState("");
  const [selection, setSelection] = useState<Selection | null>(null);

  const providerOptions = useMemo(
    () => providers.map((provider) => ({ value: provider.id, label: provider.label })),
    [providers],
  );

  useEffect(() => {
    if (!open) return;
    setSearchQuery("");
    setSelection(null);
    setModelsError(null);
  }, [open]);

  useEffect(() => {
    if (!open || !secret || providerOptions.length === 0) return;

    let cancelled = false;
    setLoadingModels(true);
    setModelsError(null);
    setErrorsByProvider({});

    void (async () => {
      try {
        const nextModels: Record<string, DiscoveredModel[]> = {};
        const nextErrors: Record<string, string> = {};

        await Promise.all(
          providerOptions.map(async (option) => {
            try {
              const result = await discoverProviderModels(secret, option.value);
              nextModels[option.value] = result.data || [];
              if (result.error?.message) {
                nextErrors[option.value] = result.error.message;
              }
            } catch (error) {
              nextModels[option.value] = [];
              nextErrors[option.value] = error instanceof Error ? error.message : "Failed to load models";
            }
          }),
        );

        if (cancelled) return;
        setModelsByProvider(nextModels);
        setErrorsByProvider(nextErrors);
      } catch (error) {
        if (!cancelled) {
          setModelsByProvider({});
          setErrorsByProvider({});
          setModelsError(error instanceof Error ? error.message : "Failed to load supported models");
        }
      } finally {
        if (!cancelled) setLoadingModels(false);
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [open, secret, providerOptions]);

  const selectableModels = useMemo(() => {
    const items: SelectableModel[] = [];
    for (const option of providerOptions) {
      for (const model of modelsByProvider[option.value] || []) {
        if (isProviderModelMapped(models, routesByModel, option.value, model.id)) continue;
        items.push({
          providerId: option.value,
          providerLabel: option.label,
          model,
        });
      }
    }
    return items.sort(
      (left, right) =>
        left.providerLabel.localeCompare(right.providerLabel) || left.model.id.localeCompare(right.model.id),
    );
  }, [models, modelsByProvider, providerOptions, routesByModel]);

  const filteredGroups = useMemo(() => {
    const query = searchQuery.trim().toLowerCase();
    const groups = new Map<string, { label: string; items: SelectableModel[]; error?: string }>();

    for (const option of providerOptions) {
      groups.set(option.value, {
        label: option.label,
        items: [],
        error: errorsByProvider[option.value],
      });
    }

    for (const item of selectableModels) {
      const haystack = [item.model.id, item.model.name, item.providerLabel, ...(item.model.capabilities || [])]
        .filter(Boolean)
        .join(" ")
        .toLowerCase();
      if (query && !haystack.includes(query)) continue;
      const group = groups.get(item.providerId);
      if (!group) continue;
      group.items.push(item);
    }

    return [...groups.entries()]
      .map(([providerId, group]) => ({ providerId, ...group }))
      .filter((group) => group.items.length > 0 || group.error || !loadingModels);
  }, [errorsByProvider, loadingModels, providerOptions, searchQuery, selectableModels]);

  const selectedItem = useMemo(() => {
    if (!selection) return null;
    return (
      selectableModels.find(
        (item) => item.providerId === selection.providerId && item.model.id === selection.upstreamModel,
      ) || null
    );
  }, [selectableModels, selection]);

  const previewForm = useMemo(() => {
    if (!selectedItem) return null;
    return buildModelFormFromSelection({
      providerId: selectedItem.providerId,
      upstreamModel: selectedItem.model.id,
      modelName: selectedItem.model.name,
      capabilities: selectedItem.model.capabilities,
      existingIds,
      credentialCounts,
    });
  }, [existingIds, credentialCounts, selectedItem]);

  const canSubmit = !!previewForm && !loadingModels;
  const totalSupported = selectableModels.length;

  const handleSubmit = () => {
    if (!previewForm) return;
    onSubmit(previewForm);
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Add virtual model"
      subtitle="Select a supported model from your configured providers. Metadata is generated automatically."
      icon="apps"
      size="lg"
      className="model-select-modal"
      footer={
        <>
          <Button variant="ghost" size="sm" onClick={onClose} disabled={saving}>
            Cancel
          </Button>
          <Button
            variant="primary"
            size="sm"
            icon="add"
            loading={saving}
            disabled={!canSubmit}
            onClick={handleSubmit}
          >
            Add model
          </Button>
        </>
      }
    >
      <div className="model-select-form">
        {providerOptions.length === 0 ? (
          <div className="model-select-empty">
            <p>No providers configured yet.</p>
            <Link to="/providers">Set up providers</Link> before adding virtual models.
          </div>
        ) : (
          <>
            <div className="model-select-toolbar">
              <div className="model-select-search">
                <Input
                  value={searchQuery}
                  onChange={(event) => setSearchQuery(event.target.value)}
                  placeholder="Search supported models…"
                  icon="search"
                />
              </div>
              <span className="model-select-count">
                {loadingModels ? "Loading supported models…" : `${totalSupported} available`}
              </span>
            </div>

            <div className="model-select-groups custom-scrollbar">
              {loadingModels && filteredGroups.every((group) => group.items.length === 0) ? (
                <div className="model-select-loading">
                  <span className="material-symbols-outlined animate-spin">progress_activity</span>
                  <p>Discovering supported models from all providers…</p>
                </div>
              ) : null}

              {!loadingModels && filteredGroups.length === 0 ? (
                <p className="cli-tool-hint">No supported models match your search.</p>
              ) : null}

              {filteredGroups.map((group) => (
                <section key={group.providerId} className="model-select-group">
                  <div className="model-select-group-head">
                    <h4>{group.label}</h4>
                    <span className="model-select-group-meta">
                      {group.items.length > 0 ? `${group.items.length} models` : "No models"}
                    </span>
                  </div>

                  {group.error && group.items.length === 0 ? (
                    <p className="model-select-group-error">{group.error}</p>
                  ) : null}

                  {group.items.length > 0 ? (
                    <div className="model-select-chip-grid">
                      {group.items.map((item) => {
                        const active =
                          selection?.providerId === item.providerId && selection.upstreamModel === item.model.id;
                        const label =
                          item.model.name && item.model.name !== item.model.id
                            ? item.model.name
                            : item.model.id;
                        return (
                          <button
                            key={selectionKey({ providerId: item.providerId, upstreamModel: item.model.id })}
                            type="button"
                            className={`model-select-chip${active ? " is-active" : ""}`}
                            title={item.model.id}
                            onClick={() =>
                              setSelection({ providerId: item.providerId, upstreamModel: item.model.id })
                            }
                          >
                            {label}
                          </button>
                        );
                      })}
                    </div>
                  ) : null}
                </section>
              ))}
            </div>

            {previewForm ? (
              <div className="model-select-preview">
                <p className="model-select-preview-title">Generated virtual model</p>
                <div className="model-select-preview-grid">
                  <div>
                    <span>Model ID</span>
                    <code>{previewForm.id}</code>
                  </div>
                  <div>
                    <span>Display name</span>
                    <strong>{previewForm.display_name}</strong>
                  </div>
                  <div>
                    <span>Provider</span>
                    <strong>{selectedItem?.providerLabel}</strong>
                  </div>
                  <div>
                    <span>Upstream</span>
                    <code>{selectedItem?.model.id}</code>
                  </div>
                  <div>
                    <span>Capabilities</span>
                    <strong>{previewForm.capabilities.join(", ")}</strong>
                  </div>
                </div>
              </div>
            ) : null}

            {!loadingModels && totalSupported === 0 ? (
              <p className="cli-tool-hint">
                No supported models are available yet. Check provider credentials or try discovery on the{" "}
                <Link to="/providers">Providers</Link> page.
              </p>
            ) : null}
          </>
        )}

        {modelsError ? <p className="field-error">{modelsError}</p> : null}
      </div>
    </Modal>
  );
}
