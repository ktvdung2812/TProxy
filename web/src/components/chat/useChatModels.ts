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

export function useChatModels(secret: string, snapshot: SnapshotSlice) {
  const staticModels = useMemo(() => buildStaticModels(snapshot), [snapshot]);
  const [providerModels, setProviderModels] = useState<ChatModelOption[]>([]);
  const [loadingProviderModels, setLoadingProviderModels] = useState(false);
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
      setLoadingProviderModels(false);
      return;
    }

    let cancelled = false;

    async function loadProviderModels() {
      setLoadingProviderModels(true);
      setProviderError("");

      const staticIds = new Set(staticModels.map((model) => model.id));
      const discovered: ChatModelOption[] = [];

      const results = await Promise.allSettled(
        discoverableProviders.map(async (provider) => {
          const response = await discoverProviderModels(secret, provider.ID);
          return { provider, models: response.data || [] };
        }),
      );

      for (const result of results) {
        if (result.status !== "fulfilled") continue;
        const { provider, models } = result.value;
        const group = providerLabel(provider);

        for (const model of models) {
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
      }

      if (!cancelled) {
        setProviderModels(discovered.sort((a, b) => a.name.localeCompare(b.name)));
        const failed = results.filter((result) => result.status === "rejected").length;
        if (failed > 0 && discovered.length === 0 && staticModels.length === 0) {
          setProviderError("Failed to discover models from configured providers.");
        }
        setLoadingProviderModels(false);
      }
    }

    void loadProviderModels();

    return () => {
      cancelled = true;
    };
  }, [secret, discoverableProviders, staticModels]);

  const models = useMemo(
    () => [...staticModels, ...providerModels].sort((a, b) => {
      if (a.group === b.group) return a.name.localeCompare(b.name);
      const groupOrder = (group: string) => {
        if (group === "Virtual models") return 0;
        if (group === "Combos") return 1;
        return 2;
      };
      const orderDiff = groupOrder(a.group) - groupOrder(b.group);
      return orderDiff !== 0 ? orderDiff : a.group.localeCompare(b.group);
    }),
    [staticModels, providerModels],
  );

  return { models, loadingProviderModels, providerError };
}
