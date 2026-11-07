import { useEffect, useState } from "react";
import type { TFunction } from "i18next";
import type { Tone } from "../page-kit";
import type {
  BorrowingPolicy,
  CatalogItem,
  ClusterCatalog,
  ClusterNode,
  ClusterRole,
  ClusterStatus,
  CreatedJoinToken,
  JoinToken,
  LedgerRow,
  LendingLimits,
  LendingPolicy,
  LinkState,
  ModelOption,
  ModelPrice,
  SharedCatalog,
  SnapshotModel,
} from "./types";

/** Provider type the backend uses for cluster peers; the Clusters page shows them instead. */
export const CLUSTER_PROVIDER_TYPE = "tproxy-cluster";
export const POLL_INTERVAL_MS = 10_000;

/** Fast lookup over a snapshot cluster_managed id list (models, combos or api_keys). */
export function managedSet(ids?: string[] | null): ReadonlySet<string> {
  return new Set((ids || []).filter(Boolean));
}

/** Defaults offered when creating or joining: lend, but gently. */
export const DEFAULT_LENDING: LendingPolicy = {
  enabled: true,
  models: [],
  limits: { requests_per_minute: 20, concurrent_streams: 2, max_input_bytes: 0, budget_usd_per_day: 0 },
};

/* ============================================================
   Defensive readers — the API may omit arrays, numbers and
   whole objects (null while standalone), so every field read
   here falls back to an empty value of the right type.
   ============================================================ */
type Raw = Record<string, unknown>;

function obj(value: unknown): Raw | null {
  return value && typeof value === "object" && !Array.isArray(value) ? (value as Raw) : null;
}
function rec(value: unknown): Raw {
  return obj(value) ?? {};
}
function str(value: unknown): string {
  if (typeof value === "string") return value;
  return typeof value === "number" && Number.isFinite(value) ? String(value) : "";
}
function num(value: unknown): number {
  const parsed = typeof value === "number" ? value : typeof value === "string" && value.trim() ? Number(value) : Number.NaN;
  return Number.isFinite(parsed) ? parsed : 0;
}
function bool(value: unknown): boolean {
  return value === true;
}
function arr(value: unknown): unknown[] {
  return Array.isArray(value) ? value.filter((item) => item !== null && item !== undefined) : [];
}
function strings(value: unknown): string[] {
  return arr(value).map(str).filter(Boolean);
}

export function normalizeLimits(raw: unknown): LendingLimits {
  const limits = rec(raw);
  return {
    requests_per_minute: num(limits.requests_per_minute),
    concurrent_streams: num(limits.concurrent_streams),
    max_input_bytes: num(limits.max_input_bytes),
    budget_usd_per_day: num(limits.budget_usd_per_day),
  };
}

export function normalizeLending(raw: unknown): LendingPolicy {
  const lending = rec(raw);
  return { enabled: bool(lending.enabled), models: strings(lending.models), limits: normalizeLimits(lending.limits) };
}

export function normalizeBorrowing(raw: unknown): BorrowingPolicy {
  const borrowing = rec(raw);
  return { enabled: bool(borrowing.enabled), mode: borrowing.mode === "shared" ? "shared" : "fallback" };
}

function normalizeNode(raw: unknown): ClusterNode {
  const node = rec(raw);
  return {
    id: str(node.id),
    name: str(node.name),
    owner: str(node.owner),
    role: node.role === "hub" ? "hub" : "member",
    status: node.status === "revoked" || node.status === "left" ? node.status : "active",
    online: bool(node.online),
    self: bool(node.self),
    version: str(node.version),
    last_seen_at: str(node.last_seen_at),
    rtt_ms: num(node.rtt_ms),
    lending_enabled: bool(node.lending_enabled),
    offers: arr(node.offers)
      .map((value) => {
        const offer = rec(value);
        return { model_id: str(offer.model_id), eligible_accounts: num(offer.eligible_accounts), healthy: bool(offer.healthy) };
      })
      .filter((offer) => offer.model_id),
  };
}

function catalogItem(value: unknown): CatalogItem {
  const item = rec(value);
  return { id: str(item.id), display_name: str(item.display_name) };
}

