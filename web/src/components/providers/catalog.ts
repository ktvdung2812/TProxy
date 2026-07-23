/* ============================================================
   Provider catalog — static metadata for tdproxy provider types.
   Ported in spirit from 9router's constants/providers.js, but scoped to the
   types tdproxy's Go backend actually supports (internal/config/config.go:566):
   openai-compatible, anthropic-compatible, gemini, vertex, ollama, codex,
   claude, kimi, xai, antigravity, tavily, elevenlabs, image, video, plugin-http.
   ============================================================ */

import type { Provider } from "./types";
import type { NinerouterPreset } from "./api";
import { findPresetCatalogEntry } from "./ninerouterCatalog";

export type ProviderCategory = "oauth" | "apikey" | "media" | "plugin";

/** Section placement on the providers list page (9router-style). */
export type ProviderListSection = "custom" | "oauth" | "freeTier" | "apikey" | "media" | "plugin";

export type ProviderTypeInfo = {
  type: string;
  name: string;
  icon: string;
  textIcon: string;
  category: ProviderCategory;
  listSection: ProviderListSection;
  /** When set, this catalog row maps to a 9router preset id (e.g. glm, deepseek). */
  presetId?: string;
  /** CSS color for the provider icon tile background. */
  color: string;
  description: string;
  website?: string;
  apiKeyUrl?: string;
  /** Default auth_type this provider uses when adding a credential. */
  defaultAuthType: "api_key" | "oauth" | "service_account" | "none";
  /** Whether this type supports OAuth (has OAuth defaults in backend). */
  supportsOAuth: boolean;
  /** Provider works without credentials (e.g. local Ollama). */
  noAuth?: boolean;
  /** Default base URL applied by backend ApplyProviderDefaults (for reference). */
  defaultBaseUrl?: string;
};

/* The authoritative allowlist — must match internal/config/config.go:566-570 */
export const ALL_PROVIDER_TYPES = [
  "openai-compatible",
  "anthropic-compatible",
  "gemini",
  "vertex",
  "ollama",
  "codex",
  "claude",
  "kimi",
  "xai",
  "antigravity",
  "tavily",
  "elevenlabs",
  "image",
  "video",
  "plugin-http",
  "copilot",
  "vertex-partner",
  "qwen",
  "kiro",
  "qoder",
  "cursor",
  "cline",
  "clinepass",
  "iflow",
  "codebuddy-cn",
  "kilocode",
  "gitlab",
  "kimchi",
] as const;

