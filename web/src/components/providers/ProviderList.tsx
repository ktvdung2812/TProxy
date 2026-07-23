import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Button } from "../ui";
import { ModelAvailabilityBadge } from "./ModelAvailabilityBadge";
import { providerDetailPath, type ProviderTypeInfo } from "./catalog";
import { fetchNinerouterPresets } from "./api";
import {
  APIKEY_INITIAL_VISIBLE,
  groupPresetsBySection,
  type PresetCatalogEntry,
} from "./ninerouterCatalog";
import { CustomProviderCard, ProviderCatalogCard } from "./ProviderCatalogCard";
import type { Credential, Provider } from "./types";
import { getProviderStats } from "./types";

type Props = {
  providers: Provider[];
  credentials: Record<string, Credential[]>;
  secret: string;
  searchQuery?: string;
  onAddOpenAI: () => void;
  onAddAnthropic: () => void;
  onTestSection?: (section: string, providerIds: string[]) => void;
  testingSection?: string | null;
};

const CUSTOM_TYPES = new Set(["openai-compatible", "anthropic-compatible"]);

function matchesSearch(name: string, query: string) {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return name.toLowerCase().includes(q);
}

function instancesForEntry(providers: Provider[], entry: ProviderTypeInfo) {
  if (entry.presetId) {
    const byPreset = providers.filter((p) => p.ID === entry.presetId);
    if (byPreset.length > 0) return byPreset;
  }
  return providers.filter((p) => p.Type === entry.type && (!entry.presetId || p.ID === entry.presetId));
}

function connectionSortScore(
  providers: Provider[],
  credentials: Record<string, Credential[]>,
  entry: PresetCatalogEntry | Provider,
) {
  const instances =
    "ID" in entry
      ? providers.filter((p) => p.ID === entry.ID)
      : instancesForEntry(providers, entry);
  const stats = getProviderStats(instances.flatMap((p) => credentials[p.ID] || []));
  return { hasAccounts: stats.total > 0 ? 1 : 0, connected: stats.active };
}

function sortByConnections<T extends PresetCatalogEntry | Provider>(
  items: T[],
  providers: Provider[],
  credentials: Record<string, Credential[]>,
): T[] {
  const byName = (a: T, b: T) => {
    const nameA = "Name" in a ? a.Name || a.ID : a.name;
    const nameB = "Name" in b ? b.Name || b.ID : b.name;
    return nameA.localeCompare(nameB, undefined, { sensitivity: "base" });
  };
  return [...items].sort((a, b) => {
    const scoreA = connectionSortScore(providers, credentials, a);
    const scoreB = connectionSortScore(providers, credentials, b);
    if (scoreA.hasAccounts !== scoreB.hasAccounts) return scoreB.hasAccounts - scoreA.hasAccounts;
    if (scoreA.connected !== scoreB.connected) return scoreB.connected - scoreA.connected;
    return byName(a, b);
  });
}

function sortCatalogByConnections(
  catalog: PresetCatalogEntry[],
  providers: Provider[],
  credentials: Record<string, Credential[]>,
) {
  return sortByConnections(catalog, providers, credentials);
}

