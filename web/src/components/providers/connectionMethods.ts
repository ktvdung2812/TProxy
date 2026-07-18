import {
  defaultOAuthMode,
  getProviderTypeInfo,
  OAUTH_PROVIDER_TYPES,
  type ProviderTypeInfo,
} from "./catalog";
import type { NinerouterPreset } from "./api";

export type ConnectionMethodKind =
  | "oauth"
  | "api_key"
  | "cookie"
  | "service_account"
  | "none"
  | "import_cliproxy"
  | "import_9router";

export type ConnectionMethod = {
  kind: ConnectionMethodKind;
  label: string;
  description?: string;
  available: boolean;
  unavailableReason?: string;
  oauthMode?: "browser" | "device";
  authHint?: string;
  apiKeyUrl?: string;
};

export type ConnectionProfile = {
  methods: ConnectionMethod[];
  /** Hide the connections card body when no credentials are required. */
  noAuth: boolean;
  /** Short guidance shown above connection actions. */
  notice?: string;
  /** Whether generic "Add credential" should remain visible. */
  showAdvancedCredential: boolean;
};

const CLIPROXY_IMPORT_TYPES = new Set([
  "codex",
  "claude",
  "xai",
  "antigravity",
  "kimi",
  "copilot",
  "gemini",
  "openai-compatible",
  "anthropic-compatible",
]);

const OAUTH_READY_TYPES = new Set(OAUTH_PROVIDER_TYPES);

const HOST_URL_PRESETS = new Set(["ollama-local"]);

function oauthAvailable(providerType: string): boolean {
  return OAUTH_READY_TYPES.has(providerType);
}

function oauthLabel(catalog: ProviderTypeInfo, presetId?: string): string {
  if (presetId === "grok-cli") return "Grok CLI Device Login";
  if (presetId === "github") return "GitHub OAuth";
  if (presetId === "gemini-cli") return "Google Sign-In";
  if (catalog.type === "copilot") return "GitHub Device Login";
  return `Connect ${catalog.name}`;
}

function apiKeyLabel(catalog: ProviderTypeInfo, preset?: NinerouterPreset | null): string {
  if (preset?.id === "ollama-local") return "Add Ollama Host";
  if (preset?.credential_auth === "cookie" || preset?.category === "webCookie") return "Add Cookie";
  return "Add API Key";
}

