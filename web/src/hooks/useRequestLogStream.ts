import { useEffect, useRef } from "react";

export type RequestLog = {
  request_id: string;
  client_api_key_id?: string;
  method: string;
  path: string;
  protocol?: string;
  public_model_id?: string;
  provider_id?: string;
  credential_id?: string;
  attempt?: number;
  status: number;
  latency_ms: number;
  error_code?: string;
  metadata?: Record<string, unknown>;
  created_at: string;
};

const MAX_RECONNECT_DELAY_MS = 30_000;

export function useRequestLogStream(
  secret: string,
  enabled: boolean,
  onUpdate: (logs: RequestLog[]) => void,
) {
  const onUpdateRef = useRef(onUpdate);
  useEffect(() => {
    onUpdateRef.current = onUpdate;
  }, [onUpdate]);

  useEffect(() => {
    if (!enabled) return undefined;

    let controller: AbortController | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let reconnectDelay = 1000;
    let cancelled = false;

    function scheduleReconnect() {
      if (cancelled || reconnectTimer !== null) return;
      // Exponential backoff reconnection, capped at MAX_RECONNECT_DELAY_MS.
      reconnectTimer = setTimeout(() => {
        reconnectTimer = null;
        reconnectDelay = Math.min(reconnectDelay * 2, MAX_RECONNECT_DELAY_MS);
        void connect();
      }, reconnectDelay);
    }

    async function connect() {
      if (cancelled) return;
      controller?.abort();
      controller = new AbortController();
      try {
        const response = await fetch("/api/admin/logs/stream?limit=50", {
          headers: secret ? { Authorization: `Bearer ${secret}` } : {},
          signal: controller.signal,
        });
        if (!response.ok || !response.body) throw new Error(`HTTP ${response.status}`);
        reconnectDelay = 1000;
        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = "";
        while (!cancelled) {
          const { value, done } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          const frames = buffer.split(/\r?\n\r?\n/);
          buffer = frames.pop() || "";
          for (const frame of frames) {
            const dataLine = frame.split(/\r?\n/)
              .filter((line) => line.startsWith("data:"))
              .map((line) => line.slice(5).trimStart())
              .join("\n");
            if (!dataLine) continue;
            try {
              const data = JSON.parse(dataLine) as { data?: RequestLog[]; recentRequests?: RequestLog[] };
              onUpdateRef.current((data.data || data.recentRequests || []) as RequestLog[]);
            } catch {
              // Ignore malformed SSE payloads.
            }
          }
        }
      } catch {
        if (cancelled || controller.signal.aborted) return;
        scheduleReconnect();
        return;
      }
      if (!cancelled) scheduleReconnect();
    }

    void connect();

    return () => {
      cancelled = true;
      if (reconnectTimer !== null) clearTimeout(reconnectTimer);
      controller?.abort();
    };
  }, [secret, enabled]);
}