/** Providers list — 9router-style sections and compact cards. */
export function ProviderList({
  providers,
  credentials,
  secret,
  searchQuery = "",
  onAddOpenAI,
  onAddAnthropic,
  onTestSection,
  testingSection,
}: Props) {
  const [showAllApikey, setShowAllApikey] = useState(false);
  const [presetSections, setPresetSections] = useState(() => groupPresetsBySection([]));

  useEffect(() => {
    setShowAllApikey(false);
  }, [searchQuery]);

  useEffect(() => {
    if (!secret) return;
    void fetchNinerouterPresets(secret)
      .then((result) => setPresetSections(groupPresetsBySection(result.presets || [])))
      .catch(() => setPresetSections(groupPresetsBySection([])));
  }, [secret]);

  const customProviders = useMemo(
    () => providers.filter((p) => CUSTOM_TYPES.has(p.Type)),
    [providers],
  );

  const filterCatalog = (items: PresetCatalogEntry[]) =>
    items.filter((item) => matchesSearch(item.name, searchQuery));

  const filterCustom = (items: Provider[]) =>
    items.filter((item) => matchesSearch(item.Name || item.ID, searchQuery));

  const oauthCatalog = filterCatalog(presetSections.oauth);
  const freeTierCatalog = filterCatalog(presetSections.freeTier);
  const apikeyCatalog = filterCatalog(presetSections.apikey);
  const mediaCatalog = filterCatalog(presetSections.media);
  const pluginCatalog = filterCatalog(presetSections.plugin);
  const visibleCustom = filterCustom(customProviders);

  const sortedCustomWithAccounts = useMemo(
    () =>
      sortByConnections(
        visibleCustom.filter((provider) => (credentials[provider.ID] || []).length > 0),
        providers,
        credentials,
      ),
    [visibleCustom, providers, credentials],
  );
  const sortedCustomWithoutAccounts = useMemo(
    () =>
      sortByConnections(
        visibleCustom.filter((provider) => (credentials[provider.ID] || []).length === 0),
        providers,
        credentials,
      ),
    [visibleCustom, providers, credentials],
  );
  const sortedVisibleCustom = useMemo(
    () => [...sortedCustomWithAccounts, ...sortedCustomWithoutAccounts],
    [sortedCustomWithAccounts, sortedCustomWithoutAccounts],
  );

  const sortedOauthCatalog = useMemo(
    () => sortCatalogByConnections(oauthCatalog, providers, credentials),
    [oauthCatalog, providers, credentials],
  );
  const sortedFreeTierCatalog = useMemo(
    () => sortCatalogByConnections(freeTierCatalog, providers, credentials),
    [freeTierCatalog, providers, credentials],
  );
  const sortedApikeyCatalog = useMemo(
    () => sortCatalogByConnections(apikeyCatalog, providers, credentials),
    [apikeyCatalog, providers, credentials],
  );
  const sortedMediaCatalog = useMemo(
    () => sortCatalogByConnections(mediaCatalog, providers, credentials),
    [mediaCatalog, providers, credentials],
  );
  const sortedPluginCatalog = useMemo(
    () => sortCatalogByConnections(pluginCatalog, providers, credentials),
    [pluginCatalog, providers, credentials],
  );

  const isApikeySearching = !!searchQuery.trim();
  const apikeyWithAccounts = useMemo(
    () =>
      sortedApikeyCatalog.filter((entry) => {
        const instances = instancesForEntry(providers, entry);
        return instances.some((provider) => (credentials[provider.ID] || []).length > 0);
      }),
    [sortedApikeyCatalog, providers, credentials],
  );
  const visibleApikeyCatalog = useMemo(() => {
    if (isApikeySearching || showAllApikey) return sortedApikeyCatalog;
    const pinned = new Set(apikeyWithAccounts.map((entry) => entry.presetId || entry.type));
    const rest = sortedApikeyCatalog.filter((entry) => !pinned.has(entry.presetId || entry.type));
    const combined = [...apikeyWithAccounts, ...rest];
    return combined.slice(0, Math.max(APIKEY_INITIAL_VISIBLE, apikeyWithAccounts.length));
  }, [isApikeySearching, showAllApikey, sortedApikeyCatalog, apikeyWithAccounts]);
  const hiddenApikeyCount = Math.max(0, sortedApikeyCatalog.length - visibleApikeyCatalog.length);

  const hasResults =
    visibleCustom.length > 0 ||
    oauthCatalog.length > 0 ||
    freeTierCatalog.length > 0 ||
    apikeyCatalog.length > 0 ||
    mediaCatalog.length > 0 ||
    pluginCatalog.length > 0;

  const sectionProviderIds = (catalog: PresetCatalogEntry[]) =>
    catalog.flatMap((item) => instancesForEntry(providers, item).map((p) => p.ID));

  const oauthCredentials = useMemo(
    () => sectionProviderIds(sortedOauthCatalog).flatMap((id) => credentials[id] || []),
    [sortedOauthCatalog, credentials, providers],
  );

  return (
    <div className="providers-page">
      {!hasResults && searchQuery.trim() && (
        <div className="providers-search-empty">
          <span className="material-symbols-outlined">search_off</span>
          <p>No providers match your search</p>
        </div>
      )}

      <ProviderSection
        title="Custom Providers (OpenAI/Anthropic Compatible)"
        actions={
          <>
            <Button size="sm" icon="add" onClick={onAddAnthropic}>
              Anthropic Compatible
            </Button>
            <Button size="sm" variant="secondary" className="btn-white" icon="add" onClick={onAddOpenAI}>
              OpenAI Compatible
            </Button>
          </>
        }
      >
        {sortedVisibleCustom.length === 0 ? (
          <div className="providers-dashed-empty">
            <span className="material-symbols-outlined">extension</span>
            <span>No custom providers — use buttons above to add OpenAI/Anthropic compatible endpoints</span>
          </div>
        ) : (
          <div className="provider-catalog-grid">
            {sortedVisibleCustom.map((provider) => (
              <CustomProviderCard
                key={provider.ID}
                provider={provider}
                credentials={credentials[provider.ID] || []}
                to={providerDetailPath(provider.ID)}
              />
            ))}
          </div>
        )}
      </ProviderSection>

      {sortedOauthCatalog.length > 0 && (
        <ProviderSection
          title="OAuth Providers"
          actions={
            <>
              <ModelAvailabilityBadge credentials={oauthCredentials} />
              <TestAllButton
                label="Test All"
                busy={testingSection === "oauth"}
                onClick={() => onTestSection?.("oauth", sectionProviderIds(sortedOauthCatalog))}
              />
            </>
          }
        >
          <CatalogGrid catalog={sortedOauthCatalog} providers={providers} credentials={credentials} />
        </ProviderSection>
      )}

      {sortedFreeTierCatalog.length > 0 && (
        <ProviderSection
          title="Free Tier Providers"
          actions={
            <TestAllButton
              label="Test All"
              busy={testingSection === "freeTier"}
              onClick={() => onTestSection?.("freeTier", sectionProviderIds(sortedFreeTierCatalog))}
            />
          }
        >
          <CatalogGrid catalog={sortedFreeTierCatalog} providers={providers} credentials={credentials} />
        </ProviderSection>
      )}

      {sortedApikeyCatalog.length > 0 && (
        <ProviderSection
          title="API Key Providers"
          actions={
            <TestAllButton
              label="Test All"
              busy={testingSection === "apikey"}
              onClick={() => onTestSection?.("apikey", sectionProviderIds(sortedApikeyCatalog))}
            />
          }
        >
          <CatalogGrid catalog={visibleApikeyCatalog} providers={providers} credentials={credentials} />
          {!isApikeySearching && !showAllApikey && hiddenApikeyCount > 0 ? (
            <button
              type="button"
              className="providers-show-more"
              onClick={() => setShowAllApikey(true)}
            >
              <span className="material-symbols-outlined">expand_more</span>
              Show all {sortedApikeyCatalog.length} providers
            </button>
          ) : null}
        </ProviderSection>
      )}

      {sortedMediaCatalog.length > 0 && (
        <ProviderSection title="Media Providers">
          <CatalogGrid catalog={sortedMediaCatalog} providers={providers} credentials={credentials} />
        </ProviderSection>
      )}

      {sortedPluginCatalog.length > 0 && (
        <ProviderSection title="Plugin Providers">
          <CatalogGrid catalog={sortedPluginCatalog} providers={providers} credentials={credentials} />
        </ProviderSection>
      )}
    </div>
  );
}

function ProviderSection({
  title,
  actions,
  children,
}: {
  title: string;
  actions?: React.ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="providers-section">
      <div className="providers-section-head">
        <h2>{title}</h2>
        {actions ? <div className="providers-section-actions">{actions}</div> : null}
      </div>
      {children}
    </section>
  );
}

function CatalogGrid({
  catalog,
  providers,
  credentials,
}: {
  catalog: PresetCatalogEntry[];
  providers: Provider[];
  credentials: Record<string, Credential[]>;
}) {
  return (
    <div className="provider-catalog-grid">
      {catalog.map((item) => (
        <ProviderCatalogCard
          key={item.presetId || item.type}
          catalog={item}
          instances={instancesForEntry(providers, item)}
          credentials={credentials}
          to={providerDetailPath(item.presetId || item.type)}
        />
      ))}
    </div>
  );
}

function TestAllButton({ label, busy, onClick }: { label: string; busy?: boolean; onClick: () => void }) {
  return (
    <button type="button" className={`providers-test-all ${busy ? "busy" : ""}`} onClick={onClick} disabled={busy}>
      <span className={`material-symbols-outlined ${busy ? "spin" : ""}`}>play_arrow</span>
      {busy ? "Testing..." : label}
    </button>
  );
}