function normalizeCatalog(raw: unknown): ClusterCatalog | null {
  const catalog = obj(raw);
  if (!catalog) return null;
  return {
    version: str(catalog.version),
    models: arr(catalog.models).map(catalogItem).filter((item) => item.id),
    combos: arr(catalog.combos).map(catalogItem).filter((item) => item.id),
    api_keys: arr(catalog.api_keys)
      .map((value) => {
        const key = rec(value);
        return { id: str(key.id), name: str(key.name) };
      })
      .filter((key) => key.id),
    conflicts: arr(catalog.conflicts).map((value) => {
      const conflict = rec(value);
      return { kind: str(conflict.kind), id: str(conflict.id), reason: str(conflict.reason) };
    }),
  };
}

export function normalizePrice(raw: unknown): ModelPrice {
  const price = rec(raw);
  return {
    input_per_million: num(price.input_per_million),
    output_per_million: num(price.output_per_million),
    reasoning_per_million: num(price.reasoning_per_million),
    request: num(price.request),
  };
}

export function normalizeShared(raw: unknown): SharedCatalog | null {
  const shared = obj(raw);
  if (!shared) return null;
  const prices: Record<string, ModelPrice> = {};
  for (const [id, value] of Object.entries(rec(shared.prices))) {
    if (id) prices[id] = normalizePrice(value);
  }
  return { models: strings(shared.models), combos: strings(shared.combos), api_keys: strings(shared.api_keys), prices };
}

export function normalizeStatus(raw: unknown): ClusterStatus {
  const status = rec(raw);
  const role: ClusterRole = status.role === "hub" || status.role === "member" ? status.role : "standalone";
  const node = rec(status.node);
  const cluster = obj(status.cluster);
  const link = obj(status.link);
  return {
    role,
    node: { id: str(node.id), name: str(node.name), version: str(node.version) },
    cluster: cluster
      ? {
          id: str(cluster.id),
          name: str(cluster.name),
          hub_url: str(cluster.hub_url),
          hub_node_id: str(cluster.hub_node_id),
          joined_at: str(cluster.joined_at),
          allow_insecure: bool(cluster.allow_insecure),
        }
      : null,
    link: link
      ? { state: link.state === "connecting" || link.state === "connected" || link.state === "revoked" || link.state === "error" ? link.state : "offline", connected_at: str(link.connected_at), last_error: str(link.last_error), rtt_ms: num(link.rtt_ms) }
      : null,
    lending: normalizeLending(status.lending),
    borrowing: normalizeBorrowing(status.borrowing),
    nodes: arr(status.nodes).map(normalizeNode).filter((item) => item.id),
    catalog: normalizeCatalog(status.catalog),
    shared: normalizeShared(status.shared),
    suggested_hub_urls: strings(status.suggested_hub_urls),
    warnings: strings(status.warnings),
  };
}

export function normalizeTokens(raw: unknown): JoinToken[] {
  return arr(rec(raw).tokens)
    .map((value) => {
      const token = rec(value);
      return {
        id: str(token.id),
        label: str(token.label),
        max_uses: num(token.max_uses),
        uses: num(token.uses),
        expires_at: str(token.expires_at),
        created_at: str(token.created_at),
        revoked_at: str(token.revoked_at),
      };
    })
    .filter((token) => token.id);
}

export function normalizeCreatedToken(raw: unknown): CreatedJoinToken {
  const created = rec(raw);
  return { id: str(created.id), token: str(created.token), expires_at: str(created.expires_at), command: str(created.command) };
}

export function normalizeLedger(raw: unknown): LedgerRow[] {
  return arr(rec(raw).rows).map((value) => {
    const row = rec(value);
    return {
      day: str(row.day),
      lender: str(row.lender),
      lender_name: str(row.lender_name),
      borrower: str(row.borrower),
      borrower_name: str(row.borrower_name),
      requests: num(row.requests),
      input_tokens: num(row.input_tokens),
      output_tokens: num(row.output_tokens),
      cost_usd: num(row.cost_usd),
    };
  });
}

/* ============================================================
   Status → tone
   ============================================================ */
const LINK_TONE: Record<LinkState, Tone> = {
  connected: "success",
  connecting: "info",
  offline: "warning",
  error: "warning",
  revoked: "danger",
};

export const LINK_STATES = Object.keys(LINK_TONE) as LinkState[];

export function linkTone(state: string): Tone {
  return LINK_TONE[state as LinkState] ?? "warning";
}

export type NodeState = "online" | "offline" | "revoked" | "left";

