import type { MappingTier, ReasoningEffort } from "./codegen";

export type ClaudeMappingResponse = {
  defaults: Record<MappingTier, string>;
  env_defaults: Record<string, string>;
  overrides: Record<MappingTier, string>;
  reasoning_effort_overrides: Partial<Record<MappingTier, ReasoningEffort>>;
  effective_reasoning_effort: Partial<Record<MappingTier, ReasoningEffort>>;
  effective: Record<MappingTier, string>;
  effective_resolved: Record<string, { raw: string; resolved: string; route: "codex-bridge" | "claude-native" | "virtual-model" }>;
  placeholders: Array<{ name: string; role: string; resolves: string }>;
  content_mapping: Record<string, Record<string, string>>;
};

export type CursorCatalogModel = {
  id: string;
  name: string;
};

export type CursorMappingResponse = {
  /** Known Cursor client model IDs for the source dropdown (live discovery when available). */
  cursor_models: CursorCatalogModel[];
  overrides: Record<string, string>;
  effective: Record<string, string>;
  placeholders: Array<{ name: string; role: string; label?: string; resolves: string }>;
  content_mapping: Record<string, Record<string, string>>;
  catalog_source?: "discovery" | "static" | "mixed" | string;
  catalog_count?: number;
  provider_id?: string;
  discovery_error?: string;
};

async function adminFetch<T>(secret: string, path: string, method: "GET" | "PUT" = "GET", body?: unknown) {
  const response = await fetch(path, {
    method,
    headers: {
      ...(secret ? { Authorization: `Bearer ${secret}` } : {}),
      ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  let data: any = null;
  const text = await response.text();
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    throw new Error(text?.slice(0, 200) || `HTTP ${response.status}`);
  }
  if (!response.ok) {
    throw new Error(data?.error?.message || data?.message || `HTTP ${response.status}`);
  }
  return data as T;
}

export function fetchClaudeMapping(secret: string) {
  return adminFetch<ClaudeMappingResponse>(secret, "/api/admin/mapping/claude");
}

export function saveClaudeMapping(
  secret: string,
  payload: {
    overrides: Record<string, string>;
    reasoning_effort_overrides?: Partial<Record<MappingTier, ReasoningEffort>>;
  },
) {
  return adminFetch<ClaudeMappingResponse>(secret, "/api/admin/mapping/claude", "PUT", payload);
}

export function fetchCursorMapping(secret: string, options?: { refresh?: boolean }) {
  const query = options?.refresh ? "?refresh=1" : "";
  return adminFetch<CursorMappingResponse>(secret, `/api/admin/mapping/cursor${query}`);
}

export function saveCursorMapping(secret: string, payload: { overrides: Record<string, string> }) {
  return adminFetch<CursorMappingResponse>(secret, "/api/admin/mapping/cursor", "PUT", payload);
}

export type ModelMappingRow = {
  source: string;
  target: string;
  effective: string;
};

export type ModelMappingResponse = {
  overrides: Record<string, string>;
  rows: ModelMappingRow[];
  content_mapping: Record<string, Record<string, string>>;
};

export type ModelMappingResolveResponse = {
  requested: string;
  resolved: string;
  steps: string[];
};

export function fetchModelMapping(secret: string) {
  return adminFetch<ModelMappingResponse>(secret, "/api/admin/mapping/models");
}

export function saveModelMapping(secret: string, payload: { overrides: Record<string, string> }) {
  return adminFetch<ModelMappingResponse>(secret, "/api/admin/mapping/models", "PUT", payload);
}

export async function testModelMapping(
  secret: string,
  model: string,
): Promise<ModelMappingResolveResponse> {
  const query = new URLSearchParams({ model }).toString();
  const response = await fetch(`/api/admin/mapping/models/resolve?${query}`, {
    headers: secret ? { Authorization: `Bearer ${secret}` } : {},
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(data?.error?.message || data?.message || `HTTP ${response.status}`);
  }
  return data as ModelMappingResolveResponse;
}
