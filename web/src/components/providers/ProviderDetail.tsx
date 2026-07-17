import { useEffect, useState } from "react";
import { Badge, Button, Card, ConfirmDialog, EmptyState, Toggle } from "../ui";
import { CooldownTimer } from "./CooldownTimer";
import { CredentialModelsModal } from "./CredentialModelsModal";
import { EditConnectionModal } from "./EditConnectionModal";
import { ModelAvailabilityBadge } from "./ModelAvailabilityBadge";
import { ModelsSection } from "./ModelsSection";
import { OAuthModal } from "./OAuthModal";
import { AddCredentialModal } from "./AddCredentialModal";
import { getProviderTypeInfo } from "./catalog";
import { checkCredentialHealth, checkProviderHealth, clearCredentialCooldown, deleteCredential, deleteProvider, listProxyPools, refreshCredential, saveCredential, type ProxyPoolOption } from "./api";
import { credentialStatusLabel, isOnCooldown, type Credential, type ModelAlias, type Provider, type PublicModel, type RouteTarget } from "./types";

type Props = {
  provider: Provider;
  credentials: Credential[];
  models: PublicModel[];
  routes: RouteTarget[];
  aliases: ModelAlias[];
  secret: string;
  onBack: () => void;
  onMutated: () => void;
  onNotice: (message: string) => void;
  onError: (message: string) => void;
};

/** Provider detail page — header + connections card + discovered models.
 *  Ported from 9router [id]/page.js, adapted to tdproxy's snapshot model. */
