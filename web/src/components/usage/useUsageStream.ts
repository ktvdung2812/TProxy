import { useEffect, useRef } from "react";
import type { UsageStats } from "./api";
import { sameUsageLiveSnapshot, type UsageLiveSnapshot } from "./utils";

export type UsageLiveUpdate = Pick<UsageStats, "activeRequests" | "recentRequests" | "errorProvider">;

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

    const params = new URLSearchParams();
    if (secret) params.set("token", secret);
    const suffix = params.toString();
    const url = `/api/admin/usage/stream${suffix ? `?${suffix}` : ""}`;
    const source = new EventSource(url);

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

    return () => {
      lastSnapshotRef.current = null;
      source.close();
    };
  }, [secret, enabled]);
}
