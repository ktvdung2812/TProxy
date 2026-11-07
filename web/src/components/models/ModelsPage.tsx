import { useMemo } from "react";
import { managedSet } from "../clusters/utils";
import { ModelsView } from "./ModelsView";
import type { ModelRecord, ProviderOption, RouteRecord } from "./types";

export type ModelsSnapshot = {
  providers: Array<{ ID: string; Name?: string; Type: string; Enabled?: boolean; Status?: string; LastError?: string }> | null;
  credentials: Record<string, Array<{ enabled: boolean }> | null> | null;
  models: ModelRecord[] | null;
  routes: Record<string, RouteRecord[] | null> | null;
  /** Cluster members: ids of models received from the hub (read-only here). */
  cluster_managed?: { models?: string[] | null } | null;
};

type Props = {
  secret: string;
  snapshot: ModelsSnapshot;
  loading?: boolean;
  onMutated: () => void;
  onNotice: (message: string) => void;
  onError: (message: string) => void;
};

/**
 * Route component for /models. Lives outside App so notices and snapshot
 * reloads re-render it instead of remounting it (which dropped filters, the
 * open priority editor and re-ran provider discovery).
 */
export function ModelsPage({ secret, snapshot, loading, onMutated, onNotice, onError }: Props) {
  const providers = useMemo<ProviderOption[]>(
    () =>
      (snapshot.providers || []).map((provider) => ({
        id: provider.ID,
        label: provider.Name ? `${provider.Name} (${provider.ID})` : provider.ID,
        type: provider.Type,
        status: provider.Status,
        enabled: provider.Enabled,
        lastError: provider.LastError,
      })),
    [snapshot.providers],
  );

  const credentialCounts = useMemo(() => {
    const counts: Record<string, number> = {};
    for (const [providerId, items] of Object.entries(snapshot.credentials || {})) {
      counts[providerId] = (items || []).filter((item) => item.enabled).length;
    }
    return counts;
  }, [snapshot.credentials]);

  // Routes pointing at a provider that no longer exists are dropped, as before.
  const routesByModel = useMemo(() => {
    const known = new Set((snapshot.providers || []).map((provider) => provider.ID));
    const mapped: Record<string, RouteRecord[]> = {};
    for (const [modelId, routes] of Object.entries(snapshot.routes || {})) {
      mapped[modelId] = (routes || []).filter((route) => snapshot.cluster_managed?.models?.includes(modelId) || known.has(route.ProviderID)).map((route) => ({
        ID: route.ID,
        ProviderID: route.ProviderID,
        UpstreamModel: route.UpstreamModel,
        Priority: route.Priority,
        Weight: route.Weight,
        Enabled: route.Enabled,
      }));
    }
    return mapped;
  }, [snapshot.providers, snapshot.routes, snapshot.cluster_managed]);

  const models = useMemo(() => snapshot.models || [], [snapshot.models]);
  const managedModels = snapshot.cluster_managed?.models;
  const managedIds = useMemo(() => managedSet(managedModels), [managedModels]);

  return (
    <ModelsView
      secret={secret}
      models={models}
      routesByModel={routesByModel}
      providers={providers}
      credentialCounts={credentialCounts}
      managedIds={managedIds}
      loading={loading}
      onMutated={onMutated}
      onNotice={onNotice}
      onError={onError}
    />
  );
}
