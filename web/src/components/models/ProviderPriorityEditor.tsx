import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Badge, Button, Select, Toggle } from "../ui";
import { discoverProviderModels, type DiscoveredModel } from "../providers/api";
import type { ModelRecord, ProviderOption, RouteFormData } from "./types";
import {
  accountHealthLabel,
  accountHealthVariant,
  reorderRoutePriorities,
  sortRouteForms,
  validatePriorityRoutes,
} from "./utils";

type Props = {
  active: boolean;
  secret: string;
  model: ModelRecord | null;
  routes: RouteFormData[];
  providers: ProviderOption[];
  credentialCounts: Record<string, number>;
  saving?: boolean;
  showActions?: boolean;
  onSave?: (routes: RouteFormData[]) => void;
  onNavigateAway?: () => void;
};

function moveItem<T>(items: T[], from: number, to: number) {
  if (to < 0 || to >= items.length) return items;
  const next = [...items];
  const [item] = next.splice(from, 1);
  next.splice(to, 0, item);
  return next;
}

export function ProviderPriorityEditor({
  active,
  secret,
  model,
  routes,
  providers,
  credentialCounts,
  saving = false,
  showActions = true,
  onSave,
  onNavigateAway,
}: Props) {
  const navigate = useNavigate();
  const [items, setItems] = useState<RouteFormData[]>([]);
  const [modelsByProvider, setModelsByProvider] = useState<Record<string, DiscoveredModel[]>>({});
  const [loadingModels, setLoadingModels] = useState(false);
  const [modelsError, setModelsError] = useState<string | null>(null);

  useEffect(() => {
    if (!active) return;
    setItems(sortRouteForms(routes));
  }, [active, routes, model?.ID]);

  const providerIds = useMemo(
    () => providers.map((provider) => provider.id).filter(Boolean),
    [providers],
  );

  useEffect(() => {
    if (!active || !secret || providerIds.length === 0) return;

    let cancelled = false;
    setLoadingModels(true);
    setModelsError(null);

    void (async () => {
      try {
        const entries = await Promise.all(
          providerIds.map(async (providerId) => {
            try {
              const result = await discoverProviderModels(secret, providerId);
              return [providerId, result.data || []] as const;
            } catch {
              return [providerId, []] as const;
            }
          }),
        );
        if (cancelled) return;
        setModelsByProvider(Object.fromEntries(entries));
      } catch (error) {
        if (!cancelled) {
          setModelsByProvider({});
          setModelsError(error instanceof Error ? error.message : "Failed to load supported models");
        }
      } finally {
        if (!cancelled) setLoadingModels(false);
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [active, secret, providerIds]);

  const providerOptions = useMemo(
    () => providers.map((provider) => ({ value: provider.id, label: provider.label })),
    [providers],
  );

  const validationError = validatePriorityRoutes(items);
  const enabledItems = items.filter((item) => item.enabled);
  const primaryProvider = enabledItems[0]?.provider || "";

  const goToProviders = () => {
    onNavigateAway?.();
    navigate("/providers");
  };

  const updateRoute = (index: number, patch: Partial<RouteFormData>) => {
    setItems((current) =>
      current.map((route, routeIndex) => {
        if (routeIndex !== index) return route;
        const next = { ...route, ...patch };
        if (patch.provider && patch.provider !== route.provider) {
          const providerModels = modelsByProvider[patch.provider] || [];
          const hasCurrent = providerModels.some((item) => item.id === route.upstream_model);
          next.upstream_model = hasCurrent ? route.upstream_model : providerModels[0]?.id || "";
        }
        return next;
      }),
    );
  };

  const upstreamOptions = (providerId: string, currentValue: string) => {
    const providerModels = modelsByProvider[providerId] || [];
    if (currentValue && !providerModels.some((item) => item.id === currentValue)) {
      return [{ id: currentValue, name: currentValue }, ...providerModels];
    }
    return providerModels;
  };

  const removeRoute = (index: number) => {
    setItems((current) => reorderRoutePriorities(current.filter((_, routeIndex) => routeIndex !== index)));
  };

  const moveRoute = (index: number, direction: -1 | 1) => {
    setItems((current) => reorderRoutePriorities(moveItem(current, index, index + direction)));
  };

  if (!model) {
    return <p className="priority-manager-empty">Select a model to manage provider priority.</p>;
  }

  return (
    <div className="priority-manager">
      <div className="priority-manager-detail-head">
        <div>
          <h3>{model.DisplayName || model.ID}</h3>
          <p>
            <code>{model.ID}</code>
            {model.Aliases?.length ? (
              <>
                {" "}
                · Aliases: {(model.Aliases || []).join(", ")}
              </>
            ) : null}
          </p>
        </div>
        {showActions && onSave ? (
          <Button
            variant="primary"
            size="sm"
            icon="save"
            loading={saving}
            disabled={!!validationError}
            onClick={() => onSave(reorderRoutePriorities(items))}
          >
            Save priority
          </Button>
        ) : null}
      </div>

      <div className="priority-manager-help">
        <p>
          Requests use the first enabled provider as <strong>P1</strong>, then fall back down the chain. Disable
          providers you do not want, or keep only one enabled route to pin a single provider.
        </p>
        {primaryProvider ? (
          <p className="priority-manager-primary">
            Primary provider: <code>{primaryProvider}</code>
          </p>
        ) : (
          <p className="priority-manager-primary is-warning">No enabled provider selected.</p>
        )}
      </div>

      {providerOptions.length === 0 ? (
        <div className="priority-manager-empty">
          <p>No providers configured yet. Set up providers first, then return here to arrange priority.</p>
          <Button variant="primary" size="sm" icon="dns" onClick={goToProviders}>
            Open Providers
          </Button>
        </div>
      ) : items.length === 0 ? (
        <div className="priority-manager-empty">
          <p>No provider routes for this model yet. Configure providers, then map them here.</p>
          <Button variant="primary" size="sm" icon="dns" onClick={goToProviders}>
            Open Providers
          </Button>
        </div>
      ) : (
        <div className="priority-manager-table" role="table" aria-label="Provider priority routes">
          <div className="priority-manager-head" role="row">
            <span>Order</span>
            <span>Provider</span>
            <span>Upstream</span>
            <span>Accounts</span>
            <span>Enabled</span>
            <span aria-hidden />
          </div>
          {items.map((route, index) => {
            const enabledPosition = route.enabled ? enabledItems.findIndex((item) => item === route) + 1 : 0;
            const accountCount = credentialCounts[route.provider] ?? 0;
            return (
              <div
                className={`priority-manager-row${route.enabled ? "" : " is-disabled"}`}
                key={`${route.id || route.provider}-${index}`}
                role="row"
              >
                <div className="priority-manager-order" role="cell">
                  {route.enabled && enabledPosition > 0 ? (
                    <span className="priority-badge">P{enabledPosition}</span>
                  ) : (
                    <span className="priority-badge is-muted">off</span>
                  )}
                </div>
                <div className="priority-manager-provider" role="cell">
                  <Select
                    value={route.provider}
                    onChange={(event) => updateRoute(index, { provider: event.target.value })}
                  >
                    {providerOptions.map((option) => (
                      <option key={option.value} value={option.value}>
                        {option.label}
                      </option>
                    ))}
                  </Select>
                </div>
                <div className="priority-manager-upstream" role="cell">
                  <Select
                    value={route.upstream_model}
                    disabled={loadingModels || upstreamOptions(route.provider, route.upstream_model).length === 0}
                    onChange={(event) => updateRoute(index, { upstream_model: event.target.value })}
                  >
                    {loadingModels ? <option value="">Loading models…</option> : null}
                    {!loadingModels && upstreamOptions(route.provider, route.upstream_model).length === 0 ? (
                      <option value="">No supported models</option>
                    ) : null}
                    {upstreamOptions(route.provider, route.upstream_model).map((item) => (
                      <option key={item.id} value={item.id}>
                        {item.name && item.name !== item.id ? `${item.name} (${item.id})` : item.id}
                      </option>
                    ))}
                  </Select>
                </div>
                <div className="priority-manager-health" role="cell">
                  <Badge variant={accountHealthVariant(accountCount)} size="sm" dot>
                    {accountHealthLabel(accountCount)}
                  </Badge>
                </div>
                <div className="priority-manager-enabled" role="cell">
                  <Toggle
                    checked={route.enabled}
                    onChange={(event) => updateRoute(index, { enabled: event.target.checked })}
                    aria-label={`Enable ${route.provider}`}
                  />
                </div>
                <div className="priority-manager-actions" role="cell">
                  <button
                    type="button"
                    className="priority-manager-icon-btn"
                    disabled={index === 0}
                    onClick={() => moveRoute(index, -1)}
                    aria-label="Move provider up"
                  >
                    <span className="material-symbols-outlined">arrow_upward</span>
                  </button>
                  <button
                    type="button"
                    className="priority-manager-icon-btn"
                    disabled={index === items.length - 1}
                    onClick={() => moveRoute(index, 1)}
                    aria-label="Move provider down"
                  >
                    <span className="material-symbols-outlined">arrow_downward</span>
                  </button>
                  <button
                    type="button"
                    className="priority-manager-icon-btn priority-manager-icon-btn-danger"
                    onClick={() => removeRoute(index)}
                    aria-label="Remove provider route"
                  >
                    <span className="material-symbols-outlined">delete</span>
                  </button>
                </div>
              </div>
            );
          })}
        </div>
      )}

      <div className="priority-manager-footer">
        <Button variant="outline" size="sm" icon="dns" onClick={goToProviders}>
          Manage providers
        </Button>
      </div>

      {modelsError ? <p className="field-error">{modelsError}</p> : null}
      {validationError ? <p className="field-error">{validationError}</p> : null}
    </div>
  );
}
