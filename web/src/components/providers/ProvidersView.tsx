import { useEffect, useMemo, useState } from "react";
import { ConfirmDialog } from "../ui";
import { AddProviderModal } from "./AddProviderModal";
import { ProviderDetail } from "./ProviderDetail";
import { ProviderList } from "./ProviderList";
import { ProviderTypeDetail } from "./ProviderTypeDetail";
import { resolveProviderSlug, type ProviderTypeInfo } from "./catalog";
import { checkProviderHealth, deleteProvider, exportAuthBundle, importAuthBundle, saveProvider } from "./api";
import { OAuthModal } from "./OAuthModal";
import type { Credential, ModelAlias, Provider, PublicModel, RouteTarget } from "./types";

type Props = {
  providers: Provider[];
  credentials: Record<string, Credential[]>;
  models: PublicModel[];
  routes: RouteTarget[];
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
  models,
  routes,
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
  const [oauthProviderId, setOauthProviderId] = useState<string | null>(null);
  const [oauthProviderType, setOauthProviderType] = useState<string | null>(null);
  const importInputId = "tproxy-auth-bundle-import";

  useEffect(() => {
    if (!selectedId) return;
    if (!resolveProviderSlug(selectedId, providers)) {
      onSelect(null);
    }
  }, [selectedId, providers, onSelect]);

  const resolved = useMemo(
    () => (selectedId ? resolveProviderSlug(selectedId, providers) : null),
    [selectedId, providers],
  );

  const openAdd = (type?: string) => {
    setAddPreset(type);
    setShowAdd(true);
  };

  const ensureCatalogProvider = async (catalog: ProviderTypeInfo): Promise<string> => {
    const existing = providers.find((p) => p.Type === catalog.type);
    if (existing) return existing.ID;

    const providerId = catalog.type;
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

  const handleConnectCatalog = async (catalog: ProviderTypeInfo) => {
    setConnectBusy(true);
    try {
      const providerId = await ensureCatalogProvider(catalog);
      setOauthProviderId(providerId);
      setOauthProviderType(catalog.type);
      setShowOAuth(true);
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
      <ProviderDetail
        provider={provider}
        credentials={credentials[provider.ID] || []}
        models={models}
        routes={routes}
        aliases={aliases}
        secret={secret}
        onBack={() => onSelect(null)}
        onMutated={onMutated}
        onNotice={onNotice}
        onError={onError}
      />
    );
  }

  if (resolved?.kind === "catalog") {
    return (
      <>
        <ProviderTypeDetail
          catalog={resolved.catalog}
          onBack={() => onSelect(null)}
          onSetup={() => openAdd(resolved.catalog.type)}
          onConnect={
            resolved.catalog.supportsOAuth ? () => void handleConnectCatalog(resolved.catalog) : undefined
          }
          connectBusy={connectBusy}
        />
        <OAuthModal
          open={showOAuth}
          providerId={oauthProviderId || resolved.catalog.type}
          providerType={oauthProviderType || resolved.catalog.type}
          secret={secret}
          autoStart
          onClose={() => {
            setShowOAuth(false);
            setOauthProviderId(null);
            setOauthProviderType(null);
          }}
          onComplete={() => {
            onNotice(`${resolved.catalog.name} connected`);
            onMutated();
            onSelect(resolved.catalog.type);
          }}
          onError={onError}
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
      <ProviderList
        providers={providers}
        credentials={credentials}
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
