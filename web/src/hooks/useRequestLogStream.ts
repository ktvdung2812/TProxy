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

    let source: EventSource | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let reconnectDelay = 1000;
    let cancelled = false;

    function connect() {
      if (cancelled) return;

      const params = new URLSearchParams({ limit: "50" });
      if (secret) params.set("token", secret);
      const url = `/api/admin/logs/stream?${params.toString()}`;
      source = new EventSource(url);

      source.onopen = () => {
        reconnectDelay = 1000; // reset on successful connection
      };

      source.onmessage = (event) => {
        try {
          const data = JSON.parse(event.data) as { data?: RequestLog[] };
          onUpdateRef.current((data.data || []) as RequestLog[]);
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
      if (reconnectTimer !== null) clearTimeout(reconnectTimer);
      source?.close();
    };
  }, [secret, enabled]);
}
