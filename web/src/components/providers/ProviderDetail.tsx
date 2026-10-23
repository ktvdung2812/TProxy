import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { ConfirmDialog } from "../ui";
import { ActionMenu, Tag, ToolButton } from "../page-kit";
import { CredentialModelsModal } from "./CredentialModelsModal";
import { EditConnectionModal } from "./EditConnectionModal";
import { ModelsSection } from "./ModelsSection";
import { OAuthModal } from "./OAuthModal";
import { KiroOAuthModal } from "./KiroOAuthModal";
import { CursorImportModal } from "./CursorImportModal";
import { AddCredentialModal } from "./AddCredentialModal";
import { ProviderAccounts } from "./ProviderAccounts";
import { ProviderIdentity } from "./ProviderIdentity";
import { HealthPill, providerLogoId } from "./ProviderListItem";
import { ProviderRotationCard } from "./ProviderRotationCard";
import { catalogWithPreset, resolveConnectionProfile, type ConnectionMethod } from "./connectionMethods";
import { checkProviderHealth, deleteProvider, listProxyPools, type NinerouterPreset, type ProxyPoolOption } from "./api";
import { providerHealth, relativeAge } from "./providerHealth";
import { maskAccountText } from "../../lib/accountMask";
import { useAccountMask } from "../../hooks/useAccountMask";
import type { Credential, ModelAlias, Provider } from "./types";

type Props = {
  provider: Provider;
  credentials: Credential[];
  aliases: ModelAlias[];
  secret: string;
  presets: NinerouterPreset[];
  onOpenImport?: () => void;
  onBack: () => void;
  onMutated: () => void;
  onNotice: (message: string) => void;
  onError: (message: string) => void;
};

/** Provider detail page — identity, accounts (with bulk actions), models and rotation.
 *  Ported from 9router [id]/page.js, adapted to tdproxy's snapshot model. */
