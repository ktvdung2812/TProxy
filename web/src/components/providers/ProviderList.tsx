import { useMemo, type ReactNode } from "react";
import { Button } from "../ui";
import { ModelAvailabilityBadge } from "./ModelAvailabilityBadge";
import { LIST_BY_SECTION, providerDetailPath, type ProviderTypeInfo } from "./catalog";
import { CustomProviderCard, ProviderCatalogCard } from "./ProviderCatalogCard";
import type { Credential, Provider } from "./types";

type Props = {
  providers: Provider[];
  credentials: Record<string, Credential[]>;
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

function providersForType(providers: Provider[], type: string) {
  return providers.filter((p) => p.Type === type);
}

/** Providers list — 9router-style sections and compact cards. */
export function ProviderList({
  providers,
  credentials,
  searchQuery = "",
  onAddOpenAI,
  onAddAnthropic,
  onTestSection,
  testingSection,
}: Props) {
  const customProviders = useMemo(
    () => providers.filter((p) => CUSTOM_TYPES.has(p.Type)),
    [providers],
  );

  const filterCatalog = (items: ProviderTypeInfo[]) =>
    items.filter((item) => matchesSearch(item.name, searchQuery));

  const filterCustom = (items: Provider[]) =>
    items.filter((item) => matchesSearch(item.Name || item.ID, searchQuery));

  const oauthCatalog = filterCatalog(LIST_BY_SECTION.oauth);
  const freeTierCatalog = filterCatalog(LIST_BY_SECTION.freeTier);
  const apikeyCatalog = filterCatalog(LIST_BY_SECTION.apikey);
  const mediaCatalog = filterCatalog(LIST_BY_SECTION.media);
  const pluginCatalog = filterCatalog(LIST_BY_SECTION.plugin);
  const visibleCustom = filterCustom(customProviders);

  const hasResults =
    visibleCustom.length > 0 ||
    oauthCatalog.length > 0 ||
    freeTierCatalog.length > 0 ||
    apikeyCatalog.length > 0 ||
    mediaCatalog.length > 0 ||
    pluginCatalog.length > 0;

  const sectionProviderIds = (catalog: ProviderTypeInfo[]) =>
    catalog.flatMap((item) => providersForType(providers, item.type).map((p) => p.ID));

  const oauthCredentials = useMemo(
    () => sectionProviderIds(oauthCatalog).flatMap((id) => credentials[id] || []),
    [oauthCatalog, credentials, providers],
  );

  return (
    <div className="providers-page">
      {!hasResults && searchQuery.trim() && (
        <div className="providers-search-empty">
          <span className="material-symbols-outlined">search_off</span>
          <p>No providers match your search</p>
        </div>
      )}

      {/* Custom compatible upstreams */}
      <ProviderSection
        title="Custom Providers (OpenAI/Anthropic Compatible)"
        actions={
          <>
            <Button size="sm" icon="add" onClick={onAddAnthropic}>
              Add Anthropic Compatible
            </Button>
            <Button size="sm" variant="secondary" className="btn-white" icon="add" onClick={onAddOpenAI}>
              Add OpenAI Compatible
            </Button>
          </>
        }
      >
        {visibleCustom.length === 0 ? (
          <div className="providers-dashed-empty">
            <span className="material-symbols-outlined">extension</span>
            <span>No custom providers — use buttons above to add OpenAI/Anthropic compatible endpoints</span>
          </div>
        ) : (
          <div className="provider-catalog-grid">
            {visibleCustom.map((provider) => (
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

      {oauthCatalog.length > 0 && (
        <ProviderSection
          title="OAuth Providers"
          actions={
            <>
              <ModelAvailabilityBadge credentials={oauthCredentials} />
              <TestAllButton
                label="Test All"
                busy={testingSection === "oauth"}
                onClick={() => onTestSection?.("oauth", sectionProviderIds(oauthCatalog))}
              />
            </>
          }
        >
          <CatalogGrid
            catalog={oauthCatalog}
            providers={providers}
            credentials={credentials}
          />
        </ProviderSection>
      )}

      {freeTierCatalog.length > 0 && (
        <ProviderSection
          title="Free Tier Providers"
          actions={
            <TestAllButton
              label="Test All"
              busy={testingSection === "freeTier"}
              onClick={() => onTestSection?.("freeTier", sectionProviderIds(freeTierCatalog))}
            />
          }
        >
          <CatalogGrid
            catalog={freeTierCatalog}
            providers={providers}
            credentials={credentials}
          />
        </ProviderSection>
      )}

      {apikeyCatalog.length > 0 && (
        <ProviderSection
          title="API Key Providers"
          actions={
            <TestAllButton
              label="Test All"
              busy={testingSection === "apikey"}
              onClick={() => onTestSection?.("apikey", sectionProviderIds(apikeyCatalog))}
            />
          }
        >
          <CatalogGrid
            catalog={apikeyCatalog}
            providers={providers}
            credentials={credentials}
          />
        </ProviderSection>
      )}

      {mediaCatalog.length > 0 && (
        <ProviderSection title="Media Providers">
          <CatalogGrid
            catalog={mediaCatalog}
            providers={providers}
            credentials={credentials}
          />
        </ProviderSection>
      )}

      {pluginCatalog.length > 0 && (
        <ProviderSection title="Plugin Providers">
          <CatalogGrid
            catalog={pluginCatalog}
            providers={providers}
            credentials={credentials}
          />
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
  catalog: ProviderTypeInfo[];
  providers: Provider[];
  credentials: Record<string, Credential[]>;
}) {
  return (
    <div className="provider-catalog-grid">
      {catalog.map((item) => (
        <ProviderCatalogCard
          key={item.type}
          catalog={item}
          instances={providersForType(providers, item.type)}
          credentials={credentials}
          to={providerDetailPath(item.type)}
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
