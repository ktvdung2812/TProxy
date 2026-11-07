export type ModelRecord = {
  ID: string;
  DisplayName: string;
  Aliases: string[];
  Enabled: boolean;
  RewriteResponseModel?: boolean;
  Capabilities: string[];
  /** Snapshot-only fields the forms do not edit; a routes-only save sends them back unchanged. */
  ExposeUpstreamName?: boolean;
  Limits?: Record<string, unknown> | null;
  Policy?: Record<string, unknown> | null;
};

export type RouteRecord = {
  ID: string;
  ProviderID: string;
  UpstreamModel: string;
  Priority: number;
  Weight?: number;
  Enabled: boolean;
};

export type ProviderOption = {
  id: string;
  label: string;
  type: string;
  /** Provider health from the snapshot (healthy, degraded, auth_required…). */
  status?: string;
  enabled?: boolean;
  lastError?: string;
};

export type RouteFormData = {
  id: string;
  provider: string;
  upstream_model: string;
  priority: number;
  weight: number;
  enabled: boolean;
};

export type ModelFormData = {
  id: string;
  display_name: string;
  aliases: string;
  enabled: boolean;
  rewrite_response_model: boolean;
  capabilities: string[];
  routes: RouteFormData[];
};
