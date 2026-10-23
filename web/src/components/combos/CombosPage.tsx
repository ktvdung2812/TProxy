import { useMemo } from "react";
import { useChatModels } from "../chat/useChatModels";
import type { ModelRecord, RouteRecord } from "../models/types";
import { CombosView } from "./CombosView";
import { useComboStepOptions } from "./modelOptions";
import type { ComboModelOption, ComboRecord } from "./types";

export type CombosSnapshot = {
  providers: Array<{ ID: string; Name?: string; Enabled?: boolean }> | null;
  credentials: Record<string, Array<{ enabled: boolean }> | null> | null;
  models: ModelRecord[] | null;
  combos: ComboRecord[] | null;
  routes: Record<string, RouteRecord[] | null> | null;
};

type Props = {
  secret: string;
  snapshot: CombosSnapshot;
  loading?: boolean;
  onMutated: () => void;
  onNotice: (message: string) => void;
  onError: (message: string) => void;
};

/**
 * Route component for /combos. Lives outside App (like ModelsPage) so a
 * notice or snapshot reload re-renders it instead of remounting it, which
 * used to drop the search, filters, selection and open dialogs.
 */
export function CombosPage({ secret, snapshot, loading, onMutated, onNotice, onError }: Props) {
  const models = useMemo(() => snapshot.models || [], [snapshot.models]);
  const combos = useMemo(() => snapshot.combos || [], [snapshot.combos]);
  const routes = useMemo(() => {
    const mapped: Record<string, RouteRecord[]> = {};
    for (const [modelId, items] of Object.entries(snapshot.routes || {})) mapped[modelId] = items || [];
    return mapped;
  }, [snapshot.routes]);

  const comboModels = useMemo<ComboModelOption[]>(
    () =>
      models.map((model) => ({
        id: model.ID,
        label: model.DisplayName ? `${model.DisplayName} (${model.ID})` : model.ID,
        enabled: model.Enabled,
      })),
    [models],
  );

  const chatSnapshot = useMemo(
    () => ({
      providers: (snapshot.providers || []).map((provider) => ({
        ID: provider.ID,
        Name: provider.Name || provider.ID,
        Enabled: provider.Enabled !== false,
      })),
      credentials: Object.fromEntries(
        Object.entries(snapshot.credentials || {}).map(([providerId, items]) => [providerId, items || []]),
      ),
      models: models.map((model) => ({
        ID: model.ID,
        DisplayName: model.DisplayName,
        Enabled: model.Enabled,
        Capabilities: model.Capabilities,
      })),
      combos: combos.map((combo) => ({
        id: combo.id,
        display_name: combo.display_name || combo.id,
        enabled: combo.enabled !== false,
        capabilities: combo.capabilities || [],
      })),
    }),
    [snapshot.providers, snapshot.credentials, models, combos],
  );
  const { models: discoveredModels } = useChatModels(secret, chatSnapshot);
  const comboStepOptions = useComboStepOptions(models, combos, routes, discoveredModels);

  return (
    <CombosView
      secret={secret}
      combos={combos}
      models={comboModels}
      modelOptions={comboStepOptions}
      loading={loading}
      onMutated={onMutated}
      onNotice={onNotice}
      onError={onError}
    />
  );
}
