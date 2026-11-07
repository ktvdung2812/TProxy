import type {
  BorrowingPolicy,
  ClusterStatus,
  CreateClusterBody,
  CreatedJoinToken,
  CreateTokenBody,
  JoinClusterBody,
  JoinToken,
  LedgerRow,
  LendingPolicy,
  NodePatch,
  SharedCatalog,
} from "./types";
import { normalizeCreatedToken, normalizeLedger, normalizeStatus, normalizeTokens } from "./utils";

/** Carries the API error envelope, so callers can map `code` to friendly text. */
export type ClusterApiError = Error & { status?: number; code?: string; requestId?: string };

type ErrorEnvelope = { error?: { type?: string; code?: string; message?: string; request_id?: string } };

async function adminFetch<T>(secret: string, path: string, method = "GET", body?: unknown, signal?: AbortSignal): Promise<T> {
  const headers: Record<string, string> = { ...(secret ? { Authorization: `Bearer ${secret}` } : {}) };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const response = await fetch(path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    signal,
  });
  const data = (await response.json().catch(() => ({}))) as T & ErrorEnvelope;
  if (!response.ok) {
    const error = new Error(data?.error?.message || `HTTP ${response.status}`) as ClusterApiError;
    error.status = response.status;
    error.code = data?.error?.code;
    error.requestId = data?.error?.request_id;
    throw error;
  }
  return data;
}

const BASE = "/api/admin/cluster";

/* ---------------- Status ---------------- */

export async function getClusterStatus(secret: string, signal?: AbortSignal): Promise<ClusterStatus> {
  return normalizeStatus(await adminFetch<unknown>(secret, BASE, "GET", undefined, signal));
}

/* ---------------- Setup ---------------- */

export function createCluster(secret: string, body: CreateClusterBody) {
  return adminFetch<unknown>(secret, `${BASE}/create`, "POST", body);
}

export function joinCluster(secret: string, body: JoinClusterBody) {
  return adminFetch<unknown>(secret, `${BASE}/join`, "POST", body);
}

/** Member only: disconnect from the hub and go back to standalone. */
export function leaveCluster(secret: string) {
  return adminFetch<unknown>(secret, `${BASE}/leave`, "POST");
}

/** Hub only: disconnect every member and delete the cluster. */
export function dissolveCluster(secret: string) {
  return adminFetch<unknown>(secret, `${BASE}/dissolve`, "POST");
}

/** Pull (member) or push (hub) the shared catalog now instead of waiting. */
export function syncCluster(secret: string) {
  return adminFetch<unknown>(secret, `${BASE}/sync`, "POST");
}

/* ---------------- This node ---------------- */

export function renameLocalNode(secret: string, name: string) {
  return adminFetch<unknown>(secret, `${BASE}/node`, "PUT", { name });
}

/* ---------------- Policies ---------------- */

export function updateLending(secret: string, policy: LendingPolicy) {
  return adminFetch<unknown>(secret, `${BASE}/lending`, "PUT", policy);
}

export function updateBorrowing(secret: string, policy: BorrowingPolicy) {
  return adminFetch<unknown>(secret, `${BASE}/borrowing`, "PUT", policy);
}

/* ---------------- Sharing (hub) ---------------- */

export function updateShared(secret: string, shared: SharedCatalog) {
  return adminFetch<unknown>(secret, `${BASE}/shared`, "PUT", shared);
}

/* ---------------- Join tokens (hub) ---------------- */

export async function listJoinTokens(secret: string, signal?: AbortSignal): Promise<JoinToken[]> {
  return normalizeTokens(await adminFetch<unknown>(secret, `${BASE}/tokens`, "GET", undefined, signal));
}

/** The response is the only time the token and its join command are visible. */
export async function createJoinToken(secret: string, body: CreateTokenBody): Promise<CreatedJoinToken> {
  return normalizeCreatedToken(await adminFetch<unknown>(secret, `${BASE}/tokens`, "POST", body));
}

export function revokeJoinToken(secret: string, id: string) {
  return adminFetch<unknown>(secret, `${BASE}/tokens/${encodeURIComponent(id)}`, "DELETE");
}

/* ---------------- Nodes (hub) ---------------- */

export function updateClusterNode(secret: string, id: string, patch: NodePatch) {
  return adminFetch<unknown>(secret, `${BASE}/nodes/${encodeURIComponent(id)}`, "PATCH", patch);
}

export function revokeClusterNode(secret: string, id: string) {
  return adminFetch<unknown>(secret, `${BASE}/nodes/${encodeURIComponent(id)}`, "DELETE");
}

/* ---------------- Ledger ---------------- */

export async function getClusterLedger(secret: string, days: 1 | 7, signal?: AbortSignal): Promise<LedgerRow[]> {
  return normalizeLedger(await adminFetch<unknown>(secret, `${BASE}/ledger?days=${days}`, "GET", undefined, signal));
}