const CATALOG: Record<string, ProviderTypeInfo> = {
  "openai-compatible": {
    type: "openai-compatible",
    name: "OpenAI Compatible",
    icon: "smart_toy",
    textIcon: "OA",
    category: "apikey",
    listSection: "custom",
    color: "#10a37f",
    description: "Any OpenAI Chat/Responses-compatible endpoint. Bring your own base URL + key.",
    website: "https://platform.openai.com/",
    apiKeyUrl: "https://platform.openai.com/api-keys",
    defaultAuthType: "api_key",
    supportsOAuth: false,
  },
  "anthropic-compatible": {
    type: "anthropic-compatible",
    name: "Anthropic Compatible",
    icon: "psychology",
    textIcon: "AN",
    category: "apikey",
    listSection: "custom",
    color: "#d97757",
    description: "Any Anthropic Messages-compatible endpoint. Bring your own base URL + key.",
    website: "https://www.anthropic.com/",
    apiKeyUrl: "https://console.anthropic.com/settings/keys",
    defaultAuthType: "api_key",
    supportsOAuth: false,
  },
  gemini: {
    type: "gemini",
    name: "Google Gemini",
    icon: "auto_awesome",
    textIcon: "GE",
    category: "apikey",
    listSection: "freeTier",
    color: "#4285f4",
    description: "Google Gemini API key access (Generative Language API).",
    website: "https://ai.google.dev/",
    apiKeyUrl: "https://aistudio.google.com/app/apikey",
    defaultAuthType: "api_key",
    supportsOAuth: false,
    defaultBaseUrl: "https://generativelanguage.googleapis.com",
  },
  vertex: {
    type: "vertex",
    name: "Vertex AI",
    icon: "hub",
    textIcon: "VX",
    category: "apikey",
    listSection: "apikey",
    color: "#34a853",
    description: "Google Cloud Vertex AI via OAuth/service account.",
    website: "https://cloud.google.com/vertex-ai",
    defaultAuthType: "service_account",
    supportsOAuth: false,
  },
  ollama: {
    type: "ollama",
    name: "Ollama",
    icon: "memory",
    textIcon: "OL",
    category: "apikey",
    listSection: "freeTier",
    color: "#ffffff",
    description: "Local Ollama daemon — no key required by default.",
    website: "https://ollama.com/",
    defaultAuthType: "none",
    supportsOAuth: false,
    noAuth: true,
    defaultBaseUrl: "http://127.0.0.1:11434",
  },
  codex: {
    type: "codex",
    name: "OpenAI Codex",
    icon: "code",
    textIcon: "CX",
    category: "oauth",
    listSection: "oauth",
    color: "#10a37f",
    description: "ChatGPT/Codex via OpenAI device-code OAuth flow.",
    website: "https://openai.com/codex/",
    defaultAuthType: "oauth",
    supportsOAuth: true,
    defaultBaseUrl: "https://chatgpt.com/backend-api/codex",
  },
  claude: {
    type: "claude",
    name: "Claude Code",
    icon: "psychology",
    textIcon: "CL",
    category: "oauth",
    listSection: "oauth",
    color: "#d97757",
    description: "Claude via Anthropic OAuth (claude.ai) browser PKCE flow.",
    website: "https://www.anthropic.com/claude",
    defaultAuthType: "oauth",
    supportsOAuth: true,
    defaultBaseUrl: "https://api.anthropic.com",
  },
  kimi: {
    type: "kimi",
    name: "Moonshot Kimi",
    icon: "dark_mode",
    textIcon: "KI",
    category: "oauth",
    listSection: "oauth",
    color: "#6366f1",
    description: "Kimi via Moonshot device-code OAuth flow (RFC 8628).",
    website: "https://kimi.moonshot.cn/",
    defaultAuthType: "oauth",
    supportsOAuth: true,
  },
  xai: {
    type: "xai",
    name: "xAI Grok",
    icon: "rocket_launch",
    textIcon: "XA",
    category: "oauth",
    listSection: "oauth",
    color: "#0f0f0f",
    description: "Grok via xAI OAuth (Grok CLI subscription or api.x.ai developer API).",
    website: "https://x.ai/",
    defaultAuthType: "oauth",
    supportsOAuth: true,
  },
  antigravity: {
    type: "antigravity",
    name: "Google Antigravity",
    icon: "travel_explore",
    textIcon: "AG",
    category: "oauth",
    listSection: "oauth",
    color: "#4285f4",
    description: "Google Antigravity via Google OAuth (requires client secret).",
    website: "https://antigravity.google/",
    defaultAuthType: "oauth",
    supportsOAuth: true,
  },
  tavily: {
    type: "tavily",
    name: "Tavily Search",
    icon: "travel_explore",
    textIcon: "TV",
    category: "apikey",
    listSection: "apikey",
    color: "#2563eb",
    description: "Tavily web search API.",
    website: "https://tavily.com/",
    apiKeyUrl: "https://app.tavily.com/api-key",
    defaultAuthType: "api_key",
    supportsOAuth: false,
  },
  elevenlabs: {
    type: "elevenlabs",
    name: "ElevenLabs",
    icon: "graphic_eq",
    textIcon: "EL",
    category: "apikey",
    listSection: "apikey",
    color: "#111827",
    description: "ElevenLabs text-to-speech / audio API.",
    website: "https://elevenlabs.io/",
    apiKeyUrl: "https://elevenlabs.io/app/settings/api-keys",
    defaultAuthType: "api_key",
    supportsOAuth: false,
  },
  image: {
    type: "image",
    name: "Image Generation",
    icon: "image",
    textIcon: "IM",
    category: "media",
    listSection: "media",
    color: "#ec4899",
    description: "OpenAI-compatible image generation endpoint.",
    defaultAuthType: "api_key",
    supportsOAuth: false,
  },
  video: {
    type: "video",
    name: "Video Generation",
    icon: "movie",
    textIcon: "VD",
    category: "media",
    listSection: "media",
    color: "#8b5cf6",
    description: "OpenAI-compatible video generation endpoint.",
    defaultAuthType: "api_key",
    supportsOAuth: false,
  },
  "plugin-http": {
    type: "plugin-http",
    name: "HTTP Plugin",
    icon: "extension",
    textIcon: "PL",
    category: "plugin",
    listSection: "plugin",
    color: "#64748b",
    description: "Arbitrary HTTP plugin transport (requires plugins enabled).",
    defaultAuthType: "api_key",
    supportsOAuth: false,
  },
  copilot: {
    type: "copilot",
    name: "GitHub Copilot",
    icon: "smart_toy",
    textIcon: "CP",
    category: "oauth",
    listSection: "oauth",
    color: "#24292f",
    description: "GitHub Copilot via OAuth and Copilot API token exchange.",
    website: "https://github.com/features/copilot",
    defaultAuthType: "oauth",
    supportsOAuth: true,
    defaultBaseUrl: "https://api.githubcopilot.com",
  },
  "vertex-partner": {
    type: "vertex-partner",
    name: "Vertex Partner",
    icon: "cloud",
    textIcon: "VP",
    category: "apikey",
    listSection: "apikey",
    color: "#4285f4",
    description: "Vertex partner OpenAI-compatible endpoint with service account auth.",
    website: "https://cloud.google.com/vertex-ai",
    defaultAuthType: "service_account",
    supportsOAuth: false,
    defaultBaseUrl: "https://aiplatform.googleapis.com/v1",
  },
  qwen: {
    type: "qwen",
    name: "Qwen Code",
    icon: "terminal",
    textIcon: "QW",
    category: "oauth",
    listSection: "oauth",
    color: "#10B981",
    description: "Alibaba Qwen Code via device-code OAuth.",
    website: "https://chat.qwen.ai",
    defaultAuthType: "oauth",
    supportsOAuth: true,
    defaultBaseUrl: "https://portal.qwen.ai/v1",
  },
  kiro: {
    type: "kiro",
    name: "Kiro AI",
    icon: "psychology_alt",
    textIcon: "KR",
    category: "oauth",
    listSection: "oauth",
    color: "#FF6B35",
    description: "AWS Kiro / CodeWhisperer with AWS event-stream chat.",
    website: "https://kiro.dev",
    defaultAuthType: "oauth",
    supportsOAuth: true,
    defaultBaseUrl: "https://runtime.us-east-1.kiro.dev",
  },
  iflow: {
    type: "iflow",
    name: "iFlow AI",
    icon: "water_drop",
    textIcon: "IF",
    category: "oauth",
    listSection: "oauth",
    color: "#6366F1",
    description: "iFlow AI via browser OAuth (API key extracted after login).",
    website: "https://iflow.cn",
    defaultAuthType: "oauth",
    supportsOAuth: true,
    defaultBaseUrl: "https://apis.iflow.cn/v1",
  },
  "codebuddy-cn": {
    type: "codebuddy-cn",
    name: "CodeBuddy CN",
    icon: "smart_toy",
    textIcon: "CB",
    category: "oauth",
    listSection: "oauth",
    color: "#006EFF",
    description: "Tencent CodeBuddy CN via browser polling OAuth.",
    website: "https://copilot.tencent.com",
    apiKeyUrl: "https://copilot.tencent.com",
    defaultAuthType: "oauth",
    supportsOAuth: true,
    defaultBaseUrl: "https://copilot.tencent.com/v2",
  },
  kilocode: {
    type: "kilocode",
    name: "Kilo Code",
    icon: "code",
    textIcon: "KC",
    category: "oauth",
    listSection: "oauth",
    color: "#FF6B35",
    description: "Kilo Code OpenRouter proxy via device OAuth.",
    website: "https://kilocode.ai",
    defaultAuthType: "oauth",
    supportsOAuth: true,
    defaultBaseUrl: "https://api.kilo.ai/api/openrouter",
  },
  gitlab: {
    type: "gitlab",
    name: "GitLab Duo",
    icon: "code",
    textIcon: "GL",
    category: "oauth",
    listSection: "oauth",
    color: "#FC6D26",
    description: "GitLab Duo chat via PKCE OAuth (set gitlab_client_id in provider config).",
    website: "https://gitlab.com",
    defaultAuthType: "oauth",
    supportsOAuth: true,
    defaultBaseUrl: "https://gitlab.com/api/v4",
  },
  kimchi: {
    type: "kimchi",
    name: "Kimchi",
    icon: "restaurant",
    textIcon: "KM",
    category: "oauth",
    listSection: "oauth",
    color: "#FF521D",
    description: "Kimchi LLM via browser token callback OAuth.",
    website: "https://kimchi.dev",
    defaultAuthType: "oauth",
    supportsOAuth: true,
    defaultBaseUrl: "https://llm.kimchi.dev/openai/v1",
  },
  qoder: {
    type: "qoder",
    name: "Qoder",
    icon: "auto_awesome",
    textIcon: "QD",
    category: "oauth",
    listSection: "oauth",
    color: "#6366f1",
    description: "Qoder agent API with COSY-signed chat and device OAuth.",
    website: "https://qoder.sh",
    defaultAuthType: "oauth",
    supportsOAuth: true,
    defaultBaseUrl: "https://api3.qoder.sh",
  },
  cursor: {
    type: "cursor",
    name: "Cursor IDE",
    icon: "edit_note",
    textIcon: "CU",
    category: "oauth",
    listSection: "oauth",
    color: "#00D4AA",
    description: "Cursor IDE subscription via Connect+protobuf API. Import token from local Cursor database.",
    website: "https://cursor.com",
    apiKeyUrl: "https://cursor.com",
    defaultAuthType: "oauth",
    supportsOAuth: false,
    defaultBaseUrl: "https://api2.cursor.sh",
  },
  cline: {
    type: "cline",
    name: "Cline",
    icon: "extension",
    textIcon: "CL",
    category: "oauth",
    listSection: "oauth",
    color: "#00D1B2",
    description: "Cline API via browser OAuth or API key from app.cline.bot.",
    website: "https://cline.bot",
    apiKeyUrl: "https://app.cline.bot",
    defaultAuthType: "oauth",
    supportsOAuth: true,
    defaultBaseUrl: "https://api.cline.bot/api/v1",
  },
  clinepass: {
    type: "clinepass",
    name: "ClinePass",
    icon: "vpn_key",
    textIcon: "CP",
    category: "oauth",
    listSection: "oauth",
    color: "#00D1B2",
    description: "ClinePass subscription models via Cline OAuth.",
    website: "https://cline.bot",
    apiKeyUrl: "https://app.cline.bot",
    defaultAuthType: "oauth",
    supportsOAuth: true,
    defaultBaseUrl: "https://api.cline.bot/api/v1",
  },
};