export function ProviderDetail({
  provider,
  credentials,
  secret,
  presets,
  onOpenImport,
  onBack,
  onMutated,
  onNotice,
  onError,
}: Props) {
  const catalog = useMemo(
    () => catalogWithPreset(provider.Type, provider.ID, presets),
    [provider.Type, provider.ID, presets],
  );
  const { t } = useTranslation();
  const { masked } = useAccountMask();
  const preset = useMemo(() => presets.find((item) => item.id === provider.ID) ?? null, [presets, provider.ID]);
  const connectionProfile = useMemo(() => resolveConnectionProfile(catalog, preset), [catalog, preset]);
  const [showAddCredential, setShowAddCredential] = useState(false);
  const [credentialMethod, setCredentialMethod] = useState<ConnectionMethod | null>(null);
  const [showOAuth, setShowOAuth] = useState(false);
  const [oauthAutoStart, setOauthAutoStart] = useState(false);
  const [reAuthCredential, setReAuthCredential] = useState<Credential | null>(null);
  const [editingCredential, setEditingCredential] = useState<Credential | null>(null);
  const [confirmDeleteProvider, setConfirmDeleteProvider] = useState(false);
  const [healthBusy, setHealthBusy] = useState(false);
  const [discoverNonce, setDiscoverNonce] = useState(0);
  const [modelsCredential, setModelsCredential] = useState<Credential | null>(null);
  const [showCursorImport, setShowCursorImport] = useState(false);
  const [showKiroOAuth, setShowKiroOAuth] = useState(false);
  const [proxyPools, setProxyPools] = useState<ProxyPoolOption[]>([]);
  const name = provider.Name || provider.ID;
  const health = providerHealth(provider, credentials, Boolean(catalog.noAuth));
  const checked = relativeAge(provider.LastChecked);

  // Fetch proxy pools once for the EditConnectionModal binding dropdown.
  useEffect(() => {
    let cancelled = false;
    listProxyPools(secret)
      .then((result) => { if (!cancelled) setProxyPools(result.proxy_pools || []); })
      .catch(() => { /* non-fatal */ });
    return () => { cancelled = true; };
  }, [secret]);

  const handleHealth = async () => {
    setHealthBusy(true);
    try {
      const result = await checkProviderHealth(secret, provider.ID);
      const summary =
        result.checked != null
          ? t("providers.list.checkSummary", { healthy: result.healthy ?? 0, checked: result.checked })
          : result.status || t("providers.providerHealthStatusChecked");
      const suffix = !result.ok && result.last_error ? t("providers.providerHealthSummarySuffix", { error: result.last_error }) : "";
      onNotice(`${t("providers.providerHealthSummary", { id: provider.ID, summary })}${suffix}`);
      onMutated();
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("providers.healthCheckFailed"));
    } finally {
      setHealthBusy(false);
    }
  };

  const handleDiscover = () => {
    setDiscoverNonce((value) => value + 1);
    onNotice(t("providers.refreshingModels", { id: provider.ID }));
  };

  const handleDeleteProvider = async () => {
    setConfirmDeleteProvider(false);
    try {
      await deleteProvider(secret, provider.ID);
      onNotice(t("providers.providerDeleted", { id: provider.ID }));
      onMutated();
      onBack();
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("providers.deleteFailed"));
    }
  };

  const handleCopyId = async () => {
    try {
      await navigator.clipboard.writeText(provider.ID);
      onNotice(t("providers.list.copiedId", { id: provider.ID }));
    } catch {
      onError(t("providers.list.copyFailed"));
    }
  };

  const handleConnectionMethod = (method: ConnectionMethod) => {
    if (!method.available) {
      onError(method.unavailableReason || t("providers.connectionMethodUnavailable"));
      return;
    }
    switch (method.kind) {
      case "oauth":
        setOauthAutoStart(true);
        setShowOAuth(true);
        break;
      case "api_key":
      case "cookie":
      case "service_account":
      case "none":
        setCredentialMethod(method);
        setShowAddCredential(true);
        break;
      case "import_data":
        onOpenImport?.();
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
  };

  const lastError = provider.LastError?.trim();

  return (
    <section className="cp-page pv-detail">
      <ProviderIdentity
        name={name}
        code={provider.ID}
        providerId={provider.ID}
        providerType={provider.Type}
        logoId={providerLogoId(provider.ID, provider.Type, Boolean(preset))}
        color={catalog.color}
        baseUrl={provider.BaseURL}
        website={catalog.website}
        apiKeyUrl={catalog.apiKeyUrl}
        onBack={onBack}
        tags={
          <>
            <HealthPill health={health} />
            {provider.Type !== provider.ID ? <Tag tone="primary">{provider.Type}</Tag> : null}
            {(provider.ProxyPoolIDs?.length ?? 0) > 0 ? (
              <Tag tone="neutral" title={provider.ProxyPoolIDs?.join(", ")}>{t("providers.list.proxyPools", { count: provider.ProxyPoolIDs!.length })}</Tag>
            ) : null}
            <span className="pv-identity-checked" title={provider.LastChecked ? new Date(provider.LastChecked).toLocaleString() : undefined}>
              {checked ? t("providers.list.checkedAgo", { time: checked }) : t("providers.list.neverChecked")}
            </span>
          </>
        }
        description={
          lastError && health !== "healthy" && health !== "off" ? (
            <span className="pv-identity-error">
              <span className="material-symbols-outlined" aria-hidden>error</span>
              {lastError}
            </span>
          ) : null
        }
        actions={
          <>
            <ToolButton icon="monitor_heart" spinning={healthBusy} disabled={healthBusy} onClick={() => void handleHealth()} title={t("providers.identity.healthHint")}>
              {t("providers.healthCheck")}
            </ToolButton>
            <ActionMenu
              label={t("providers.menu.more")}
              items={[
                { key: "discover", icon: "travel_explore", label: t("providers.discoverModels"), hint: t("providers.identity.discoverHint"), onSelect: handleDiscover },
                { key: "copy", icon: "content_copy", label: t("providers.menu.copyId"), hint: provider.ID, onSelect: () => void handleCopyId() },
                catalog.website ? { key: "site", icon: "open_in_new", label: t("providers.website"), hint: catalog.website, onSelect: () => window.open(catalog.website, "_blank", "noopener,noreferrer") } : null,
                onOpenImport ? { key: "import", icon: "upload_file", label: t("import.cardTitle"), hint: t("providers.list.importHint"), onSelect: onOpenImport } : null,
                { key: "delete", icon: "delete", label: t("providers.deleteProvider"), danger: true, onSelect: () => setConfirmDeleteProvider(true) },
              ]}
            />
          </>
        }
      />

      <ProviderAccounts
        provider={provider}
        credentials={credentials}
        secret={secret}
        presets={presets}
        connectionProfile={connectionProfile}
        onMethod={handleConnectionMethod}
        onEdit={setEditingCredential}
        onModels={setModelsCredential}
        onReAuth={setReAuthCredential}
        onMutated={onMutated}
        onNotice={onNotice}
        onError={onError}
      />

      <div className="pv-detail-grid">
        <ModelsSection
          providerId={provider.ID}
          credentials={credentials}
          secret={secret}
          discoverNonce={discoverNonce}
          onDiscover={handleDiscover}
        />
        <ProviderRotationCard
          providerId={provider.ID}
          providerName={name}
          accountCount={credentials.length}
          secret={secret}
          onSaved={onNotice}
          onError={onError}
        />
      </div>

      {/* Modals */}
      <CredentialModelsModal
        open={modelsCredential !== null}
        credential={modelsCredential}
        providerId={provider.ID}
        secret={secret}
        onClose={() => setModelsCredential(null)}
      />
      <AddCredentialModal
        open={showAddCredential}
        providerId={provider.ID}
        providerType={provider.Type}
        secret={secret}
        method={credentialMethod}
        onClose={() => {
          setShowAddCredential(false);
          setCredentialMethod(null);
        }}
        onSaved={() => {
          onNotice(t("providers.credentialSaved"));
          onMutated();
        }}
      />
      <OAuthModal
        open={showOAuth}
        providerId={provider.ID}
        providerType={provider.Type}
        presetId={catalog.presetId}
        secret={secret}
        autoStart={oauthAutoStart}
        onClose={() => {
          setShowOAuth(false);
          setOauthAutoStart(false);
        }}
        onComplete={() => {
          onNotice(t("providers.account.oauthConnected"));
          setOauthAutoStart(false);
          onMutated();
        }}
        onError={onError}
      />
      <CursorImportModal
        open={showCursorImport}
        secret={secret}
        providerId={provider.ID}
        onClose={() => setShowCursorImport(false)}
        onComplete={() => {
          onNotice(t("providers.cursorImported"));
          onMutated();
        }}
        onError={onError}
      />
      <KiroOAuthModal
        open={showKiroOAuth}
        secret={secret}
        providerId={provider.ID}
        providerType={provider.Type}
        onClose={() => setShowKiroOAuth(false)}
        onComplete={() => {
          onNotice(t("providers.kiroConnected"));
          onMutated();
        }}
        onError={onError}
      />
      <OAuthModal
        open={reAuthCredential !== null}
        providerId={provider.ID}
        providerType={provider.Type}
        presetId={catalog.presetId}
        secret={secret}
        credentialId={reAuthCredential?.id}
        initialLabel={reAuthCredential?.label}
        initialEmail={reAuthCredential?.email}
        autoStart
        onClose={() => setReAuthCredential(null)}
        onComplete={() => {
          onNotice(t("providers.credentialReAuthenticated", { id: maskAccountText(reAuthCredential?.id ?? "", masked) }));
          setReAuthCredential(null);
          onMutated();
        }}
        onError={onError}
      />
      <EditConnectionModal
        open={editingCredential !== null}
        providerId={provider.ID}
        credential={editingCredential}
        proxyPools={proxyPools}
        secret={secret}
        onClose={() => setEditingCredential(null)}
        onSaved={() => {
          onNotice(t("providers.credentialUpdated"));
          onMutated();
        }}
      />
      <ConfirmDialog
        open={confirmDeleteProvider}
        title={t("providers.deleteProviderConfirm", { id: provider.ID })}
        message={t("providers.identity.deleteMessage")}
        confirmText={t("providers.deleteProvider")}
        cancelText={t("common.cancel")}
        variant="danger"
        onConfirm={handleDeleteProvider}
        onClose={() => setConfirmDeleteProvider(false)}
      />
    </section>
  );
}