export const NODE_TONE: Record<NodeState, Tone> = {
  online: "success",
  offline: "warning",
  revoked: "danger",
  left: "neutral",
};

export function nodeState(node: Pick<ClusterNode, "status" | "online">): NodeState {
  if (node.status === "revoked") return "revoked";
  if (node.status === "left") return "left";
  return node.online ? "online" : "offline";
}

export type TokenState = "active" | "expired" | "usedUp" | "revoked";

export const TOKEN_TONE: Record<TokenState, Tone> = {
  active: "success",
  expired: "muted",
  usedUp: "neutral",
  revoked: "danger",
};

export function tokenState(token: JoinToken, now = Date.now()): TokenState {
  if (token.revoked_at) return "revoked";
  if (token.max_uses > 0 && token.uses >= token.max_uses) return "usedUp";
  const expires = Date.parse(token.expires_at);
  if (Number.isFinite(expires) && expires <= now) return "expired";
  return "active";
}

/* ============================================================
   Errors and warnings → friendly text
   ============================================================ */
export const CLUSTER_ERROR_CODES = [
  "cluster_not_configured",
  "cluster_already_configured",
  "invalid_join_token",
  "join_token_expired",
  "hub_unreachable",
  "insecure_hub_url",
  "master_key_required",
  "cluster_managed",
  "cluster_lending_disabled",
  "cluster_node_unknown",
  "cluster_node_offline",
  "cluster_relay_failed",
  "cluster_unauthorized",
  "cluster_node_revoked",
  "request_too_large",
  "invalid_request",
  "not_found",
] as const;

export function isAbortError(error: unknown) {
  return error instanceof DOMException && error.name === "AbortError";
}

/** Known error codes get friendly text (plus the server detail); anything else shows the raw message. */
export function clusterErrorMessage(error: unknown, t: TFunction, fallback: string): string {
  const code = typeof (error as { code?: unknown } | null)?.code === "string" ? (error as { code: string }).code : "";
  const raw = error instanceof Error ? error.message.trim() : "";
  if ((CLUSTER_ERROR_CODES as readonly string[]).includes(code)) {
    const friendly = t(`clusters.errors.${code}`);
    return raw && raw !== code && !/^HTTP \d+$/.test(raw) ? t("clusters.errors.withDetail", { message: friendly, detail: raw }) : friendly;
  }
  return raw || fallback;
}

export const KNOWN_WARNINGS = ["master_key_missing", "hub_listens_on_loopback", "insecure_hub_url", "hub_unreachable"] as const;

export function warningText(code: string, t: TFunction) {
  return (KNOWN_WARNINGS as readonly string[]).includes(code) ? t(`clusters.warnings.${code}`) : t("clusters.warnings.unknown", { code });
}

/* ============================================================
   Small helpers shared by the forms
   ============================================================ */
export function isPlainHttp(url: string) {
  return /^http:\/\//i.test(url.trim());
}

export function isHttpUrl(url: string) {
  return /^https?:\/\/[^\s/]+/i.test(url.trim());
}

