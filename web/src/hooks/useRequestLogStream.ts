import { useEffect } from "react";

export type RequestLog = {
  request_id: string;
  client_api_key_id?: string;
  method: string;
  path: string;
  status: number;
  latency_ms: number;
  error_code?: string;
  created_at: string;
};

export function useRequestLogStream(
  secret: string,
  enabled: boolean,
  onUpdate: (logs: RequestLog[]) => void,
) {
  useEffect(() => {
    if (!enabled) return undefined;

    const params = new URLSearchParams({ limit: "50" });
    if (secret) params.set("token", secret);
    const url = `/api/admin/logs/stream?${params.toString()}`;
    const source = new EventSource(url);

    source.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data) as { data?: RequestLog[] };
        onUpdate((data.data || []) as RequestLog[]);
      } catch {
        // Ignore malformed SSE payloads.
      }
    };

    return () => source.close();
  }, [secret, enabled, onUpdate]);
}
