import type { ApiKeyFormData, ApiKeyRecord, ProxyEndpoint } from "./types";

export function normalizeBaseUrl(origin: string): string {
  const trimmed = origin.replace(/\/+$/, "");
  return trimmed.endsWith("/v1") ? trimmed : `${trimmed}/v1`;
}

export function gatewayBaseUrl(): string {
  if (typeof window === "undefined") return "http://localhost:8080/v1";
  return normalizeBaseUrl(window.location.origin);
}

export function emptyApiKeyForm(): ApiKeyFormData {
  return {
    id: "",
    name: "",
    models: "*",
    enabled: true,
    team: "",
    endpoints: "",
    rpm: 0,
    streams: 0,
    max_input_bytes: 0,
    max_output_tokens: 0,
    media_jobs: 0,
    budget_usd_per_day: 0,
  };
}

export function apiKeyToForm(key: ApiKeyRecord): ApiKeyFormData {
  return {
    id: key.id,
    name: key.name,
    models: key.models?.length ? key.models.join(", ") : "*",
    enabled: key.enabled,
    team: key.policy?.team || "",
    endpoints: key.policy?.endpoints?.join(", ") || "",
    rpm: key.policy?.limits?.requests_per_minute || 0,
    streams: key.policy?.limits?.concurrent_streams || 0,
    max_input_bytes: Number(key.policy?.limits?.max_input_bytes || 0),
    max_output_tokens: key.policy?.limits?.max_output_tokens || 0,
    media_jobs: key.policy?.limits?.media_jobs || 0,
    budget_usd_per_day: key.policy?.limits?.budget_usd_per_day || 0,
  };
}

function parseList(value: string): string[] {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

function nonzero(value: number): number | undefined {
  return value > 0 ? value : undefined;
}

export function formToPayload(form: ApiKeyFormData, editing: boolean) {
  const models = parseList(form.models);
  const endpoints = parseList(form.endpoints);
  const limits = {
    requests_per_minute: nonzero(form.rpm),
    concurrent_streams: nonzero(form.streams),
    max_input_bytes: nonzero(form.max_input_bytes),
    max_output_tokens: nonzero(form.max_output_tokens),
    media_jobs: nonzero(form.media_jobs),
    budget_usd_per_day: nonzero(form.budget_usd_per_day),
  };
  const hasLimits = Object.values(limits).some((value) => value !== undefined);

  const payload: Record<string, unknown> = {
    name: form.name.trim(),
    models: models.length ? models : ["*"],
    enabled: form.enabled,
    policy: {
      ...(form.team.trim() ? { team: form.team.trim() } : {}),
      ...(endpoints.length ? { endpoints } : {}),
      ...(hasLimits ? { limits } : {}),
    },
  };

  if (!editing && form.id.trim()) {
    payload.id = form.id.trim();
  }

  return payload;
}

export function formatLimitSummary(key: ApiKeyRecord): string {
  const limits = key.policy?.limits;
  if (!limits) return "No limits";

  const parts: string[] = [];
  if (limits.requests_per_minute) parts.push(`${limits.requests_per_minute} req/min`);
  if (limits.concurrent_streams) parts.push(`${limits.concurrent_streams} streams`);
  if (limits.max_input_bytes) parts.push(`${limits.max_input_bytes} bytes in`);
  if (limits.max_output_tokens) parts.push(`${limits.max_output_tokens} tokens out`);
  if (limits.media_jobs) parts.push(`${limits.media_jobs} media jobs`);
  if (limits.budget_usd_per_day) parts.push(`$${limits.budget_usd_per_day}/day`);
  return parts.length ? parts.join(" · ") : "No limits";
}

type ExampleModelSource = {
  ID: string;
  Enabled?: boolean;
};

type ExampleComboSource = {
  id: string;
  enabled?: boolean;
};

/** Public model IDs exposed via Provider Priority Manager (and combo IDs). */
export function buildExampleModelOptions(
  models: ExampleModelSource[] | null | undefined,
  combos: ExampleComboSource[] | null | undefined,
): Array<{ value: string; label: string }> {
  const options: Array<{ value: string; label: string }> = [];

  for (const model of models || []) {
    if (!model.Enabled) continue;
    options.push({ value: model.ID, label: model.ID });
  }

  for (const combo of combos || []) {
    if (!combo.enabled) continue;
    options.push({ value: combo.id, label: combo.id });
  }

  return options.sort((a, b) => a.label.localeCompare(b.label));
}

export const PROXY_ENDPOINTS: ProxyEndpoint[] = [
  { path: "/v1/models", methods: ["GET"], description: "List virtual models available to the client." },
  { path: "/v1/models/info", methods: ["GET"], description: "Detailed model metadata and routing hints." },
  { path: "/v1/chat/completions", methods: ["POST"], description: "OpenAI-compatible chat completions.", capability: "text" },
  { path: "/v1/responses", methods: ["POST"], description: "OpenAI Responses API (Codex CLI wire_api=responses).", capability: "text" },
  { path: "/v1/responses/compact", methods: ["POST"], description: "Compact Responses API context (Codex).", capability: "text" },
  { path: "/v1/responses/ws", methods: ["GET"], description: "WebSocket transport for Responses API.", capability: "text" },
  { path: "/responses", methods: ["POST"], description: "Responses API alias (Codex-compatible).", capability: "text" },
  { path: "/codex/*", methods: ["POST"], description: "Codex CLI alias to Responses API.", capability: "text" },
  { path: "/v1/messages", methods: ["POST"], description: "Anthropic-compatible messages API.", capability: "text" },
  { path: "/v1/messages/count_tokens", methods: ["POST"], description: "Token counting for Anthropic messages.", capability: "text" },
  { path: "/v1/embeddings", methods: ["POST"], description: "Text embeddings.", capability: "embeddings" },
  { path: "/v1/images/generations", methods: ["POST"], description: "Image generation.", capability: "image" },
  { path: "/v1/images/edits", methods: ["POST"], description: "Image edits.", capability: "image" },
  { path: "/v1/audio/speech", methods: ["POST"], description: "Text-to-speech.", capability: "audio" },
  { path: "/v1/audio/transcriptions", methods: ["POST"], description: "Speech-to-text.", capability: "audio" },
  { path: "/v1/audio/voices", methods: ["GET", "POST"], description: "Voice catalog and management.", capability: "audio" },
  { path: "/v1/videos", methods: ["POST"], description: "Video generation (async job).", capability: "video" },
  { path: "/v1/videos/generations", methods: ["POST"], description: "Video generation alias.", capability: "video" },
  { path: "/v1/videos/{id}", methods: ["GET"], description: "Poll async video job status.", capability: "video" },
  { path: "/v1/search", methods: ["POST"], description: "Web search proxy.", capability: "search" },
  { path: "/v1/web/fetch", methods: ["POST"], description: "Fetch and extract web page content.", capability: "web" },
];

export function buildCurlExample(baseUrl: string, apiKey: string, model: string): string {
  const key = apiKey.trim() || "your-api-key";
  const modelId = model.trim() || "virtual-model-id";
  return [
    `curl ${baseUrl}/chat/completions \\`,
    `  -H "Authorization: Bearer ${key}" \\`,
    `  -H "Content-Type: application/json" \\`,
    `  -d '{"model":"${modelId}","messages":[{"role":"user","content":"Hello"}]}'`,
  ].join("\n");
}
