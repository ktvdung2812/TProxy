import { useEffect, useMemo, useState } from "react";
import { discoverProviderModels } from "../providers/api";
import type { ChatModelOption } from "./types";

type Provider = {
  ID: string;
  Name: string;
  Enabled: boolean;
};

type VirtualModel = {
  ID: string;
  DisplayName: string;
  Enabled: boolean;
  Capabilities: string[];
};

type Combo = {
  id: string;
  display_name: string;
  enabled: boolean;
  capabilities: string[];
};

type Credential = {
  enabled: boolean;
};

type SnapshotSlice = {
  providers: Provider[] | null;
  credentials: Record<string, Credential[]>;
  models: VirtualModel[] | null;
  combos: Combo[];
};

function providerLabel(provider: Provider): string {
  return provider.Name?.trim() || provider.ID;
}

function formatProviderModelId(providerId: string, modelId: string): string {
  return `${providerId}:${modelId}`;
}

function buildStaticModels(snapshot: SnapshotSlice): ChatModelOption[] {
  const items: ChatModelOption[] = [];

  for (const model of snapshot.models || []) {
    if (!model.Enabled) continue;
    items.push({
      id: model.ID,
      name: model.DisplayName || model.ID,
      group: "Virtual models",
      capabilities: model.Capabilities || [],
    });
  }

  for (const combo of snapshot.combos || []) {
    if (!combo.enabled) continue;
    items.push({
      id: combo.id,
      name: combo.display_name || combo.id,
      group: "Combos",
      capabilities: combo.capabilities || [],
    });
  }

  return items;
}

function sortModels(items: ChatModelOption[]): ChatModelOption[] {
  return [...items].sort((a, b) => {
    if (a.group === b.group) return a.name.localeCompare(b.name);
    const groupOrder = (group: string) => {
      if (group === "Virtual models") return 0;
      if (group === "Combos") return 1;
      return 2;
    };
    const orderDiff = groupOrder(a.group) - groupOrder(b.group);
    return orderDiff !== 0 ? orderDiff : a.group.localeCompare(b.group);
  });
}

export function useChatModels(secret: string, snapshot: SnapshotSlice) {
  const staticModels = useMemo(() => buildStaticModels(snapshot), [snapshot]);
  const [providerModels, setProviderModels] = useState<ChatModelOption[]>([]);
  const [loadingProviderIds, setLoadingProviderIds] = useState<string[]>([]);
  const [providerError, setProviderError] = useState("");

  const discoverableProviders = useMemo(() => {
    return (snapshot.providers || []).filter((provider) => {
      if (!provider.Enabled) return false;
      const credentials = snapshot.credentials?.[provider.ID] || [];
      return credentials.some((credential) => credential.enabled);
    });
  }, [snapshot.providers, snapshot.credentials]);

  useEffect(() => {
    if (!secret || discoverableProviders.length === 0) {
      setProviderModels([]);
      setProviderError("");
      setLoadingProviderIds([]);
      return;
    }

    let cancelled = false;
    setProviderModels([]);
    setProviderError("");
    setLoadingProviderIds(discoverableProviders.map((provider) => provider.ID));

    const staticIds = new Set(staticModels.map((model) => model.id));
    let failures = 0;

    void Promise.all(
      discoverableProviders.map(async (provider) => {
        const group = providerLabel(provider);
        try {
          const response = await discoverProviderModels(secret, provider.ID);
          if (cancelled) return;
          if (response.error?.message) return;

          const discovered: ChatModelOption[] = [];
          for (const model of response.data || []) {
            const upstreamId = model.id?.trim();
            if (!upstreamId) continue;

            const id = formatProviderModelId(provider.ID, upstreamId);
            if (staticIds.has(id) || staticIds.has(upstreamId)) continue;

            discovered.push({
              id,
              name: model.name?.trim() || upstreamId,
              group,
              capabilities: model.capabilities || [],
              requestModel: id,
            });
            staticIds.add(id);
          }

          if (discovered.length > 0) {
            setProviderModels((current) => sortModels([...current, ...discovered]));
          }
        } catch {
          failures += 1;
        } finally {
          if (!cancelled) {
            setLoadingProviderIds((current) => current.filter((id) => id !== provider.ID));
          }
        }
      }),
    ).then(() => {
      if (cancelled) return;
      if (failures > 0 && staticModels.length === 0) {
        setProviderModels((current) => {
          if (current.length === 0) {
            setProviderError("Failed to discover models from configured providers.");
          }
          return current;
        });
      }
    });

    return () => {
      cancelled = true;
    };
  }, [secret, discoverableProviders, staticModels]);

  const models = useMemo(
    () => sortModels([...staticModels, ...providerModels]),
    [staticModels, providerModels],
  );

  const loadingProviderModels = loadingProviderIds.length > 0;

  return { models, staticModels, loadingProviderModels, loadingProviderIds, providerError };
}
