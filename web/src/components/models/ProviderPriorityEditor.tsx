import { useEffect, useMemo, useState, type DragEvent } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useNavigate } from "react-router-dom";
import { ProviderLogo } from "../providers/ProviderLogo";
import { Button, Select, Toggle, cn } from "../ui";
import { IconAction, LinkButton, PageNote, StatusPill, ToolButton, type Tone } from "../page-kit";
import type { FailoverState } from "./failoverApi";
import { formatRetryIn } from "./modelHealth";
import type { ModelRecord, ProviderOption, RouteFormData } from "./types";
import { useProviderFailover } from "./useProviderFailover";
import { useProviderModelDiscovery } from "./useProviderModelDiscovery";
import {
  hasUsableAccounts,
  providerDisplayLabel,
  providersForUpstreamModel,
  reorderRoutePriorities,
  resolveCanonicalUpstreamModel,
  resolveProviderUpstreamModel,
  routeFormsEqual,
  sortRouteForms,
  syncRoutesForUpstreamModel,
  validatePriorityRoutes,
} from "./utils";

function failoverPill(state: FailoverState | undefined, t: TFunction): { tone: Tone; label: string; title: string } | null {
  if (!state) return null;
  const detail = [
    state.last_status ? t("models.priority.lastStatus", { status: state.last_status }) : "",
    state.last_error || "",
  ].filter(Boolean).join(" · ");
  if (state.state === "open") {
    const retry = formatRetryIn(state.retry_in_seconds);
    return {
      tone: "danger",
      label: retry ? t("models.step.pausedFor", { time: retry }) : t("models.step.paused"),
      title: [t("models.priority.pausedHint", { count: state.failures }), detail].filter(Boolean).join(" — "),
    };
  }
  if (state.state === "half_open") {
    return { tone: "warning", label: t("models.step.probing"), title: t("models.priority.probingHint") };
  }
  if (state.failures > 0) {
    return {
      tone: "warning",
      label: t("models.step.failing", { failures: state.failures, threshold: state.threshold }),
      title: [t("models.priority.failingHint", { count: state.threshold - state.failures }), detail].filter(Boolean).join(" — "),
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
  onRegisterSave?: (actions: { canSave: boolean; dirty: boolean; save: () => void } | null) => void;
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
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [items, setItems] = useState<RouteFormData[]>([]);
  const [resettingProvider, setResettingProvider] = useState<string | null>(null);
  const [dragIndex, setDragIndex] = useState<number | null>(null);
  const [dropIndex, setDropIndex] = useState<number | null>(null);
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
  const savedProviders = useMemo(() => new Set(routes.map((route) => route.provider)), [routes]);
  const dirty = useMemo(
    () => !routeFormsEqual(reorderRoutePriorities(items), reorderRoutePriorities(sortRouteForms(routes))),
    [items, routes],
  );

  useEffect(() => {
    if (!onRegisterSave) return;
    if (!model || !onSave) {
      onRegisterSave(null);
      return;
    }
    onRegisterSave({
      canSave: !validationError,
      dirty,
      save: () => onSave(reorderRoutePriorities(items)),
    });
    return () => onRegisterSave(null);
  }, [onRegisterSave, model, onSave, validationError, items, dirty]);

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
  const moveVisible = (from: number, to: number) => {
    if (from === to || to < 0 || to >= visibleItems.length) return;
    setItems((current) => {
      const visible = current.filter((item) => hasUsableAccounts(item, credentialCounts));
      const slots = visible.map((item) => current.indexOf(item));
      const reordered = [...visible];
      const [moved] = reordered.splice(from, 1);
      reordered.splice(to, 0, moved);
      const next = [...current];
      slots.forEach((slot, position) => {
        next[slot] = reordered[position];
      });
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

  const dragHandlers = (position: number) => ({
    onDragOver: (event: DragEvent<HTMLDivElement>) => {
      if (dragIndex === null || dragIndex === position) return;
      event.preventDefault();
      event.dataTransfer.dropEffect = "move";
      setDropIndex(position);
    },
    onDrop: (event: DragEvent<HTMLDivElement>) => {
      event.preventDefault();
      if (dragIndex !== null) moveVisible(dragIndex, position);
      setDragIndex(null);
      setDropIndex(null);
    },
  });

  if (!model) {
    return <p className="priority-manager-empty">{t("models.priority.selectModel")}</p>;
  }

  const providerFor = (id: string) => providers.find((provider) => provider.id === id);

  return (
    <div className="md-pe">
      <dl className="cp-facts md-pe-facts">
        <div>
          <dt>{t("models.priority.publicId")}</dt>
          <dd><code>{model.ID}</code></dd>
        </div>
        <div>
          <dt>{t("models.priority.upstream")}</dt>
          <dd title={canonicalUpstream}><code>{canonicalUpstream || "—"}</code></dd>
        </div>
        <div>
          <dt>{t("models.priority.compatible")}</dt>
          <dd>{loadingModels ? t("models.priority.checking") : t("models.priority.compatibleCount", { count: compatibleProviders.length })}</dd>
        </div>
        <div>
          <dt>{t("models.priority.primary")}</dt>
          <dd className={cn(!primaryProvider && "md-warn-text cp-tone-danger")}>
            {primaryProvider ? providerDisplayLabel(providers, primaryProvider) : t("models.priority.noPrimary")}
          </dd>
        </div>
      </dl>

      <PageNote icon="low_priority">{t("models.priority.help")}</PageNote>

      {providers.length === 0 ? (
        <div className="priority-manager-empty">
          <p>{t("models.priority.noProviders")}</p>
          <Button variant="primary" size="sm" icon="dns" onClick={goToProviders}>
            {t("models.priority.openProviders")}
          </Button>
        </div>
      ) : !loadingModels && compatibleProviders.length === 0 && items.length === 0 ? (
        <div className="priority-manager-empty">
          <p>
            {canonicalUpstream
              ? t("models.priority.noCompatible", { model: canonicalUpstream })
              : t("models.priority.noRoutesYet")}
          </p>
          <Button variant="primary" size="sm" icon="dns" onClick={goToProviders}>
            {t("models.priority.openProviders")}
          </Button>
        </div>
      ) : items.length === 0 ? (
        <div className="priority-manager-empty">
          <p>
            <span className="material-symbols-outlined animate-spin" aria-hidden>progress_activity</span>{" "}
            {t("models.priority.discovering", { model: canonicalUpstream })}
          </p>
        </div>
      ) : visibleItems.length === 0 ? (
        <div className="priority-manager-empty">
          <p>{t("models.priority.allHidden")}</p>
          <Button variant="primary" size="sm" icon="dns" onClick={goToProviders}>
            {t("models.priority.openProviders")}
          </Button>
        </div>
      ) : (
        <div className="md-pe-table" role="table" aria-label={t("models.priority.tableLabel")}>
          <div className="md-pe-head" role="row">
            <span role="columnheader">{t("models.priority.colOrder")}</span>
            <span role="columnheader">{t("models.priority.colProvider")}</span>
            <span role="columnheader">{t("models.priority.colHealth")}</span>
            <span role="columnheader">{t("models.priority.colEnabled")}</span>
            <span aria-hidden />
          </div>
          {visibleItems.map((route, visiblePosition) => {
            const index = items.indexOf(route);
            const enabledPosition = route.enabled ? enabledItems.findIndex((item) => item === route) + 1 : 0;
            const accountCount = credentialCounts[route.provider] ?? 0;
            const failover = failoverByProvider[route.provider];
            const pill = failoverPill(failover, t);
            const trippedOut = failover?.state === "open";
            const provider = providerFor(route.provider);
            const isNew = !savedProviders.has(route.provider);
            const status = provider?.status;
            return (
              <div
                className={cn(
                  "md-pe-row",
                  !route.enabled && "is-disabled",
                  trippedOut && "is-failed-over",
                  dragIndex === visiblePosition && "is-dragging",
                  dropIndex === visiblePosition && "is-drop-target",
                )}
                key={`${route.id || route.provider}-${index}`}
                role="row"
                {...dragHandlers(visiblePosition)}
              >
                <div className="md-pe-order" role="cell">
                  <button
                    type="button"
                    className="md-pe-handle"
                    draggable
                    onDragStart={(event) => {
                      event.dataTransfer.effectAllowed = "move";
                      event.dataTransfer.setData("text/plain", String(visiblePosition));
                      setDragIndex(visiblePosition);
                    }}
                    onDragEnd={() => {
                      setDragIndex(null);
                      setDropIndex(null);
                    }}
                    aria-label={t("models.priority.dragHint")}
                    title={t("models.priority.dragHint")}
                  >
                    <span className="material-symbols-outlined" aria-hidden>drag_indicator</span>
                  </button>
                  <span className={cn("md-pe-rank", enabledPosition === 1 && "is-primary", !enabledPosition && "is-off")}>
                    {route.enabled && enabledPosition > 0 ? `P${enabledPosition}` : t("models.step.off")}
                  </span>
                </div>
                <div className="md-pe-provider" role="cell">
                  <ProviderLogo className="cp-section-logo md-pe-logo" providerId={route.provider} providerType={provider?.type || route.provider} />
                  <div className="md-pe-provider-main">
                    <Select
                      value={route.provider}
                      onChange={(event) => updateRoute(index, { provider: event.target.value })}
                      aria-label={t("models.priority.colProvider")}
                    >
                      {providerOptionsForRoute(route.provider).map((option) => (
                        <option key={option.value} value={option.value}>
                          {option.label}
                        </option>
                      ))}
                    </Select>
                    <code title={t("models.priority.upstreamHint")}>{route.upstream_model || "—"}</code>
                  </div>
                </div>
                <div className="md-pe-health" role="cell">
                  <StatusPill tone={accountCount <= 0 ? "danger" : accountCount === 1 ? "warning" : "success"}>
                    {t("models.accountCount", { count: accountCount })}
                  </StatusPill>
                  {status && status !== "healthy" && status !== "unknown" ? (
                    <StatusPill tone={status === "auth_required" ? "danger" : "warning"} title={provider?.lastError}>
                      {t(`models.providerStatus.${status}`, { defaultValue: status })}
                    </StatusPill>
                  ) : null}
                  {pill ? (
                    <StatusPill tone={pill.tone} title={pill.title}>
                      {pill.label}
                    </StatusPill>
                  ) : null}
                  {isNew ? <StatusPill tone="info" title={t("models.stepHint.suggested", { provider: providerDisplayLabel(providers, route.provider) })}>{t("models.step.suggested")}</StatusPill> : null}
                  {trippedOut ? (
                    <LinkButton
                      disabled={resettingProvider === route.provider}
                      onClick={() => void onResetFailover(route.provider)}
                    >
                      {resettingProvider === route.provider ? t("models.priority.reenabling") : t("models.priority.reenable")}
                    </LinkButton>
                  ) : null}
                </div>
                <div className="md-pe-enabled" role="cell">
                  <Toggle
                    checked={route.enabled}
                    onChange={(event) => updateRoute(index, { enabled: event.target.checked })}
                    aria-label={t("models.priority.enableRoute", { provider: route.provider })}
                  />
                </div>
                <div className="md-pe-actions" role="cell">
                  <IconAction
                    icon="arrow_upward"
                    label={t("models.priority.moveUp")}
                    disabled={visiblePosition === 0}
                    onClick={() => moveVisible(visiblePosition, visiblePosition - 1)}
                  />
                  <IconAction
                    icon="arrow_downward"
                    label={t("models.priority.moveDown")}
                    disabled={visiblePosition === visibleItems.length - 1}
                    onClick={() => moveVisible(visiblePosition, visiblePosition + 1)}
                  />
                  <IconAction icon="delete" danger label={t("models.priority.removeRoute")} onClick={() => removeRoute(index)} />
                </div>
              </div>
            );
          })}
        </div>
      )}

      {hiddenCount > 0 ? <PageNote icon="visibility_off">{t("models.priority.hiddenNote", { count: hiddenCount })}</PageNote> : null}

      <div className="md-pe-footer">
        <ToolButton icon="dns" onClick={goToProviders}>
          {t("models.priority.manageProviders")}
        </ToolButton>
      </div>

      {modelsError ? <p className="field-error">{modelsError}</p> : null}
      {failoverError ? <p className="field-error">{failoverError}</p> : null}
      {validationError ? <p className="field-error">{validationError}</p> : null}
    </div>
  );
}
