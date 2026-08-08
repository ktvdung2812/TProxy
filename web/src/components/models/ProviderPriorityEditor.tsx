import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Badge, Button, Select, Toggle } from "../ui";
import type { FailoverState } from "./failoverApi";
import type { ModelRecord, ProviderOption, RouteFormData } from "./types";
import { useProviderFailover } from "./useProviderFailover";
import { useProviderModelDiscovery } from "./useProviderModelDiscovery";
import {
  accountHealthLabel,
  accountHealthVariant,
  hasUsableAccounts,
  providersForUpstreamModel,
  reorderRoutePriorities,
  resolveCanonicalUpstreamModel,
  resolveProviderUpstreamModel,
  routeFormsEqual,
  sortRouteForms,
  syncRoutesForUpstreamModel,
  validatePriorityRoutes,
} from "./utils";

function formatRetryIn(seconds: number) {
  if (seconds <= 0) return "any moment";
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  return `${Math.round(minutes / 60)}h`;
}

function failoverBadge(state: FailoverState | undefined) {
  if (!state) return null;
  if (state.state === "open") {
    return {
      variant: "error" as const,
      label: `auto-disabled · retry in ${formatRetryIn(state.retry_in_seconds)}`,
      title: `Disabled after ${state.failures} failed attempt${state.failures === 1 ? "" : "s"}${
        state.last_status ? ` (last status ${state.last_status})` : ""
      }${state.last_error ? `: ${state.last_error}` : ""}`,
    };
  }
  if (state.state === "half_open") {
    return { variant: "warning" as const, label: "probing", title: "Next request is a probe; success puts this provider back in rotation." };
  }
  if (state.failures > 0) {
    return {
      variant: "warning" as const,
      label: `${state.failures}/${state.threshold} failures`,
      title: `${state.threshold - state.failures} more consecutive failure${
        state.threshold - state.failures === 1 ? "" : "s"
      } will take this provider out of the chain.`,
    };
  }
  return null;
}

type Props = {
  active: boolean;
  secret: string;
  model: ModelRecord | null;
  routes: RouteFormData[];
  providers: ProviderOption[];
  credentialCounts: Record<string, number>;
  onSave?: (routes: RouteFormData[]) => void;
  onNavigateAway?: () => void;
  onRegisterSave?: (actions: { canSave: boolean; save: () => void } | null) => void;
};

