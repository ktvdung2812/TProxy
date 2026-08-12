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

    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let reconnectDelay = 1000;
    let cancelled = false;
    let controller: AbortController | null = null;

    function scheduleReconnect() {
      if (cancelled) return;
      reconnectTimer = setTimeout(() => {
        reconnectDelay = Math.min(reconnectDelay * 2, MAX_RECONNECT_DELAY_MS);
        connect();
      }, reconnectDelay);
    }

    function applyEvent(event: string) {
      const data = event
        .split("\n")
        .filter((line) => line.startsWith("data:"))
        .map((line) => line.slice(5).trimStart())
        .join("\n");
      if (!data) return;

      try {
        const update = JSON.parse(data) as UsageLiveUpdate;
        const next = {
          activeRequests: update.activeRequests || [],
          recentRequests: update.recentRequests || [],
          errorProvider: update.errorProvider || "",
        };
        if (lastSnapshotRef.current && sameUsageLiveSnapshot(lastSnapshotRef.current, next)) return;
        lastSnapshotRef.current = next;
        onUpdateRef.current(next);
      } catch {
        // Ignore malformed SSE payloads; the next complete event can still be used.
      }
    }

    async function connect() {
      if (cancelled) return;
      controller = new AbortController();
      try {
        const response = await fetch("/api/admin/usage/stream", {
          headers: secret ? { Authorization: `Bearer ${secret}` } : undefined,
          signal: controller.signal,
        });
        if (!response.ok || !response.body) throw new Error(`usage stream failed: ${response.status}`);

        reconnectDelay = 1000;
        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let pending = "";
        while (!cancelled) {
          const { value, done } = await reader.read();
          if (done) break;
          pending += decoder.decode(value, { stream: true }).replace(/\r\n/g, "\n");
          let separator = pending.indexOf("\n\n");
          while (separator >= 0) {
            applyEvent(pending.slice(0, separator));
            pending = pending.slice(separator + 2);
            separator = pending.indexOf("\n\n");
          }
        }
      } catch (error) {
        if (!cancelled && !(error instanceof DOMException && error.name === "AbortError")) scheduleReconnect();
        return;
      }
      scheduleReconnect();
    }

    connect();

    return () => {
      cancelled = true;
      lastSnapshotRef.current = null;
      if (reconnectTimer !== null) clearTimeout(reconnectTimer);
      controller?.abort();
    };
  }, [secret, enabled]);
}