/** Resolve per-provider connection methods from catalog + optional 9router preset metadata. */
export function resolveConnectionProfile(
  catalog: ProviderTypeInfo,
  preset?: NinerouterPreset | null,
): ConnectionProfile {
  const presetId = catalog.presetId || preset?.id;
  const providerType = catalog.type;
  const authModes = preset?.auth_modes ?? [];
  const hasOAuthMode =
    authModes.includes("oauth") ||
    preset?.has_oauth ||
    preset?.auth_type === "oauth" ||
    preset?.category === "oauth" ||
    preset?.category === "free";
  const hasApiKeyMode =
    authModes.includes("apikey") ||
    preset?.credential_auth === "apikey" ||
    preset?.category === "apikey" ||
    preset?.category === "freeTier" ||
    catalog.defaultAuthType === "api_key";
  const isCookie =
    preset?.credential_auth === "cookie" ||
    preset?.category === "webCookie" ||
    preset?.auth_type === "cookie";
  const noAuth = Boolean(catalog.noAuth || preset?.no_auth || preset?.credential_auth === "none");

  if (noAuth) {
    return {
      noAuth: true,
      showAdvancedCredential: true,
      notice: "This provider works without API credentials. Add a no-auth connection or enable the provider to route traffic.",
      methods: [
        {
          kind: "none",
          label: "Add no-auth connection",
          description: "Use proxy pools and routing without upstream credentials.",
          available: true,
        },
      ],
    };
  }

  const methods: ConnectionMethod[] = [];

  if (isCookie) {
    methods.push({
      kind: "cookie",
      label: apiKeyLabel(catalog, preset),
      description: preset?.auth_hint || "Paste the session cookie from your browser.",
      available: true,
      authHint: preset?.auth_hint,
    });
  }

  if (hasOAuthMode) {
    const available = oauthAvailable(providerType);
    methods.push({
      kind: "oauth",
      label: oauthLabel(catalog, presetId),
      description: available
        ? defaultOAuthMode(providerType, presetId) === "device"
          ? "Device authorization — complete sign-in in the browser tab that opens."
          : "Browser OAuth — sign in with your provider account."
        : undefined,
      available,
      unavailableReason: available ? undefined : "OAuth for this provider is not implemented in tproxy yet. Import a token export instead.",
      oauthMode: defaultOAuthMode(providerType, presetId),
    });
    if (!available && CLIPROXY_IMPORT_TYPES.has(providerType)) {
      methods.push({
        kind: "import_cliproxy",
        label: "Import CLIProxyAPI auth JSON",
        description: "Paste an exported OAuth token file from CLIProxyAPI.",
        available: true,
      });
    }
  }

  if (hasApiKeyMode && !isCookie) {
    methods.push({
      kind: "api_key",
      label: apiKeyLabel(catalog, preset),
      description:
        presetId && HOST_URL_PRESETS.has(presetId)
          ? "Set the Ollama host URL. API key is optional for local instances."
          : "Paste an API key from the provider console.",
      available: true,
      apiKeyUrl: preset?.api_key_url || catalog.apiKeyUrl,
      authHint: preset?.auth_hint,
    });
  }

  if (catalog.defaultAuthType === "service_account" || providerType === "vertex" || providerType === "vertex-partner") {
    methods.push({
      kind: "service_account",
      label: "Add Service Account",
      description: "Paste Google Cloud service account JSON.",
      available: true,
    });
  }

  if (oauthAvailable(providerType) && hasOAuthMode) {
    methods.push({
      kind: "import_cliproxy",
      label: "Import CLIProxyAPI OAuth JSON",
      description: "Alternative to interactive OAuth — import `tproxy-auth-bundle.json` or CLIProxyAPI auth export.",
      available: true,
    });
  }

  methods.push({
    kind: "import_9router",
    label: "Import 9router backup",
    description: "Restore credentials from a 9router export via Import Data.",
    available: true,
  });

  const unique = methods.filter(
    (method, index) => methods.findIndex((item) => item.kind === method.kind && item.label === method.label) === index,
  );

  return {
    noAuth: false,
    showAdvancedCredential: true,
    notice: preset?.auth_hint && isCookie ? preset.auth_hint : undefined,
    methods: unique,
  };
}

export function findPresetByProviderId(presets: NinerouterPreset[], providerId: string, providerType: string) {
  return presets.find((item) => item.id === providerId) ?? presets.find((item) => item.id === providerType) ?? null;
}

export function catalogWithPreset(
  providerType: string,
  providerId: string,
  presets: NinerouterPreset[],
): ProviderTypeInfo {
  const preset = findPresetByProviderId(presets, providerId, providerType);
  if (preset) {
    const base = getProviderTypeInfo(preset.type);
    return {
      ...base,
      type: preset.type,
      presetId: preset.id,
      name: preset.name,
      defaultBaseUrl: preset.base_url || base.defaultBaseUrl,
      defaultAuthType:
        preset.auth_type === "oauth"
          ? "oauth"
          : preset.auth_type === "none"
            ? "none"
            : preset.auth_type === "service_account"
              ? "service_account"
              : "api_key",
      supportsOAuth: oauthAvailable(preset.type) && (preset.has_oauth || preset.auth_type === "oauth" || (preset.auth_modes ?? []).includes("oauth")),
      noAuth: preset.no_auth,
      apiKeyUrl: preset.api_key_url || base.apiKeyUrl,
    };
  }
  const info = getProviderTypeInfo(providerType);
  if (providerId !== providerType) {
    return { ...info, presetId: providerId };
  }
  return info;
}
