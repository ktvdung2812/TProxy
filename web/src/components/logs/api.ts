/* Audit events API + types for the Logs view.
 *
 * Mirrors the Go struct `store.AuditEvent` returned by GET /api/admin/audit.
 */

export type AuditEvent = {
  id?: number;
  actor?: string;
  action: string;
  resource_type?: string;
  resource_id?: string;
  status: number;
  metadata?: Record<string, unknown>;
  created_at: string;
};

/** Fetch the most recent admin audit events (default limit 50). */
export async function fetchAuditEvents(
  secret: string,
  signal?: AbortSignal,
  limit = 50,
): Promise<AuditEvent[]> {
  const headers: Record<string, string> = {
    Accept: "application/json",
    ...(secret ? { Authorization: `Bearer ${secret}` } : {}),
  };
  const response = await fetch(`/api/admin/audit?limit=${limit}`, { headers, signal });
  if (!response.ok) {
    let message = `HTTP ${response.status}`;
    try {
      const body = await response.json();
      if (body?.error?.message) message = body.error.message;
    } catch {
      // ignore body parse failure
    }
    throw new Error(message);
  }
  const body = (await response.json()) as { data?: AuditEvent[] };
  return body.data ?? [];
}
