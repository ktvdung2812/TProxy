import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { fetchFailoverState, resetFailoverState, type FailoverState } from "./failoverApi";

const POLL_INTERVAL_MS = 10_000;

/**
 * Tracks which providers the router has taken out of this model's priority
 * chain. Polls while the Provider Priority Manager is open so an operator sees a
 * provider drop out — and come back — without reloading the page.
 */
export function useProviderFailover(secret: string, modelId: string | undefined, active: boolean) {
  const [states, setStates] = useState<FailoverState[]>([]);
  const [error, setError] = useState<string | null>(null);
  const cancelled = useRef(false);

  const load = useCallback(async () => {
    if (!secret || !modelId) return;
    try {
      const result = await fetchFailoverState(secret, modelId);
      if (!cancelled.current) {
        setStates(result);
        setError(null);
      }
    } catch (loadError) {
      if (!cancelled.current) {
        setError(loadError instanceof Error ? loadError.message : "Failed to load failover state");
      }
    }
  }, [secret, modelId]);

  useEffect(() => {
    cancelled.current = false;
    if (!active || !secret || !modelId) {
      setStates([]);
      return;
    }
    void load();
    const timer = window.setInterval(() => void load(), POLL_INTERVAL_MS);
    return () => {
      cancelled.current = true;
      window.clearInterval(timer);
    };
  }, [active, secret, modelId, load]);

  const byProvider = useMemo(() => {
    const map: Record<string, FailoverState> = {};
    for (const state of states) map[state.provider_id] = state;
    return map;
  }, [states]);

  const reset = useCallback(
    async (providerId: string) => {
      if (!secret || !modelId) return;
      await resetFailoverState(secret, modelId, providerId);
      await load();
    },
    [secret, modelId, load],
  );

  return { failoverByProvider: byProvider, failoverError: error, resetFailover: reset, reloadFailover: load };
}

/**
 * Failover state for every model at once, for the PPM overview: cards show a
 * paused provider without opening each model's priority editor.
 */
export function useFailoverOverview(secret: string) {
  const [states, setStates] = useState<FailoverState[]>([]);

  const load = useCallback(async () => {
    if (!secret) return;
    try {
      setStates(await fetchFailoverState(secret));
    } catch {
      // Older gateways without /api/admin/failover simply show no failover state.
      setStates([]);
    }
  }, [secret]);

  useEffect(() => {
    void load();
    const timer = window.setInterval(() => void load(), POLL_INTERVAL_MS * 3);
    return () => window.clearInterval(timer);
  }, [load]);

  const reset = useCallback(
    async (modelId: string, providerId: string) => {
      await resetFailoverState(secret, modelId, providerId);
      await load();
    },
    [secret, load],
  );

  return { failoverStates: states, reloadFailover: load, resetFailover: reset };
}
