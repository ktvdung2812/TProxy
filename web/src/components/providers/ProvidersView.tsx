import { useEffect, useMemo, useState } from "react";
import { ConfirmDialog } from "../ui";
import { ImportDataCard } from "../import/ImportDataCard";
import { ImportModal } from "../import/ImportModal";
import { AddProviderModal } from "./AddProviderModal";
import { ProviderDetail } from "./ProviderDetail";
import { ProviderList } from "./ProviderList";
import { ProviderTypeDetail } from "./ProviderTypeDetail";
import { resolveProviderSlug, type ProviderTypeInfo } from "./catalog";
import { resolveConnectionProfile, type ConnectionMethod } from "./connectionMethods";
import { checkProviderHealth, deleteProvider, exportAuthBundle, fetchNinerouterPresets, importAuthBundle, saveProvider, type NinerouterPreset } from "./api";
import { AddCredentialModal } from "./AddCredentialModal";
import { OAuthModal } from "./OAuthModal";
import type { Credential, ModelAlias, Provider } from "./types";

type Props = {
  providers: Provider[];
  credentials: Record<string, Credential[]>;
  aliases: ModelAlias[];
  secret: string;
  searchQuery?: string;
  selectedId: string | null;
  onSelect: (id: string | null) => void;
  onMutated: () => void;
  onNotice: (message: string) => void;
  onError: (message: string) => void;
};

