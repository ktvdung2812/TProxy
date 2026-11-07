import { useTranslation } from "react-i18next";
import { useCallback, useEffect, useMemo, useState } from "react";
import { discoverProviderModels, type DiscoveredModel } from "../providers/api";
import type { ProviderOption } from "./types";

export function useProviderModelDiscovery(secret: string, providers: ProviderOption[], active = true) {
  const { t } = useTranslation();
  const [modelsByProvider, setModelsByProvider] = useState<Record<string, DiscoveredModel[]>>({});
  const [errorsByProvider, setErrorsByProvider] = useState<Record<string, string>>({});
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [reloadToken, setReloadToken] = useState(0);
  // True once the first discovery pass has finished, so callers can tell
  // "nothing discovered" apart from "not asked yet".
  const [settled, setSettled] = useState(false);

  const providerIds = useMemo(
    () => providers.map((provider) => provider.id).filter(Boolean),
    [providers],
  );
  // Callers rebuild the provider list on every snapshot; only a real change in
  // ids should trigger another round of discovery requests.
  const providerKey = providerIds.join("\n");

  useEffect(() => {
    if (!active || !secret || !providerKey) return;
    const ids = providerKey.split("\n");

    let cancelled = false;
    setLoading(true);
    setError(null);

    void (async () => {
      try {
        const failures: Record<string, string> = {};
        const entries = await Promise.all(
          ids.map(async (providerId) => {
            try {
              const result = await discoverProviderModels(secret, providerId);
              if (result.error?.message) failures[providerId] = result.error.message;
              return [providerId, result.data || []] as const;
            } catch (cause) {
              failures[providerId] = cause instanceof Error ? cause.message : t("featureB.errors.loadModels");
              return [providerId, []] as const;
            }
          }),
        );
        if (cancelled) return;
        setModelsByProvider(Object.fromEntries(entries));
        setErrorsByProvider(failures);
      } catch (loadError) {
        if (!cancelled) {
          setModelsByProvider({});
          setErrorsByProvider({});
          setError(loadError instanceof Error ? loadError.message : t("featureB.errors.loadSupportedModels"));
        }
      } finally {
        if (!cancelled) {
          setLoading(false);
          setSettled(true);
        }
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [active, secret, providerKey, reloadToken, t]);

  const reload = useCallback(() => setReloadToken((value) => value + 1), []);

  const ready = settled || !active || !secret || !providerKey;

  return { modelsByProvider, errorsByProvider, loading, ready, error, reload };
}
