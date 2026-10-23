import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { ConfirmDialog } from "../ui";
import { EmptyPanel } from "../page-kit";
import { ImportModal } from "../import/ImportModal";
import { AddProviderModal } from "./AddProviderModal";
import { ProviderDetail } from "./ProviderDetail";
import { ProviderList } from "./ProviderList";
import { ProviderTypeDetail } from "./ProviderTypeDetail";
import { resolveProviderSlug, type ProviderTypeInfo } from "./catalog";
import { resolveConnectionProfile, type ConnectionMethod } from "./connectionMethods";
import { checkProviderHealth, deleteProvider, exportAuthBundle, fetchNinerouterPresets, saveProvider, type NinerouterPreset } from "./api";
import { AddCredentialModal } from "./AddCredentialModal";
import { CursorImportModal } from "./CursorImportModal";
import { KiroOAuthModal } from "./KiroOAuthModal";
import { OAuthModal } from "./OAuthModal";
import type { Credential, ModelAlias, Provider } from "./types";

type Props = {
  providers: Provider[];
  credentials: Record<string, Credential[]>;
  aliases: ModelAlias[];
  secret: string;
  selectedId: string | null;
  snapshotLoading?: boolean;
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
  selectedId,
  snapshotLoading = false,
  onSelect,
  onMutated,
  onNotice,
  onError,
}: Props) {
  const { t } = useTranslation();
  const [addPreset, setAddPreset] = useState<string | undefined>();
  const [showAdd, setShowAdd] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState<Provider | null>(null);
  const [testingSection, setTestingSection] = useState<string | null>(null);
  const [checkingIds, setCheckingIds] = useState<Set<string>>(() => new Set());
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
  const [showCursorImport, setShowCursorImport] = useState(false);
  const [showKiroOAuth, setShowKiroOAuth] = useState(false);

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
    if (!selectedId || snapshotLoading || !presetsLoaded) return;
    if (!resolveProviderSlug(selectedId, providers, presets)) {
      onSelect(null);
    }
  }, [selectedId, providers, presets, presetsLoaded, snapshotLoading, onSelect]);

  const resolved = useMemo(
    () => (selectedId ? resolveProviderSlug(selectedId, providers, presets) : null),
    [selectedId, providers, presets],
  );

  const awaitingProviderResolution = useMemo(() => {
    if (!selectedId) return false;
    if (resolved) return false;
    if (snapshotLoading) return true;
    if (!presetsLoaded) {
      return resolveProviderSlug(selectedId, providers, []) === null;
    }
    return false;
  }, [selectedId, snapshotLoading, presetsLoaded, providers, resolved]);

  if (awaitingProviderResolution) {
    return (
      <section className="cp-page">
        <EmptyPanel loading title={t("providers.providersViewLoadingProvider")} description={t("providers.providersViewLoadingDesc")} />
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
      onError(method.unavailableReason || t("providers.methodUnavailable"));
      return;
    }
    // File imports create only the providers actually present in the export.
    // Opening or cancelling the picker must not leave an empty provider behind.
    if (method.kind === "import_data") {
      setShowImport(true);
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
        case "import_cursor":
          setShowCursorImport(true);
          break;
        case "connect_kiro":
          setShowKiroOAuth(true);
          break;
        default: {
          const _exhaustive: never = method.kind;
          void _exhaustive;
        }
      }
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("providers.failedPrepare"));
    } finally {
      setConnectBusy(false);
    }
  };

  const handleTestSection = async (providerIds: string[], section: string) => {
    if (providerIds.length === 0) {
      onError(t("providers.noProvidersToTest"));
      return;
    }
    setTestingSection(section);
    setCheckingIds(new Set(providerIds));
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
        setCheckingIds((current) => {
          const next = new Set(current);
          next.delete(id);
          return next;
        });
      }
      onNotice(t("providers.healthCheckFinished", { ok, failed }));
      onMutated();
    } finally {
      setTestingSection(null);
      setCheckingIds(new Set());
    }
  };

  const handleCheckProvider = async (provider: Provider) => {
    setCheckingIds((current) => new Set(current).add(provider.ID));
    try {
      const result = await checkProviderHealth(secret, provider.ID);
      const summary = result.checked != null
        ? t("providers.list.checkSummary", { healthy: result.healthy ?? 0, checked: result.checked })
        : result.status || t("providers.providerHealthStatusChecked");
      const suffix = !result.ok && result.last_error ? t("providers.providerHealthSummarySuffix", { error: result.last_error }) : "";
      onNotice(`${t("providers.providerHealthSummary", { id: provider.Name || provider.ID, summary })}${suffix}`);
      onMutated();
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("providers.healthCheckFailed"));
    } finally {
      setCheckingIds((current) => {
        const next = new Set(current);
        next.delete(provider.ID);
        return next;
      });
    }
  };

  const handleCopyId = async (provider: Provider) => {
    try {
      await navigator.clipboard.writeText(provider.ID);
      onNotice(t("providers.list.copiedId", { id: provider.ID }));
    } catch {
      onError(t("providers.list.copyFailed"));
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
      onNotice(t("providers.authExportSuccess"));
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("providers.authExportFailed"));
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
      onNotice(t("providers.providerDeleted", { id: target.ID }));
      onMutated();
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("providers.deleteFailed"));
    }
  };

  const importModal = (
    <ImportModal
      key="import"
      open={showImport}
      secret={secret}
      onClose={() => setShowImport(false)}
      onNotice={onNotice}
      onError={onError}
      onMutated={onMutated}
    />
  );

  const addProviderModal = (
    <AddProviderModal
      open={showAdd}
      secret={secret}
      presetType={addPreset}
      onClose={() => {
        setShowAdd(false);
        setAddPreset(undefined);
      }}
      onSaved={(providerId) => {
        onNotice(t("providers.providerCreated", { id: providerId }));
        onMutated();
        // Open the instance just created. Selecting its type resolved to the
        // first provider of that type (e.g. OpenRouter for a new openai-compatible one).
        onSelect(providerId);
        setAddPreset(undefined);
      }}
    />
  );

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
        {importModal}
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
            onNotice(t("providers.providerConnected", { name: catalog.name }));
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
            onNotice(t("providers.credentialSaved"));
            onMutated();
            onSelect(catalog.presetId || catalog.type);
          }}
        />
        {importModal}
        <CursorImportModal
          open={showCursorImport}
          secret={secret}
          providerId={catalogProviderId || catalog.presetId || catalog.type}
          onClose={() => setShowCursorImport(false)}
          onComplete={() => {
            onNotice(t("providers.cursorImported"));
            onMutated();
            onSelect(catalog.presetId || catalog.type);
          }}
          onError={onError}
        />
        <KiroOAuthModal
          open={showKiroOAuth}
          secret={secret}
          providerId={catalogProviderId || catalog.presetId || catalog.type}
          providerType={catalog.type}
          onClose={() => setShowKiroOAuth(false)}
          onComplete={() => {
            onNotice(t("providers.kiroConnected"));
            onMutated();
            onSelect(catalog.presetId || catalog.type);
          }}
          onError={onError}
        />
        {addProviderModal}
      </>
    );
  }

  return (
    <>
      <ProviderList
        providers={providers}
        credentials={credentials}
        presets={presets}
        presetsLoading={!presetsLoaded}
        snapshotLoading={snapshotLoading}
        checkingIds={checkingIds}
        testingSection={testingSection}
        exportBusy={authBusy}
        onAdd={openAdd}
        onImport={() => setShowImport(true)}
        onExportAuth={() => void handleExportAuth()}
        onCheck={(provider) => void handleCheckProvider(provider)}
        onTestSection={(section, ids) => void handleTestSection(ids, section)}
        onCopyId={(provider) => void handleCopyId(provider)}
        onDelete={setConfirmDelete}
      />
      {importModal}
      {addProviderModal}
      <ConfirmDialog
        open={confirmDelete !== null}
        title={t("providers.deleteProviderConfirm", { id: confirmDelete?.Name || confirmDelete?.ID })}
        message={t("providers.deleteProviderMessage")}
        confirmText={t("providers.deleteProvider")}
        cancelText={t("common.cancel")}
        variant="danger"
        onConfirm={handleDelete}
        onClose={() => setConfirmDelete(null)}
      />
    </>
  );
}
