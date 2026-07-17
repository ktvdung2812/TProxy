import { useEffect } from "react";
import type { UsageActiveRequest, UsageRecentRequest, UsageStats } from "./api";

export type UsageLiveUpdate = Pick<UsageStats, "activeRequests" | "recentRequests" | "errorProvider">;

export function useUsageStream(
  secret: string,
  enabled: boolean,
  onUpdate: (update: UsageLiveUpdate) => void,
) {
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
        onUpdate({
          activeRequests: (data.activeRequests || []) as UsageActiveRequest[],
          recentRequests: (data.recentRequests || []) as UsageRecentRequest[],
          errorProvider: data.errorProvider || "",
        });
      } catch {
        // Ignore malformed SSE payloads.
      }
    };

    return () => source.close();
  }, [secret, enabled, onUpdate]);
}
