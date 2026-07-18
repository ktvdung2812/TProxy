import {
  getProviderTypeInfo,
  providerTextIcon,
  OAUTH_PROVIDER_TYPES,
  type ProviderListSection,
  type ProviderTypeInfo,
} from "./catalog";
import type { NinerouterPreset } from "./api";

export const APIKEY_INITIAL_VISIBLE = 20;

const MEDIA_TYPES = new Set(["tavily", "elevenlabs", "image", "video"]);

export type PresetCatalogEntry = ProviderTypeInfo & {
  /** 9router registry id — also used as the default provider instance id. */
  presetId: string;
};

function mapAuthType(authType: string): ProviderTypeInfo["defaultAuthType"] {
  switch (authType) {
    case "oauth":
      return "oauth";
    case "service_account":
      return "service_account";
    case "none":
      return "none";
    default:
      return "api_key";
  }
}

function mapListSection(preset: NinerouterPreset): ProviderListSection {
  if (MEDIA_TYPES.has(preset.type)) return "media";
  switch (preset.category) {
    case "oauth":
    case "webCookie":
      return "oauth";
    case "free":
    case "freeTier":
      return "freeTier";
    case "apikey":
      return "apikey";
    default:
      return "apikey";
  }
}

/** Convert a 9router preset into a provider card entry for the dashboard grid. */
export function presetToCatalogEntry(preset: NinerouterPreset): PresetCatalogEntry {
  const base = getProviderTypeInfo(preset.type);
  const listSection = mapListSection(preset);
  const category =
    listSection === "oauth"
      ? "oauth"
      : listSection === "media"
        ? "media"
        : listSection === "plugin"
          ? "plugin"
          : "apikey";

  return {
    ...base,
    type: preset.type,
    presetId: preset.id,
    name: preset.name,
    textIcon: providerTextIcon(preset.name, preset.id),
    category,
    listSection,
    defaultAuthType: mapAuthType(preset.auth_type),
    supportsOAuth:
      OAUTH_PROVIDER_TYPES.includes(preset.type) &&
      (preset.has_oauth || preset.auth_type === "oauth" || (preset.auth_modes ?? []).includes("oauth")),
    noAuth: preset.no_auth,
    apiKeyUrl: preset.api_key_url || base.apiKeyUrl,
    defaultBaseUrl: preset.base_url || base.defaultBaseUrl,
    description: base.description || `${preset.name} via ${preset.type}.`,
  };
}

export function groupPresetsBySection(presets: NinerouterPreset[]) {
  const grouped: Record<Exclude<ProviderListSection, "custom">, PresetCatalogEntry[]> = {
    oauth: [],
    freeTier: [],
    apikey: [],
    media: [],
    plugin: [],
  };

  const entries = presets.map(presetToCatalogEntry);
  for (const entry of entries) {
    if (entry.listSection === "custom") continue;
    grouped[entry.listSection].push(entry);
  }

  const byName = (a: PresetCatalogEntry, b: PresetCatalogEntry) =>
    a.name.localeCompare(b.name, undefined, { sensitivity: "base" });

  grouped.oauth.sort(byName);
  grouped.freeTier.sort((a, b) => {
    if (a.noAuth !== b.noAuth) return a.noAuth ? -1 : 1;
    return byName(a, b);
  });
  grouped.apikey.sort(byName);
  grouped.media.sort(byName);
  grouped.plugin.sort(byName);

  return grouped;
}

export function findPresetCatalogEntry(
  presets: NinerouterPreset[],
  slug: string,
): PresetCatalogEntry | null {
  const preset = presets.find((item) => item.id === slug);
  return preset ? presetToCatalogEntry(preset) : null;
}