/** Container for the Providers feature: switches between list and detail. */
export function ProvidersView({
  providers,
  credentials,
  aliases,
  secret,
  searchQuery = "",
  selectedId,
  onSelect,
  onMutated,
  onNotice,
  onError,
}: Props) {
  const [addPreset, setAddPreset] = useState<string | undefined>();
  const [showAdd, setShowAdd] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState<Provider | null>(null);
  const [testingSection, setTestingSection] = useState<string | null>(null);
  const [authBusy, setAuthBusy] = useState(false);
  const [connectBusy, setConnectBusy] = useState(false);
  const [showOAuth, setShowOAuth] = useState(false);
  const [showImport, setShowImport] = useState(false);
  const [oauthProviderId, setOauthProviderId] = useState<string | null>(null);
  const [oauthProviderType, setOauthProviderType] = useState<string | null>(null);
  const [oauthPresetId, setOauthPresetId] = useState<string | null>(null);
  const [presets, setPresets] = useState<NinerouterPreset[]>([]);
  const [presetsLoaded, setPresetsLoaded] = useState(false);
  const [showCatalogCredential, setShowCatalogCredential] = useState(false);
  const [catalogCredentialMethod, setCatalogCredentialMethod] = useState<ConnectionMethod | null>(null);
  const [catalogProviderId, setCatalogProviderId] = useState<string | null>(null);
  const importInputId = "tproxy-auth-bundle-import";

  useEffect(() => {
    if (!secret) {
      setPresets([]);
      setPresetsLoaded(true);
      return;
    }
    setPresetsLoaded(false);
    void fetchNinerouterPresets(secret)
      .then((result) => setPresets(result.presets || []))
      .catch(() => setPresets([]))
      .finally(() => setPresetsLoaded(true));
  }, [secret]);

  useEffect(() => {
    if (!selectedId || !presetsLoaded) return;
    if (!resolveProviderSlug(selectedId, providers, presets)) {
      onSelect(null);
    }
  }, [selectedId, providers, presets, presetsLoaded, onSelect]);

  const resolved = useMemo(
    () => (selectedId ? resolveProviderSlug(selectedId, providers, presets) : null),
    [selectedId, providers, presets],
  );

  const awaitingPresetCatalog = useMemo(() => {
    if (!selectedId || presetsLoaded) return false;
    return resolveProviderSlug(selectedId, providers, []) === null;
  }, [selectedId, presetsLoaded, providers]);

  if (awaitingPresetCatalog) {
    return (
      <section className="section">
        <div className="section-head">
          <p className="eyebrow">Providers</p>
          <h2>Loading provider…</h2>
          <p>Fetching provider catalog metadata.</p>
        </div>
      </section>
    );
  }

  const openAdd = (type?: string) => {
    setAddPreset(type);
    setShowAdd(true);
  };

  const ensureCatalogProvider = async (catalog: ProviderTypeInfo): Promise<string> => {
    const providerId = catalog.presetId || catalog.type;
    const existing = providers.find((p) => p.ID === providerId);
    if (existing) return existing.ID;

    await saveProvider(secret, {
      id: providerId,
      type: catalog.type,
      name: catalog.name,
      base_url: catalog.defaultBaseUrl,
      enabled: true,
    });
    onMutated();
    return providerId;
  };

  const handleConnectCatalog = async (catalog: ProviderTypeInfo, method: ConnectionMethod) => {
    if (!method.available) {
      onError(method.unavailableReason || "This connection method is not available yet.");
      return;
    }
    setConnectBusy(true);
    try {
      const providerId = await ensureCatalogProvider(catalog);
      setCatalogProviderId(providerId);
      switch (method.kind) {
        case "oauth":
          setOauthProviderId(providerId);
          setOauthProviderType(catalog.type);
          setOauthPresetId(catalog.presetId || null);
          setShowOAuth(true);
          break;
        case "api_key":
        case "cookie":
        case "service_account":
        case "none":
          setCatalogCredentialMethod(method);
          setShowCatalogCredential(true);
          break;
        case "import_cliproxy":
        case "import_9router":
          setShowImport(true);
          break;
        default: {
          const _exhaustive: never = method.kind;
          void _exhaustive;
        }
      }
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "Failed to prepare provider");
    } finally {
      setConnectBusy(false);
    }
  };

  const handleTestSection = async (providerIds: string[], section: string) => {
    if (providerIds.length === 0) {
      onError("No configured providers to test in this section");
      return;
    }
    setTestingSection(section);
    let ok = 0;
    let failed = 0;
    try {
      for (const id of providerIds) {
        try {
          const result = await checkProviderHealth(secret, id);
          if (result.ok) ok++;
          else failed++;
        } catch {
          failed++;
        }
      }
      onNotice(`Health check finished: ${ok} healthy, ${failed} failed`);
      onMutated();
    } finally {
      setTestingSection(null);
    }
  };

  const handleExportAuth = async () => {
    setAuthBusy(true);
    try {
      const blob = await exportAuthBundle(secret);
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = "tproxy-auth-bundle.json";
      anchor.click();
      URL.revokeObjectURL(url);
      onNotice("OAuth auth bundle exported");
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "Auth export failed");
    } finally {
      setAuthBusy(false);
    }
  };

  const handleImportAuth = async (file: File) => {
    setAuthBusy(true);
    try {
      const text = await file.text();
      const bundle = JSON.parse(text) as unknown;
      await importAuthBundle(secret, bundle);
      onNotice("OAuth auth bundle imported");
      onMutated();
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "Auth import failed");
    } finally {
      setAuthBusy(false);
    }
  };

  const handleDelete = async () => {
    if (!confirmDelete) return;
    const target = confirmDelete;
    setConfirmDelete(null);
    try {
      await deleteProvider(secret, target.ID);
      onNotice(`Provider ${target.ID} deleted`);
      onMutated();
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "Delete failed");
    }
  };

  if (resolved?.kind === "instance") {
    const provider = resolved.provider;
    return (
      <>
        <ProviderDetail
          provider={provider}
          credentials={credentials[provider.ID] || []}
          aliases={aliases}
          secret={secret}
          presets={presets}
          onOpenImport={() => setShowImport(true)}
          onBack={() => onSelect(null)}
          onMutated={onMutated}
          onNotice={onNotice}
          onError={onError}
        />
        <ImportModal
          open={showImport}
          secret={secret}
          onClose={() => setShowImport(false)}
          onNotice={onNotice}
          onError={onError}
          onMutated={onMutated}
        />
      </>
    );
  }

  if (resolved?.kind === "catalog") {
    const catalog = resolved.catalog;
    const catalogPreset = presets.find((item) => item.id === catalog.presetId) ?? null;
    const connectionProfile = resolveConnectionProfile(catalog, catalogPreset);
    return (
      <>
        <ProviderTypeDetail
          catalog={catalog}
          connectionProfile={connectionProfile}
          onBack={() => onSelect(null)}
          onSetup={() => openAdd(catalog.presetId || catalog.type)}
          onConnectionMethod={(method) => void handleConnectCatalog(catalog, method)}
          connectBusy={connectBusy}
        />
        <OAuthModal
          open={showOAuth}
          providerId={oauthProviderId || catalog.presetId || catalog.type}
          providerType={oauthProviderType || catalog.type}
          presetId={oauthPresetId || catalog.presetId}
          secret={secret}
          autoStart
          onClose={() => {
            setShowOAuth(false);
            setOauthProviderId(null);
            setOauthProviderType(null);
            setOauthPresetId(null);
          }}
          onComplete={() => {
            onNotice(`${catalog.name} connected`);
            onMutated();
            onSelect(catalog.presetId || catalog.type);
          }}
          onError={onError}
        />
        <AddCredentialModal
          open={showCatalogCredential}
          providerId={catalogProviderId || catalog.presetId || catalog.type}
          providerType={catalog.type}
          secret={secret}
          method={catalogCredentialMethod}
          onClose={() => {
            setShowCatalogCredential(false);
            setCatalogCredentialMethod(null);
          }}
          onSaved={() => {
            onNotice("Credential saved");
            onMutated();
            onSelect(catalog.presetId || catalog.type);
          }}
        />
        <ImportModal
          open={showImport}
          secret={secret}
          onClose={() => setShowImport(false)}
          onNotice={onNotice}
          onError={onError}
          onMutated={onMutated}
        />
        <AddProviderModal
          open={showAdd}
          secret={secret}
          presetType={addPreset}
          onClose={() => {
            setShowAdd(false);
            setAddPreset(undefined);
          }}
          onSaved={(providerId, providerType) => {
            onNotice(`Provider ${providerId} created`);
            onMutated();
            onSelect(providerType ?? providerId);
            setAddPreset(undefined);
          }}
        />
      </>
    );
  }

  return (
    <>
      <ImportDataCard onOpen={() => setShowImport(true)} />
      <ImportModal
        open={showImport}
        secret={secret}
        onClose={() => setShowImport(false)}
        onNotice={onNotice}
        onError={onError}
        onMutated={onMutated}
      />
      <ProviderList
        providers={providers}
        credentials={credentials}
        secret={secret}
        searchQuery={searchQuery}
        onAddOpenAI={() => openAdd("openai-compatible")}
        onAddAnthropic={() => openAdd("anthropic-compatible")}
        onTestSection={(section, ids) => void handleTestSection(ids, section)}
        testingSection={testingSection}
      />
      <div className="auth-bundle-toolbar">
        <button type="button" className="auth-bundle-btn" onClick={() => void handleExportAuth()} disabled={authBusy}>
          Export OAuth bundle
        </button>
        <label className="auth-bundle-btn" htmlFor={importInputId}>
          Import OAuth bundle
        </label>
        <input
          id={importInputId}
          type="file"
          accept="application/json,.json"
          hidden
          onChange={(event) => {
            const file = event.target.files?.[0];
            event.target.value = "";
            if (file) void handleImportAuth(file);
          }}
        />
      </div>
      <AddProviderModal
        open={showAdd}
        secret={secret}
        presetType={addPreset}
        onClose={() => {
          setShowAdd(false);
          setAddPreset(undefined);
        }}
        onSaved={(providerId, providerType) => {
          onNotice(`Provider ${providerId} created`);
          onMutated();
          onSelect(providerType ?? providerId);
          setAddPreset(undefined);
        }}
      />
      <ConfirmDialog
        open={confirmDelete !== null}
        title={`Delete provider ${confirmDelete?.ID}?`}
        message="This removes the provider configuration. Linked credentials remain but become unbound."
        confirmText="Delete provider"
        variant="danger"
        onConfirm={handleDelete}
        onClose={() => setConfirmDelete(null)}
      />
    </>
  );
}
