import { useEffect, useRef } from "react";
import type { UsageStats } from "./api";
import { sameUsageLiveSnapshot, type UsageLiveSnapshot } from "./utils";

export type UsageLiveUpdate = Pick<UsageStats, "activeRequests" | "recentRequests" | "errorProvider">;

const MAX_RECONNECT_DELAY_MS = 30_000;

export function useUsageStream(
  secret: string,
  enabled: boolean,
  onUpdate: (update: UsageLiveUpdate) => void,
) {
  const onUpdateRef = useRef(onUpdate);
  const lastSnapshotRef = useRef<UsageLiveSnapshot | null>(null);

  useEffect(() => {
    onUpdateRef.current = onUpdate;
  }, [onUpdate]);

  useEffect(() => {
    if (!enabled) return undefined;

    let source: EventSource | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let reconnectDelay = 1000;
    let cancelled = false;

    function connect() {
      if (cancelled) return;

      const params = new URLSearchParams();
      if (secret) params.set("token", secret);
      const suffix = params.toString();
      const url = `/api/admin/usage/stream${suffix ? `?${suffix}` : ""}`;
      source = new EventSource(url);

      source.onopen = () => {
        reconnectDelay = 1000; // reset on successful connection
      };

      source.onmessage = (event) => {
        try {
          const data = JSON.parse(event.data) as UsageLiveUpdate;
          const next = {
            activeRequests: data.activeRequests || [],
            recentRequests: data.recentRequests || [],
            errorProvider: data.errorProvider || "",
          };
          if (lastSnapshotRef.current && sameUsageLiveSnapshot(lastSnapshotRef.current, next)) {
            return;
          }
          lastSnapshotRef.current = next;
          onUpdateRef.current({
            activeRequests: next.activeRequests,
            recentRequests: next.recentRequests,
            errorProvider: next.errorProvider,
          });
        } catch {
          // Ignore malformed SSE payloads.
        }
      };

      source.onerror = () => {
        source?.close();
        source = null;
        if (cancelled) return;
        // Exponential backoff reconnection, capped at MAX_RECONNECT_DELAY_MS.
        reconnectTimer = setTimeout(() => {
          reconnectDelay = Math.min(reconnectDelay * 2, MAX_RECONNECT_DELAY_MS);
          connect();
        }, reconnectDelay);
      };
    }

    connect();

    return () => {
      cancelled = true;
      lastSnapshotRef.current = null;
      if (reconnectTimer !== null) clearTimeout(reconnectTimer);
      source?.close();
    };
  }, [secret, enabled]);
}
