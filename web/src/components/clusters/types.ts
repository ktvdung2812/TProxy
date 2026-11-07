/* ============================================================
   Cluster admin API shapes (GET/PUT/POST /api/admin/cluster…).
   Responses go through normalizeStatus & co. in utils.ts, so the
   types below are the defensive, fully-populated form: missing
   arrays are [], missing numbers 0 and missing strings "".
   ============================================================ */

export type ClusterRole = "standalone" | "hub" | "member";
export type LinkState = "connecting" | "connected" | "revoked" | "error" | "offline";
export type NodeStatus = "active" | "revoked" | "left";
export type BorrowMode = "fallback" | "shared";

export type LendingLimits = {
  requests_per_minute: number;
  concurrent_streams: number;
  max_input_bytes: number;
  budget_usd_per_day: number;
};

export type LendingPolicy = {
  enabled: boolean;
  /** Empty means every model shared in the cluster. */
  models: string[];
  limits: LendingLimits;
};

export type BorrowingPolicy = {
  enabled: boolean;
  mode: BorrowMode;
};

export type ClusterNodeOffer = {
  model_id: string;
  eligible_accounts: number;
  healthy: boolean;
};

export type ClusterNode = {
  id: string;
  name: string;
  owner: string;
  role: "hub" | "member";
  status: NodeStatus;
  online: boolean;
  self: boolean;
  version: string;
  last_seen_at: string;
  rtt_ms: number;
  lending_enabled: boolean;
  offers: ClusterNodeOffer[];
};

export type CatalogItem = { id: string; display_name: string };
export type CatalogKey = { id: string; name: string };
export type CatalogConflict = {
  /** model | combo | api_key | alias */
  kind: string;
  id: string;
  reason: string;
};

export type ClusterCatalog = {
  version: string;
  models: CatalogItem[];
  combos: CatalogItem[];
  api_keys: CatalogKey[];
  conflicts: CatalogConflict[];
};

export type ModelPrice = {
  input_per_million: number;
  output_per_million: number;
  reasoning_per_million: number;
  request: number;
};

export type SharedCatalog = {
  models: string[];
  combos: string[];
  api_keys: string[];
  prices: Record<string, ModelPrice>;
};

export type ClusterInfo = {
  id: string;
  name: string;
  hub_url: string;
  hub_node_id: string;
  joined_at: string;
  allow_insecure: boolean;
};

export type ClusterLink = {
  state: LinkState;
  connected_at: string;
  last_error: string;
  rtt_ms: number;
};

export type ClusterStatus = {
  role: ClusterRole;
  node: { id: string; name: string; version: string };
  /** null while standalone. */
  cluster: ClusterInfo | null;
  /** Only members have a link to the hub. */
  link: ClusterLink | null;
  lending: LendingPolicy;
  borrowing: BorrowingPolicy;
  nodes: ClusterNode[];
  /** null while standalone. */
  catalog: ClusterCatalog | null;
  /** Hub only: what the hub chose to share. */
  shared: SharedCatalog | null;
  /** Standalone only. */
  suggested_hub_urls: string[];
  warnings: string[];
};

export type JoinToken = {
  id: string;
  label: string;
  /** 0 means unlimited. */
  max_uses: number;
  uses: number;
  expires_at: string;
  created_at: string;
  /** "" while the token is active. */
  revoked_at: string;
};

export type CreatedJoinToken = {
  id: string;
  token: string;
  expires_at: string;
  command: string;
};

export type LedgerRow = {
  day: string;
  lender: string;
  lender_name: string;
  borrower: string;
  borrower_name: string;
  requests: number;
  input_tokens: number;
  output_tokens: number;
  cost_usd: number;
};

/* ---------------- Request bodies ---------------- */

export type CreateClusterBody = {
  name: string;
  hub_url: string;
  allow_insecure: boolean;
  lending: LendingPolicy;
};

export type JoinClusterBody = {
  token: string;
  node_name: string;
  owner: string;
  allow_insecure: boolean;
  lending: LendingPolicy;
};

export type CreateTokenBody = {
  label: string;
  ttl_hours: number;
  max_uses: number;
};

export type NodePatch = {
  name?: string;
  owner?: string;
};

/* ---------------- Snapshot slices the page reads ---------------- */

export type SnapshotModel = { ID: string; DisplayName?: string; Enabled?: boolean };
export type SnapshotCombo = { id: string; display_name?: string; enabled?: boolean };
export type SnapshotApiKey = { id: string; name?: string; enabled?: boolean };

/** Snapshot field listing the items a member received from the hub. */
export type ClusterManaged = {
  models?: string[] | null;
  combos?: string[] | null;
  api_keys?: string[] | null;
};

export type ModelOption = { id: string; label: string };