/** HTTPS suggestions first — they work without the insecure opt-in. */
export function preferredHubUrl(urls: string[]) {
  return urls.find((url) => /^https:\/\//i.test(url)) || urls[0] || "";
}

/** Whole non-negative number from a text field ("" → 0). */
export function toCount(value: string) {
  const parsed = Math.floor(Number(value));
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

/** Non-negative amount (money) from a text field. */
export function toAmount(value: string) {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

export function cleanLending(policy: LendingPolicy): LendingPolicy {
  return {
    enabled: policy.enabled,
    models: [...new Set(policy.models.map((id) => id.trim()).filter(Boolean))],
    limits: {
      requests_per_minute: toCount(String(policy.limits.requests_per_minute)),
      concurrent_streams: toCount(String(policy.limits.concurrent_streams)),
      max_input_bytes: toCount(String(policy.limits.max_input_bytes)),
      budget_usd_per_day: toAmount(String(policy.limits.budget_usd_per_day)),
    },
  };
}

/** Models a node can lend: the cluster catalog plus this node's own public models. */
export function buildModelOptions(status: ClusterStatus | undefined, models: SnapshotModel[]): ModelOption[] {
  const labels = new Map<string, string>();
  for (const item of status?.catalog?.models || []) labels.set(item.id, item.display_name || item.id);
  for (const id of status?.shared?.models || []) if (!labels.has(id)) labels.set(id, id);
  for (const model of models) if (model?.ID && !labels.has(model.ID)) labels.set(model.ID, model.DisplayName || model.ID);
  return [...labels].map(([id, label]) => ({ id, label })).sort((left, right) => left.id.localeCompare(right.id));
}

export function fmtBytes(bytes: number, language: string) {
  if (!bytes) return "";
  const units = ["byte", "kilobyte", "megabyte", "gigabyte"] as const;
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return new Intl.NumberFormat(language, { style: "unit", unit: units[unit], unitDisplay: "short", maximumFractionDigits: 1 }).format(value);
}

export function fmtInt(value: number, language: string) {
  return new Intl.NumberFormat(language, { maximumFractionDigits: 0 }).format(value || 0);
}

/** Cluster costs are often fractions of a cent, so small amounts keep four decimals. */
export function fmtUsd(value: number, language: string) {
  const amount = value || 0;
  return new Intl.NumberFormat(language, {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: 2,
    maximumFractionDigits: amount > 0 && amount < 1 ? 4 : 2,
  }).format(amount);
}

export function fmtDateTime(value: string, language: string) {
  const time = Date.parse(value);
  return Number.isFinite(time) && time > 0 ? new Date(time).toLocaleString(language) : "";
}

/* ============================================================
   Ledger
   ============================================================ */
export type LedgerTotals = { requests: number; input_tokens: number; output_tokens: number; cost_usd: number };

export type LedgerPair = LedgerTotals & {
  key: string;
  lender: string;
  lenderName: string;
  borrower: string;
  borrowerName: string;
};

const emptyTotals = (): LedgerTotals => ({ requests: 0, input_tokens: 0, output_tokens: 0, cost_usd: 0 });

function addTo(target: LedgerTotals, row: LedgerTotals) {
  target.requests += row.requests;
  target.input_tokens += row.input_tokens;
  target.output_tokens += row.output_tokens;
  target.cost_usd += row.cost_usd;
}

/** One line per lender → borrower pair, summed across the days in the window, busiest first. */
export function aggregateLedger(rows: LedgerRow[]): LedgerPair[] {
  const pairs = new Map<string, LedgerPair>();
  for (const row of rows) {
    const key = `${row.lender}\u0000${row.borrower}`;
    let pair = pairs.get(key);
    if (!pair) {
      pair = { key, lender: row.lender, lenderName: row.lender_name || row.lender, borrower: row.borrower, borrowerName: row.borrower_name || row.borrower, ...emptyTotals() };
      pairs.set(key, pair);
    }
    addTo(pair, row);
  }
  return [...pairs.values()].sort((left, right) => right.requests - left.requests || right.cost_usd - left.cost_usd || left.key.localeCompare(right.key));
}

export function ledgerTotals(rows: LedgerTotals[]): LedgerTotals {
  const totals = emptyTotals();
  for (const row of rows) addTo(totals, row);
  return totals;
}

/** What this node lent to and borrowed from the rest of the cluster. */
export function nodeTraffic(rows: LedgerRow[], nodeId: string) {
  const lent = emptyTotals();
  const borrowed = emptyTotals();
  if (!nodeId) return { lent, borrowed };
  for (const row of rows) {
    if (row.lender === nodeId) addTo(lent, row);
    if (row.borrower === nodeId) addTo(borrowed, row);
  }
  return { lent, borrowed };
}

/* ============================================================
   Draft state for forms fed by a polled server value
   ============================================================ */
/**
 * Holds an editable copy of a server value. Polling adopts new server values
 * only while the copy is untouched, so a 10 s refresh never wipes an edit in
 * progress; markSaved() accepts the current copy as the new baseline.
 */
export function useDraft<T>(server: T) {
  const serverKey = JSON.stringify(server);
  const [draft, setDraft] = useState<T>(server);
  const [base, setBase] = useState(serverKey);
  const draftKey = JSON.stringify(draft);
  const dirty = draftKey !== base;

  useEffect(() => {
    if (serverKey === base || dirty) return;
    setDraft(JSON.parse(serverKey) as T);
    setBase(serverKey);
    // Only a new server value should trigger adoption.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [serverKey]);

  return {
    draft,
    setDraft,
    dirty,
    reset: () => {
      setDraft(JSON.parse(serverKey) as T);
      setBase(serverKey);
    },
    markSaved: () => setBase(draftKey),
  };
}