/** Lookup provider type metadata. Returns a generic fallback for unknown types. */
export function getProviderTypeInfo(type: string): ProviderTypeInfo {
  const normalized = type.trim().toLowerCase();
  return (
    CATALOG[normalized] ?? CATALOG[type] ?? {
      type,
      name: type,
      icon: "dns",
      textIcon: type.slice(0, 2).toUpperCase(),
      category: "apikey" as ProviderCategory,
      listSection: "apikey" as ProviderListSection,
      color: "#64748b",
      description: "Custom provider.",
      defaultAuthType: "api_key" as const,
      supportsOAuth: false,
    }
  );
}

/** True when the URL slug is a known catalog provider type (e.g. codex, claude). */
export function isCatalogSlug(slug: string): boolean {
  const normalized = slug.trim().toLowerCase();
  return normalized in CATALOG || slug in CATALOG;
}

export type ResolvedProviderSlug =
  | { kind: "instance"; provider: Provider }
  | { kind: "catalog"; catalog: ProviderTypeInfo };

/** Resolve /providers/:slug — instance id, preset id, catalog type, or unknown. */
export function resolveProviderSlug(
  slug: string,
  providers: Provider[],
  presets: NinerouterPreset[] = [],
): ResolvedProviderSlug | null {
  const byId = providers.find((p) => p.ID === slug);
  if (byId) return { kind: "instance", provider: byId };

  const presetCatalog = findPresetCatalogEntry(presets, slug);
  if (presetCatalog) return { kind: "catalog", catalog: presetCatalog };

  const byType = providers.filter((p) => p.Type === slug);
  if (byType.length > 0) {
    const provider = byType.find((p) => p.Enabled) ?? byType[0];
    return { kind: "instance", provider };
  }

  if (isCatalogSlug(slug)) {
    return { kind: "catalog", catalog: getProviderTypeInfo(slug) };
  }
  return null;
}