export function ProviderDetail({ provider, credentials, models, routes, aliases, secret, onBack, onMutated, onNotice, onError }: Props) {
  const info = getProviderTypeInfo(provider.Type);
  const [showAddCredential, setShowAddCredential] = useState(false);
  const [showOAuth, setShowOAuth] = useState(false);
  const [reAuthCredential, setReAuthCredential] = useState<Credential | null>(null);
  const [editingCredential, setEditingCredential] = useState<Credential | null>(null);
  const [confirmDeleteProvider, setConfirmDeleteProvider] = useState(false);
  const [confirmDeleteCred, setConfirmDeleteCred] = useState<Credential | null>(null);
  const [healthBusy, setHealthBusy] = useState(false);
  const [discoverNonce, setDiscoverNonce] = useState(0);
  const [modelsCredential, setModelsCredential] = useState<Credential | null>(null);
  const [proxyPools, setProxyPools] = useState<ProxyPoolOption[]>([]);

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
          ? `${result.healthy ?? 0}/${result.checked} healthy`
          : result.status || "checked";
      onNotice(result.ok ? `${provider.ID}: ${summary}` : `${provider.ID}: ${summary}${result.last_error ? ` — ${result.last_error}` : ""}`);
      onMutated();
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "Health check failed");
    } finally {
      setHealthBusy(false);
    }
  };

  const handleDiscover = () => {
    setDiscoverNonce((value) => value + 1);
    onNotice(`Refreshing models for ${provider.ID}`);
  };

  const handleDeleteProvider = async () => {
    setConfirmDeleteProvider(false);
    try {
      await deleteProvider(secret, provider.ID);
      onNotice(`Provider ${provider.ID} deleted`);
      onMutated();
      onBack();
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "Delete failed");
    }
  };

  const handleDeleteCredential = async () => {
    if (!confirmDeleteCred) return;
    const cred = confirmDeleteCred;
    setConfirmDeleteCred(null);
    try {
      await deleteCredential(secret, cred.id);
      onNotice(`Credential ${cred.id} deleted`);
      onMutated();
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "Delete failed");
    }
  };

  return (
    <div>
      <button className="detail-back" onClick={onBack}>
        <span className="material-symbols-outlined">arrow_back</span>
        Back to providers
      </button>

      {/* Header */}
      <div className="detail-header">
        <span className="provider-logo">
          <span className="material-symbols-outlined">{info.icon}</span>
        </span>
        <div className="detail-title-block">
          <h2>{provider.Name || provider.ID}</h2>
          <div className="detail-meta">
            <Badge variant="primary" size="sm">{provider.Type}</Badge>
            <Badge variant={provider.Enabled ? "success" : "default"} size="sm" dot>
              {provider.Enabled ? "enabled" : "disabled"}
            </Badge>
            {provider.Status && (
              <Badge variant={provider.Status === "healthy" ? "success" : provider.Status === "auth_required" ? "error" : "warning"} size="sm">
                {provider.Status}
              </Badge>
            )}
          </div>
          <div className="detail-meta" style={{ marginTop: 8 }}>
            {provider.BaseURL && <span className="detail-url">{provider.BaseURL}</span>}
            {info.website && (
              <a className="detail-link" href={info.website} target="_blank" rel="noreferrer">
                Website <span className="material-symbols-outlined" style={{ fontSize: 14 }}>open_in_new</span>
              </a>
            )}
            {info.apiKeyUrl && (
              <a className="detail-link" href={info.apiKeyUrl} target="_blank" rel="noreferrer">
                Get API key <span className="material-symbols-outlined" style={{ fontSize: 14 }}>key</span>
              </a>
            )}
          </div>
        </div>
        <div className="detail-header-actions">
          <Button variant="outline" size="md" icon="monitor_heart" onClick={handleHealth} loading={healthBusy}>
            Health check
          </Button>
          <Button variant="outline" size="md" icon="search" onClick={handleDiscover}>
            Discover models
          </Button>
          <Button variant="danger" size="md" icon="delete" onClick={() => setConfirmDeleteProvider(true)}>
            Delete
          </Button>
        </div>
      </div>

      {/* Connections card */}
      <Card pad="md" className="section" title="Connections" icon="vpn_key"
        action={
          <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
            <ModelAvailabilityBadge credentials={credentials} />
            {info.supportsOAuth && (
              <Button variant="primary" size="sm" icon="lock_person" onClick={() => setShowOAuth(true)}>
                Connect {info.name}
              </Button>
            )}
            <Button variant="secondary" size="sm" icon="key" onClick={() => setShowAddCredential(true)}>
              Add credential
            </Button>
          </div>
        }
      >
        {credentials.length === 0 ? (
          <EmptyState
            icon="key_off"
            text="No connections yet."
            hint={info.supportsOAuth ? "Add an API key credential or connect via OAuth." : "Add an API key credential to enable this provider."}
          />
        ) : (
          credentials.map((cred) => (
            <ConnectionRow
              key={cred.id}
              providerId={provider.ID}
              credential={cred}
              secret={secret}
              supportsOAuth={info.supportsOAuth}
              onEdit={(c) => setEditingCredential(c)}
              onDeleted={(c) => setConfirmDeleteCred(c)}
              onReAuth={(c) => setReAuthCredential(c)}
              onShowModels={(c) => setModelsCredential(c)}
              onMutated={onMutated}
              onNotice={onNotice}
              onError={onError}
            />
          ))
        )}
      </Card>

      {/* Models routing + aliases */}
      <ModelsSection
        providerId={provider.ID}
        credentials={credentials}
        models={models}
        routes={routes}
        secret={secret}
        discoverNonce={discoverNonce}
      />

      {/* Modals */}
      <CredentialModelsModal
        open={modelsCredential !== null}
        credential={modelsCredential}
        secret={secret}
        onClose={() => setModelsCredential(null)}
      />
      <AddCredentialModal
        open={showAddCredential}
        providerId={provider.ID}
        providerType={provider.Type}
        secret={secret}
        onClose={() => setShowAddCredential(false)}
        onSaved={() => {
          onNotice("Credential saved");
          onMutated();
        }}
      />
      <OAuthModal
        open={showOAuth}
        providerId={provider.ID}
        providerType={provider.Type}
        secret={secret}
        onClose={() => setShowOAuth(false)}
        onComplete={() => {
          onNotice("OAuth credential connected");
          onMutated();
        }}
        onError={onError}
      />
      <OAuthModal
        open={reAuthCredential !== null}
        providerId={provider.ID}
        providerType={provider.Type}
        secret={secret}
        credentialId={reAuthCredential?.id}
        initialLabel={reAuthCredential?.label}
        initialEmail={reAuthCredential?.email}
        autoStart
        onClose={() => setReAuthCredential(null)}
        onComplete={() => {
          onNotice(`Credential ${reAuthCredential?.id ?? ""} re-authenticated`);
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
          onNotice("Credential updated");
          onMutated();
        }}
      />
      <ConfirmDialog
        open={confirmDeleteProvider}
        title={`Delete provider ${provider.ID}?`}
        message="This removes the provider and unbinds its credentials. Credentials are not deleted."
        confirmText="Delete provider"
        variant="danger"
        onConfirm={handleDeleteProvider}
        onClose={() => setConfirmDeleteProvider(false)}
      />
      <ConfirmDialog
        open={confirmDeleteCred !== null}
        title={`Delete credential ${confirmDeleteCred?.id}?`}
        message="This permanently removes the credential and its encrypted secret."
        confirmText="Delete credential"
        variant="danger"
        onConfirm={handleDeleteCredential}
        onClose={() => setConfirmDeleteCred(null)}
      />
    </div>
  );
}

