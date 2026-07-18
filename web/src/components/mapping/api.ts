import type { MappingTier, ReasoningEffort } from "./codegen";

export type ClaudeMappingResponse = {
  defaults: Record<MappingTier, string>;
  env_defaults: Record<string, string>;
  overrides: Record<MappingTier, string>;
  reasoning_effort_overrides: Partial<Record<MappingTier, ReasoningEffort>>;
  effective_reasoning_effort: Partial<Record<MappingTier, ReasoningEffort>>;
  effective: Record<MappingTier, string>;
  effective_resolved: Record<string, { raw: string; resolved: string; route: "codex-bridge" | "claude-native" | "virtual-model" }>;
  default_codex_provider: string;
  placeholders: Array<{ name: string; role: string; resolves: string }>;
  content_mapping: Record<string, Record<string, string>>;
};

async function adminFetch<T>(secret: string, path: string, method: "GET" | "PUT" = "GET", body?: unknown) {
  const response = await fetch(path, {
    method,
    headers: {
      ...(secret ? { Authorization: `Bearer ${secret}` } : {}),
      ...(body ? { "Content-Type": "application/json" } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  const data = await response.json();
  if (!response.ok) {
    throw new Error(data?.error?.message || `HTTP ${response.status}`);
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
    default_codex_provider?: string;
  },
) {
  return adminFetch<ClaudeMappingResponse>(secret, "/api/admin/mapping/claude", "PUT", payload);
}