export function providerDetailPath(slug: string): string {
  return `/providers/${encodeURIComponent(slug)}`;
}

/** Providers whose OAuth callback may omit the state query parameter. */
export function allowsStatelessOAuthCallback(type: string): boolean {
  return type === "cline" || type === "clinepass" || type === "kimchi";
}

/** Extract OAuth code from a pasted Cline AuthKit callback URL. */
export function parseClineCallbackUrl(raw: string): string | null {
  const trimmed = raw.trim();
  if (!trimmed) return null;
  try {
    const parsed = new URL(trimmed);
    const direct = parsed.searchParams.get("code") || parsed.searchParams.get("token");
    if (direct) return direct;
    if (!parsed.hostname.includes("authkit.cline.bot")) return null;
    const nested = parsed.searchParams.get("redirect_uri");
    if (!nested) return null;
    const nestedUrl = new URL(nested);
    return nestedUrl.searchParams.get("code") || nestedUrl.searchParams.get("token");
  } catch {
    return null;
  }
}

/** Default OAuth mode when the client does not specify one explicitly. */
export function defaultOAuthMode(type: string, presetId?: string): "browser" | "device" {
  if (presetId === "grok-cli" || presetId === "github") return "device";
  if (type === "kimi" || type === "xai" || type === "qwen" || type === "qoder" || type === "copilot" ||
    type === "kilocode" || type === "codebuddy-cn" || type === "kiro") {
    return "device";
  }
  if (type === "cline" || type === "clinepass" || type === "iflow" || type === "kimchi" || type === "gitlab") {
    return "browser";
  }
  return "browser";
}