/** A single credential row inside the connections card. */
function ConnectionRow({
  providerId,
  credential,
  secret,
  supportsOAuth,
  onDeleted,
  onMutated,
  onEdit,
  onReAuth,
  onShowModels,
  onNotice,
  onError,
}: {
  providerId: string;
  credential: Credential;
  secret: string;
  supportsOAuth: boolean;
  onEdit: (credential: Credential) => void;
  onDeleted: (credential: Credential) => void;
  onReAuth: (credential: Credential) => void;
  onShowModels: (credential: Credential) => void;
  onMutated: () => void;
  onNotice: (message: string) => void;
  onError: (message: string) => void;
}) {
  const [busy, setBusy] = useState(false);
  const status = credentialStatusLabel(credential);
  const authIcon = credential.auth_type === "oauth" ? "lock_person" : credential.auth_type === "none" ? "lock_open" : "key";
  const hasProxy = (credential.proxy_pool_ids?.length ?? 0) > 0;
  const onCooldown = credential.cooldown_until && isOnCooldown(credential.cooldown_until);
  const needsReAuth = credential.status === "auth_required" && credential.auth_type === "oauth";

  const handleToggle = async () => {
    setBusy(true);
    try {
      // Re-save with toggled enabled flag via the credential save endpoint.
      await saveCredential(secret, {
        provider_id: providerId,
        credential: {
          id: credential.id,
          label: credential.label,
          email: credential.email,
          auth_type: credential.auth_type as "api_key" | "oauth" | "service_account" | "none",
          priority: 0,
          weight: 1,
          enabled: !credential.enabled,
          proxy_pools: credential.proxy_pool_ids,
        },
      });
      onNotice(`Credential ${credential.id} ${credential.enabled ? "disabled" : "enabled"}`);
      onMutated();
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "Toggle failed");
    } finally {
      setBusy(false);
    }
  };

  const handleHealthCheck = async () => {
    setBusy(true);
    try {
      const result = await checkCredentialHealth(secret, credential.id);
      onNotice(
        result.ok
          ? `${credential.id} is ${result.status || "healthy"}`
          : `${credential.id}: ${result.status || "failed"}${result.last_error ? ` — ${result.last_error}` : ""}`,
      );
      onMutated();
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "Health check failed");
    } finally {
      setBusy(false);
    }
  };

  const handleRefresh = async () => {
    setBusy(true);
    try {
      await refreshCredential(secret, credential.id);
      onNotice(`Token refreshed for ${credential.id}`);
      onMutated();
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "Token refresh failed");
    } finally {
      setBusy(false);
    }
  };

  const handleClearCooldown = async () => {
    setBusy(true);
    try {
      await clearCredentialCooldown(secret, credential.id);
      onNotice(`Cooldown cleared for ${credential.id}`);
      onMutated();
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "Clear cooldown failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="connection-row">
      <span className="connection-auth-icon">
        <span className="material-symbols-outlined">{authIcon}</span>
      </span>
      <div className="connection-main">
        <div className="connection-label">{credential.email || credential.label || credential.id}</div>
        {(credential.email || credential.label) && (
          <div className="connection-sub">
            <code style={{ color: "var(--color-brand-600)" }}>{credential.id}</code>
          </div>
        )}
        {credential.last_error && <div className="connection-error">{credential.last_error}</div>}
        <div className="connection-badges">
          <Badge variant={status.tone} size="sm" dot>{status.label}</Badge>
          <Badge variant="default" size="sm">{credential.auth_type}</Badge>
          {hasProxy && (
            <Badge variant="info" size="sm" icon="lan">{credential.proxy_pool_ids!.length} proxy</Badge>
          )}
          {credential.cooldown_until && isOnCooldown(credential.cooldown_until) && (
            <CooldownTimer until={credential.cooldown_until} onExpire={onMutated} />
          )}
        </div>
      </div>
      <div className="connection-actions">
        <Button variant="ghost" size="sm" icon="apps" onClick={() => onShowModels(credential)} aria-label="Supported models" title="View supported models" />
        <Button variant="ghost" size="sm" icon="monitor_heart" onClick={handleHealthCheck} loading={busy} aria-label="Health check" title="Health check this connection" />
        {credential.auth_type === "oauth" && (
          <Button variant="ghost" size="sm" icon="sync" onClick={handleRefresh} loading={busy} aria-label="Refresh token" title="Force OAuth token refresh" />
        )}
        {onCooldown && (
          <Button variant="ghost" size="sm" icon="timer_off" onClick={handleClearCooldown} loading={busy} aria-label="Clear cooldown" title="Clear cooldown" />
        )}
        {needsReAuth && supportsOAuth && (
          <Button variant="ghost" size="sm" icon="lock_reset" onClick={() => onReAuth(credential)} aria-label="Re-authenticate" title="Re-authenticate OAuth" />
        )}
        <Button variant="ghost" size="sm" icon="edit" onClick={() => onEdit(credential)} aria-label="Edit credential" />
        <Toggle checked={credential.enabled} onChange={handleToggle} disabled={busy} aria-label="Toggle credential" />
        <Button variant="ghost" size="sm" icon="delete" onClick={() => onDeleted(credential)} aria-label="Delete credential" />
      </div>
    </div>
  );
}
