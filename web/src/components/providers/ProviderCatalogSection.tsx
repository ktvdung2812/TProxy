import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { EmptyPanel, FilterTabs, LinkButton, SectionHead, Tag } from "../page-kit";
import type { NinerouterPreset } from "./api";
import { providerDetailPath } from "./catalog";
import { CustomEndpointCard, ProviderCatalogCard, type CatalogAuthKind } from "./ProviderCatalogCard";
import { APIKEY_INITIAL_VISIBLE, groupPresetsBySection, type PresetCatalogEntry } from "./ninerouterCatalog";
import type { Credential, Provider } from "./types";

type CatalogTab = "oauth" | "freeTier" | "apikey" | "media" | "plugin" | "custom";
const PRESET_TABS: Exclude<CatalogTab, "custom">[] = ["oauth", "freeTier", "apikey", "media", "plugin"];
const TAB_ICON: Record<CatalogTab, string> = {
  oauth: "login",
  freeTier: "redeem",
  apikey: "key",
  media: "perm_media",
  plugin: "extension",
  custom: "add_link",
};

type Props = {
  presets: NinerouterPreset[];
  providers: Provider[];
  credentials: Record<string, Credential[]>;
  search: string;
  loading?: boolean;
  onAddCustom: (type: "openai-compatible" | "anthropic-compatible") => void;
};

function matches(entry: PresetCatalogEntry, query: string) {
  if (!query) return true;
  return entry.name.toLowerCase().includes(query) || entry.presetId.toLowerCase().includes(query);
}

function instancesFor(providers: Provider[], entry: PresetCatalogEntry) {
  return providers.filter((provider) => provider.ID === entry.presetId);
}

function authKind(entry: PresetCatalogEntry, preset?: NinerouterPreset): CatalogAuthKind {
  if (entry.noAuth) return "noAuth";
  if (preset?.credential_auth === "cookie" || preset?.category === "webCookie") return "cookie";
  if (entry.defaultAuthType === "oauth") return "oauth";
  if (entry.defaultAuthType === "service_account") return "serviceAccount";
  return "apikey";
}

/** "Add a provider" catalog: one tab per 9router section, searchable from the page toolbar. */
export function ProviderCatalogSection({ presets, providers, credentials, search, loading, onAddCustom }: Props) {
  const { t } = useTranslation();
  const [tab, setTab] = useState<CatalogTab>("oauth");
  const [showAll, setShowAll] = useState(false);
  const query = search.trim().toLowerCase();
  const sections = useMemo(() => groupPresetsBySection(presets), [presets]);
  const presetById = useMemo(() => new Map(presets.map((preset) => [preset.id, preset])), [presets]);

  useEffect(() => setShowAll(false), [tab, query]);

  const filtered = useMemo(() => {
    const result = {} as Record<Exclude<CatalogTab, "custom">, PresetCatalogEntry[]>;
    for (const key of PRESET_TABS) {
      // Providers that are already set up lead their section, like before.
      result[key] = sections[key]
        .filter((entry) => matches(entry, query))
        .sort((a, b) => Number(instancesFor(providers, b).length > 0) - Number(instancesFor(providers, a).length > 0));
    }
    return result;
  }, [sections, query, providers]);

  const customMatches = !query || ["openai", "anthropic", "compatible", "custom"].some((word) => word.includes(query) || query.includes(word));
  const total = PRESET_TABS.reduce((sum, key) => sum + sections[key].length, 0);
  const matchCount = PRESET_TABS.reduce((sum, key) => sum + filtered[key].length, 0);

  const tabs = [
    ...PRESET_TABS.filter((key) => sections[key].length > 0).map((key) => ({
      value: key as CatalogTab,
      icon: TAB_ICON[key],
      label: t(`providers.catalog.tab.${key}`),
      count: filtered[key].length,
    })),
    { value: "custom" as CatalogTab, icon: TAB_ICON.custom, label: t("providers.catalog.tab.custom"), count: customMatches ? 2 : 0 },
  ];

  // While searching, jump to the first tab that has a hit so a match is never hidden.
  const activeTab: CatalogTab =
    query && tab !== "custom" && filtered[tab].length === 0
      ? (PRESET_TABS.find((key) => filtered[key].length > 0) ?? (customMatches ? "custom" : tab))
      : tab;

  const entries = activeTab === "custom" ? [] : filtered[activeTab];
  const limit = activeTab === "apikey" ? APIKEY_INITIAL_VISIBLE : 24;
  const visible = showAll || query ? entries : entries.slice(0, limit);
  const hidden = entries.length - visible.length;

  return (
    <section className="pv-catalog" aria-label={t("providers.catalog.title")}>
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>add_circle</span>}
        title={t("providers.catalog.title")}
        subtitle={query ? t("providers.catalog.matches", { count: matchCount, total }) : t("providers.catalog.subtitle", { count: total })}
        meta={loading ? <Tag tone="neutral">{t("providers.catalog.loading")}</Tag> : null}
      />
      <FilterTabs label={t("providers.catalog.tabsLabel")} value={activeTab} onChange={setTab} items={tabs} />
      {activeTab === "custom" ? (
        <>
          <p className="pv-catalog-hint">{t("providers.catalog.customHint")}</p>
          <div className="pv-tiles">
            <CustomEndpointCard type="openai-compatible" onAdd={() => onAddCustom("openai-compatible")} />
            <CustomEndpointCard type="anthropic-compatible" onAdd={() => onAddCustom("anthropic-compatible")} />
          </div>
        </>
      ) : entries.length === 0 ? (
        <EmptyPanel
          compact
          icon={loading ? "progress_activity" : "search_off"}
          loading={loading}
          title={loading ? t("providers.catalog.loading") : t("providers.catalog.noMatch")}
          description={loading ? undefined : t("providers.catalog.noMatchHint")}
        />
      ) : (
        <>
          <p className="pv-catalog-hint">{t(`providers.catalog.hint.${activeTab}`)}</p>
          <div className="pv-tiles">
            {visible.map((entry) => (
              <ProviderCatalogCard
                key={entry.presetId}
                catalog={entry}
                instances={instancesFor(providers, entry)}
                credentials={credentials}
                to={providerDetailPath(entry.presetId)}
                auth={authKind(entry, presetById.get(entry.presetId))}
              />
            ))}
          </div>
          {hidden > 0 ? (
            <LinkButton className="pv-catalog-more" onClick={() => setShowAll(true)}>
              {t("providers.providerListShowAll", { count: entries.length })}
            </LinkButton>
          ) : null}
        </>
      )}
    </section>
  );
}