/** Providers that use browser PKCE (popup + optional callback paste), not device codes. */
export function usesBrowserOAuth(type: string, presetId?: string): boolean {
  return defaultOAuthMode(type, presetId) === "browser";
}

/** Catalog entries grouped for the providers list sections (9router layout). */
export const LIST_BY_SECTION: Record<Exclude<ProviderListSection, "custom">, ProviderTypeInfo[]> = {
  oauth: Object.values(CATALOG).filter((p) => p.listSection === "oauth"),
  freeTier: Object.values(CATALOG).filter((p) => p.listSection === "freeTier"),
  apikey: Object.values(CATALOG).filter((p) => p.listSection === "apikey"),
  media: Object.values(CATALOG).filter((p) => p.listSection === "media"),
  plugin: Object.values(CATALOG).filter((p) => p.listSection === "plugin"),
};

/** All OAuth-capable types (have backend OAuth defaults). */
export const OAUTH_PROVIDER_TYPES = Object.values(CATALOG)
  .filter((p) => p.supportsOAuth)
  .map((p) => p.type);

/** Types grouped by category, for the list page sections. */
export const TYPES_BY_CATEGORY: Record<ProviderCategory, ProviderTypeInfo[]> = {
  oauth: Object.values(CATALOG).filter((p) => p.category === "oauth"),
  apikey: Object.values(CATALOG).filter((p) => p.category === "apikey"),
  media: Object.values(CATALOG).filter((p) => p.category === "media"),
  plugin: Object.values(CATALOG).filter((p) => p.category === "plugin"),
};

/** Gallery entries for the Add Provider picker — curated, most common first. */
export const ADDABLE_PROVIDERS: ProviderTypeInfo[] = [
  CATALOG["openai-compatible"],
  CATALOG["anthropic-compatible"],
  CATALOG["gemini"],
  CATALOG["ollama"],
  CATALOG["claude"],
  CATALOG["codex"],
  CATALOG["xai"],
  CATALOG["kimi"],
  CATALOG["antigravity"],
  CATALOG["vertex"],
  CATALOG["tavily"],
  CATALOG["elevenlabs"],
  CATALOG["image"],
  CATALOG["video"],
  CATALOG["plugin-http"],
];

/** Generate a 2-letter text fallback from a provider name/id. */
export function providerTextIcon(name: string, id: string): string {
  const clean = name.replace(/[^a-zA-Z0-9]/g, "");
  if (clean.length >= 2) return clean.slice(0, 2).toUpperCase();
  return id.slice(0, 2).toUpperCase();
}
