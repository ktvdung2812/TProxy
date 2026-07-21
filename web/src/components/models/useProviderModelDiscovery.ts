import { useEffect, useMemo, useState } from "react";
import { discoverProviderModels, type DiscoveredModel } from "../providers/api";
import type { ProviderOption } from "./types";

export function useProviderModelDiscovery(secret: string, providers: ProviderOption[], active = true) {
  const [modelsByProvider, setModelsByProvider] = useState<Record<string, DiscoveredModel[]>>({});
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const providerIds = useMemo(
    () => providers.map((provider) => provider.id).filter(Boolean),
    [providers],
  );

  useEffect(() => {
    if (!active || !secret || providerIds.length === 0) return;

    let cancelled = false;
    setLoading(true);
    setError(null);

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
      } catch (loadError) {
        if (!cancelled) {
          setModelsByProvider({});
          setError(loadError instanceof Error ? loadError.message : "Failed to load supported models");
        }
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [active, secret, providerIds]);

  return { modelsByProvider, loading, error };
}