export function ProviderPriorityEditor({
  active,
  secret,
  model,
  routes,
  providers,
  credentialCounts,
  onSave,
  onNavigateAway,
  onRegisterSave,
}: Props) {
  const navigate = useNavigate();
  const [items, setItems] = useState<RouteFormData[]>([]);
  const [resettingProvider, setResettingProvider] = useState<string | null>(null);
  const { modelsByProvider, loading: loadingModels, error: modelsError } = useProviderModelDiscovery(
    secret,
    providers,
    active,
  );
  const { failoverByProvider, failoverError, resetFailover } = useProviderFailover(secret, model?.ID, active);

  useEffect(() => {
    if (!active) return;
    setItems(sortRouteForms(routes));
  }, [active, routes, model?.ID]);

  const canonicalUpstream = useMemo(
    () => (model ? resolveCanonicalUpstreamModel(model, sortRouteForms(routes)) : ""),
    [model, routes],
  );

  useEffect(() => {
    if (!active || !model || loadingModels || !canonicalUpstream) return;

    setItems((current) => {
      const base = current.length > 0 ? current : sortRouteForms(routes);
      const synced = syncRoutesForUpstreamModel(base, providers, modelsByProvider, canonicalUpstream, credentialCounts);
      return routeFormsEqual(base, synced) ? base : synced;
    });
  }, [active, canonicalUpstream, loadingModels, model, modelsByProvider, providers, routes]);

  const compatibleProviders = useMemo(
    () => providersForUpstreamModel(providers, modelsByProvider, canonicalUpstream),
    [canonicalUpstream, modelsByProvider, providers],
  );

  const providerOptions = useMemo(
    () => compatibleProviders.map((provider) => ({ value: provider.id, label: provider.label })),
    [compatibleProviders],
  );

  const validationError = validatePriorityRoutes(items);
  // Providers with no accounts stay in the saved chain but are hidden here and
  // skipped by the router, so they never occupy a visible priority slot.
  const visibleItems = useMemo(
    () => items.filter((item) => hasUsableAccounts(item, credentialCounts)),
    [items, credentialCounts],
  );
  const hiddenCount = items.length - visibleItems.length;
  const enabledItems = visibleItems.filter((item) => item.enabled);
  const primaryProvider = enabledItems[0]?.provider || "";

  useEffect(() => {
    if (!onRegisterSave) return;
    if (!model || !onSave) {
      onRegisterSave(null);
      return;
    }
    onRegisterSave({
      canSave: !validationError,
      save: () => onSave(reorderRoutePriorities(items)),
    });
    return () => onRegisterSave(null);
  }, [onRegisterSave, model, onSave, validationError, items]);

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
          if (canonicalUpstream) {
            next.upstream_model = resolveProviderUpstreamModel(
              modelsByProvider,
              patch.provider,
              canonicalUpstream,
            );
          } else {
            const providerModels = modelsByProvider[patch.provider] || [];
            next.upstream_model = providerModels[0]?.id || route.upstream_model;
          }
        }
        return next;
      }),
    );
  };

  const providerOptionsForRoute = (currentProviderId: string) => {
    if (!currentProviderId) return providerOptions;
    if (providerOptions.some((option) => option.value === currentProviderId)) return providerOptions;
    const fallback = providers.find((provider) => provider.id === currentProviderId);
    if (!fallback) return providerOptions;
    return [...providerOptions, { value: fallback.id, label: fallback.label }];
  };

  const removeRoute = (index: number) => {
    setItems((current) => reorderRoutePriorities(current.filter((_, routeIndex) => routeIndex !== index)));
  };

  // Moves are expressed against the visible rows, then applied to the real list
  // so hidden account-less routes keep their saved position.
  const moveRoute = (visiblePosition: number, direction: -1 | 1) => {
    const target = visiblePosition + direction;
    if (target < 0 || target >= visibleItems.length) return;
    const from = items.indexOf(visibleItems[visiblePosition]);
    const to = items.indexOf(visibleItems[target]);
    if (from < 0 || to < 0) return;
    setItems((current) => {
      const next = [...current];
      [next[from], next[to]] = [next[to], next[from]];
      return reorderRoutePriorities(next);
    });
  };

  const onResetFailover = async (providerId: string) => {
    setResettingProvider(providerId);
    try {
      await resetFailover(providerId);
    } finally {
      setResettingProvider(null);
    }
  };

  if (!model) {
    return <p className="priority-manager-empty">Select a model to manage provider priority.</p>;
  }

  return (
    <div className="priority-manager">
      <div className="priority-manager-help">
        <p>
          This is where tproxy decides which provider serves <code>{model.ID}</code>. Requests hit{" "}
          <strong>P1</strong> first, then fall back down the chain. Disable providers you do not want, or keep only one
          enabled route to pin a single provider.
        </p>
        <p>
          A provider that keeps failing is taken out of this chain automatically and traffic moves to the next one. It is
          probed again after a short backoff, or immediately if you re-enable it here. Providers with no accounts are
          hidden because they cannot serve a request.
        </p>
        {hiddenCount > 0 ? (
          <p className="priority-manager-primary">
            {hiddenCount} provider{hiddenCount === 1 ? "" : "s"} hidden — no accounts configured. Their saved priority is
            kept and they return to the chain once an account is added.
          </p>
        ) : null}
        {canonicalUpstream ? (
          <p className="priority-manager-primary">
            Upstream model: <code>{canonicalUpstream}</code>
            {!loadingModels ? (
              <>
                {" "}
                · {compatibleProviders.length} compatible provider{compatibleProviders.length === 1 ? "" : "s"}
              </>
            ) : null}
          </p>
        ) : null}
        {primaryProvider ? (
          <p className="priority-manager-primary">
            Primary provider: <code>{primaryProvider}</code>
          </p>
        ) : (
          <p className="priority-manager-primary is-warning">No enabled provider selected.</p>
        )}
      </div>

      {providers.length === 0 ? (
        <div className="priority-manager-empty">
          <p>No providers configured yet. Set up providers first, then return here to arrange priority.</p>
          <Button variant="primary" size="sm" icon="dns" onClick={goToProviders}>
            Open Providers
          </Button>
        </div>
      ) : !loadingModels && compatibleProviders.length === 0 && items.length === 0 ? (
        <div className="priority-manager-empty">
          <p>
            {canonicalUpstream
              ? `No providers currently expose upstream model "${canonicalUpstream}".`
              : "No provider routes for this model yet. Configure providers, then map them here."}
          </p>
          <Button variant="primary" size="sm" icon="dns" onClick={goToProviders}>
            Open Providers
          </Button>
        </div>
      ) : items.length === 0 ? (
        <div className="priority-manager-empty">
          <p>Discovering providers that support upstream model "{canonicalUpstream}"…</p>
        </div>
      ) : visibleItems.length === 0 ? (
        <div className="priority-manager-empty">
          <p>
            Every provider mapped to this model has 0 accounts, so none of them can serve a request. Add an account to a
            provider and it rejoins the chain at its saved priority.
          </p>
          <Button variant="primary" size="sm" icon="dns" onClick={goToProviders}>
            Open Providers
          </Button>
        </div>
      ) : (
        <div className="priority-manager-table" role="table" aria-label="Provider priority routes">
          <div className="priority-manager-head" role="row">
            <span>Order</span>
            <span>Provider</span>
            <span>Accounts &amp; health</span>
            <span>Enabled</span>
            <span aria-hidden />
          </div>
          {visibleItems.map((route, visiblePosition) => {
            const index = items.indexOf(route);
            const enabledPosition = route.enabled ? enabledItems.findIndex((item) => item === route) + 1 : 0;
            const accountCount = credentialCounts[route.provider] ?? 0;
            const failover = failoverByProvider[route.provider];
            const badge = failoverBadge(failover);
            const trippedOut = failover?.state === "open";
            return (
              <div
                className={`priority-manager-row${route.enabled ? "" : " is-disabled"}${
                  trippedOut ? " is-failed-over" : ""
                }`}
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
                    {providerOptionsForRoute(route.provider).map((option) => (
                      <option key={option.value} value={option.value}>
                        {option.label}
                      </option>
                    ))}
                  </Select>
                </div>
                <div className="priority-manager-health" role="cell">
                  <Badge variant={accountHealthVariant(accountCount)} size="sm" dot>
                    {accountHealthLabel(accountCount)}
                  </Badge>
                  {badge ? (
                    <Badge variant={badge.variant} size="sm" dot title={badge.title}>
                      {badge.label}
                    </Badge>
                  ) : null}
                  {trippedOut ? (
                    <button
                      type="button"
                      className="priority-manager-reset-btn"
                      disabled={resettingProvider === route.provider}
                      onClick={() => void onResetFailover(route.provider)}
                    >
                      {resettingProvider === route.provider ? "Re-enabling…" : "Re-enable now"}
                    </button>
                  ) : null}
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
                    disabled={visiblePosition === 0}
                    onClick={() => moveRoute(visiblePosition, -1)}
                    aria-label="Move provider up"
                  >
                    <span className="material-symbols-outlined">arrow_upward</span>
                  </button>
                  <button
                    type="button"
                    className="priority-manager-icon-btn"
                    disabled={visiblePosition === visibleItems.length - 1}
                    onClick={() => moveRoute(visiblePosition, 1)}
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
      {failoverError ? <p className="field-error">{failoverError}</p> : null}
      {validationError ? <p className="field-error">{validationError}</p> : null}
    </div>
  );
}
