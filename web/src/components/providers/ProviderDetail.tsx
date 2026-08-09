import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Badge, Button, Card, ConfirmDialog, EmptyState, Input, Select, Toggle } from "../ui";
import { CooldownTimer } from "./CooldownTimer";
import { CredentialModelsModal } from "./CredentialModelsModal";
import { EditConnectionModal } from "./EditConnectionModal";
import { ModelAvailabilityBadge } from "./ModelAvailabilityBadge";
import { ConnectionStatsInline } from "./ConnectionStatsInline";
import { ModelsSection } from "./ModelsSection";
import { OAuthModal } from "./OAuthModal";
import { KiroOAuthModal } from "./KiroOAuthModal";
import { CursorImportModal } from "./CursorImportModal";
import { AddCredentialModal } from "./AddCredentialModal";
import { ProviderConnectionActions } from "./ProviderConnectionActions";
import { ProviderRotationCard } from "./ProviderRotationCard";
import {
  catalogWithPreset,
  resolveConnectionProfile,
  type ConnectionMethod,
} from "./connectionMethods";
import { ProviderLogo } from "./ProviderLogo";
import { checkCredentialHealth, checkProviderHealth, clearCredentialCooldown, deleteCredential, deleteProvider, listProxyPools, refreshCredential, saveCredential, type NinerouterPreset, type ProxyPoolOption } from "./api";
import {
  fetchCredentialProxyUsage,
  fetchCredentialQuota,
  type CredentialProxyUsage,
  type CredentialQuota,
} from "../quota/api";
import { formatProxyUsageLabel, getColorTone } from "../quota/utils";
import { credentialStatusLabel, isOnCooldown, buildCredentialAccountNumbers, compareCredentialsByCreatedAt, formatCredentialAddedAt, formatServicePlanLabel, type Credential, type ModelAlias, type Provider } from "./types";

/** Providers whose upstream quota/balance probe is implemented in tproxy. */
const CONNECTION_QUOTA_PROVIDER_IDS = new Set([
  "deepseek",
  "codex",
  "claude",
  "copilot",
  "github",
  "antigravity",
  "gemini-cli",
  "glm",
  "glm-cn",
  "minimax",
  "minimax-cn",
  "kiro",
  "qoder",
  "qwen",
  "xai",
  "grok-cli",
  "vercel-ai-gateway",
  "codebuddy-cn",
  "ollama",
  "kimi",
  "kimi-coding",
]);

function providerSupportsUpstreamQuota(providerId: string, presets: NinerouterPreset[]) {
  if (CONNECTION_QUOTA_PROVIDER_IDS.has(providerId)) return true;
  return Boolean(presets.find((preset) => preset.id === providerId)?.supports_quota);
}

function quotaBadgesFromQuota(quota: CredentialQuota | null | undefined): Array<{
  key: string;
  label: string;
  tone: "success" | "warning" | "error" | "info" | "default";
}> {
  if (!quota?.quotas) return [];
  return Object.entries(quota.quotas).map(([key, entry]) => {
    const name = (entry.name || key).trim();
    if (entry.unlimited) {
      return { key, label: `${name} ∞`, tone: "info" as const };
    }
    // Absolute balance labels from DeepSeek-style probes: "USD 0.12"
    if (/^[A-Z]{3}\s+-?\d/.test(name)) {
      return { key, label: name, tone: "info" as const };
    }
    const total = entry.total || 0;
    const used = entry.used || 0;
    const remainingPct =
      typeof entry.remaining === "number" && entry.remaining > 0 && entry.remaining <= 100
        ? Math.round(entry.remaining)
        : total > 0
          ? Math.max(0, Math.round(((total - used) / total) * 100))
          : 100;
    const toneRaw = getColorTone(remainingPct);
    const tone = toneRaw === "danger" ? ("error" as const) : toneRaw;
    return { key, label: `${name} ${remainingPct}%`, tone };
  });
}

type Props = {
  provider: Provider;
  credentials: Credential[];
  aliases: ModelAlias[];
  secret: string;
  presets: NinerouterPreset[];
  onOpenImport?: (source: "cliproxy" | "9router") => void;
  onBack: () => void;
  onMutated: () => void;
  onNotice: (message: string) => void;
  onError: (message: string) => void;
};

/** Provider detail page — header + connections card + discovered models.
 *  Ported from 9router [id]/page.js, adapted to tdproxy's snapshot model. */
export function ProviderDetail({
  provider,
  credentials,
  aliases,
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
  const connectionProfile = useMemo(
    () => resolveConnectionProfile(catalog, presets.find((item) => item.id === provider.ID) ?? null),
    [catalog, presets, provider.ID],
  );
  const [credentialRefreshOverrides, setCredentialRefreshOverrides] = useState<Record<string, { status?: string; last_validated_at?: string }>>({});
  const displayedCredentials = useMemo(
    () => credentials.map((credential) => {
      const override = credentialRefreshOverrides[credential.id];
      return override ? { ...credential, ...override } : credential;
    }),
    [credentials, credentialRefreshOverrides],
  );
  const sortedCredentials = useMemo(
    () => [...displayedCredentials].sort(compareCredentialsByCreatedAt),
    [displayedCredentials],
  );
  const credentialAccountNumbers = useMemo(
    () => buildCredentialAccountNumbers(credentials),
    [credentials],
  );
  const [accountQuery, setAccountQuery] = useState("");
  const [errorCodeFilter, setErrorCodeFilter] = useState("");
  const errorCodeOptions = useMemo(() => {
    const counts = new Map<string, number>();
    for (const credential of sortedCredentials) {
      const code = credential.last_error_code?.trim();
      if (!code) continue;
      counts.set(code, (counts.get(code) || 0) + 1);
    }
    return [...counts.entries()]
      .sort((left, right) => right[1] - left[1] || left[0].localeCompare(right[0]))
      .map(([code, count]) => ({ code, count }));
  }, [sortedCredentials]);
  const erroredWithoutCodeCount = useMemo(
    () =>
      sortedCredentials.filter((credential) => {
        if (credential.last_error_code?.trim()) return false;
        return Boolean(
          credential.last_error?.trim() ||
            credential.status === "auth_required" ||
            credential.status === "cooldown" ||
            isOnCooldown(credential.cooldown_until),
        );
      }).length,
    [sortedCredentials],
  );
  const filteredCredentials = useMemo(() => {
    const query = accountQuery.trim().toLowerCase();
    return sortedCredentials.filter((credential) => {
      const code = credential.last_error_code?.trim() || "";
      const hasError =
        Boolean(code) ||
        Boolean(credential.last_error?.trim()) ||
        credential.status === "auth_required" ||
        credential.status === "cooldown" ||
        isOnCooldown(credential.cooldown_until);

      if (errorCodeFilter === "__any__") {
        if (!hasError) return false;
      } else if (errorCodeFilter === "__none__") {
        if (!hasError || code) return false;
      } else if (errorCodeFilter) {
        if (code !== errorCodeFilter) return false;
      }

      if (!query) return true;
      const haystack = [
        credential.id,
        credential.label,
        credential.email,
        credential.auth_type,
        credential.status,
        credential.last_error_code,
        credential.last_error,
      ]
        .filter(Boolean)
        .join(" ")
        .toLowerCase();
      return haystack.includes(query);
    });
  }, [sortedCredentials, accountQuery, errorCodeFilter]);
  useEffect(() => {
    if (!errorCodeFilter) return;
    if (errorCodeFilter === "__any__") return;
    if (errorCodeFilter === "__none__") {
      if (erroredWithoutCodeCount === 0) setErrorCodeFilter("");
      return;
    }
    if (!errorCodeOptions.some((option) => option.code === errorCodeFilter)) {
      setErrorCodeFilter("");
    }
  }, [errorCodeFilter, errorCodeOptions, erroredWithoutCodeCount]);
  const [showAddCredential, setShowAddCredential] = useState(false);
  const [credentialMethod, setCredentialMethod] = useState<ConnectionMethod | null>(null);
  const [showOAuth, setShowOAuth] = useState(false);
  const [oauthAutoStart, setOauthAutoStart] = useState(false);
  const [reAuthCredential, setReAuthCredential] = useState<Credential | null>(null);
  const [editingCredential, setEditingCredential] = useState<Credential | null>(null);
  const [confirmDeleteProvider, setConfirmDeleteProvider] = useState(false);
  const [confirmDeleteCred, setConfirmDeleteCred] = useState<Credential | null>(null);
  const [confirmBulkDelete, setConfirmBulkDelete] = useState(false);
  const [selectedCredentialIds, setSelectedCredentialIds] = useState<string[]>([]);
  const [bulkDeleteBusy, setBulkDeleteBusy] = useState(false);
  const [healthBusy, setHealthBusy] = useState(false);
  const [discoverNonce, setDiscoverNonce] = useState(0);
  const [modelsCredential, setModelsCredential] = useState<Credential | null>(null);
  const [showCursorImport, setShowCursorImport] = useState(false);
  const [showKiroOAuth, setShowKiroOAuth] = useState(false);
  const [proxyPools, setProxyPools] = useState<ProxyPoolOption[]>([]);
  const [proxyUsageById, setProxyUsageById] = useState<Record<string, CredentialProxyUsage>>({});
  const supportsUpstreamQuota = useMemo(
    () => providerSupportsUpstreamQuota(provider.ID, presets),
    [provider.ID, presets],
  );

  useEffect(() => {
    const available = new Set(credentials.map((credential) => credential.id));
    setCredentialRefreshOverrides((current) => {
      const next = Object.fromEntries(Object.entries(current).filter(([id]) => available.has(id)));
      return Object.keys(next).length === Object.keys(current).length ? current : next;
    });
    setSelectedCredentialIds((current) => {
      const next = current.filter((id) => available.has(id));
      return next.length === current.length ? current : next;
    });
  }, [credentials]);

  const filteredIdSet = useMemo(
    () => new Set(filteredCredentials.map((credential) => credential.id)),
    [filteredCredentials],
  );
  const selectedInView = useMemo(
    () => selectedCredentialIds.filter((id) => filteredIdSet.has(id)),
    [selectedCredentialIds, filteredIdSet],
  );
  const allFilteredSelected =
    filteredCredentials.length > 0 && selectedInView.length === filteredCredentials.length;
  const someFilteredSelected = selectedInView.length > 0 && !allFilteredSelected;

  const handleCredentialRefreshed = (credentialId: string, status?: string) => {
    setCredentialRefreshOverrides((current) => ({
      ...current,
      [credentialId]: { status: status || "healthy", last_validated_at: new Date().toISOString() },
    }));
  };

  const toggleCredentialSelected = (credentialId: string, selected: boolean) => {
    setSelectedCredentialIds((current) => {
      if (selected) {
        return current.includes(credentialId) ? current : [...current, credentialId];
      }
      return current.filter((id) => id !== credentialId);
    });
  };

  const toggleSelectAllFiltered = () => {
    setSelectedCredentialIds((current) => {
      if (allFilteredSelected) {
        return current.filter((id) => !filteredIdSet.has(id));
      }
      const merged = new Set(current);
      for (const credential of filteredCredentials) merged.add(credential.id);
      return [...merged];
    });
  };

  // Fetch proxy pools once for the EditConnectionModal binding dropdown.
  useEffect(() => {
    let cancelled = false;
    listProxyPools(secret)
      .then((result) => { if (!cancelled) setProxyPools(result.proxy_pools || []); })
      .catch(() => { /* non-fatal */ });
    return () => { cancelled = true; };
  }, [secret]);

  useEffect(() => {
    let cancelled = false;
    fetchCredentialProxyUsage(secret, "today")
      .then((result) => {
        if (!cancelled) setProxyUsageById(result.by_credential || {});
      })
      .catch(() => {
        if (!cancelled) setProxyUsageById({});
      });
    return () => {
      cancelled = true;
    };
  }, [secret, credentials]);

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
      case "import_cliproxy":
        onOpenImport?.("cliproxy");
        break;
      case "import_cursor":
        setShowCursorImport(true);
        break;
      case "connect_kiro":
        setShowKiroOAuth(true);
        break;
      case "import_9router":
        onOpenImport?.("9router");
        break;
      default: {
        const _exhaustive: never = method.kind;
        void _exhaustive;
      }
    }
  };

  const handleDeleteCredential = async () => {
    if (!confirmDeleteCred) return;
    const cred = confirmDeleteCred;
    setConfirmDeleteCred(null);
    try {
      await deleteCredential(secret, cred.id);
      setSelectedCredentialIds((current) => current.filter((id) => id !== cred.id));
      onNotice(t("providers.credentialDeleted", { id: cred.id }));
      onMutated();
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("providers.deleteFailed"));
    }
  };

  const handleBulkDeleteCredentials = async () => {
    const ids = [...selectedCredentialIds];
    if (ids.length === 0) return;
    setConfirmBulkDelete(false);
    setBulkDeleteBusy(true);
    let deleted = 0;
    const failures: string[] = [];
    try {
      for (const id of ids) {
        try {
          await deleteCredential(secret, id);
          deleted += 1;
        } catch (cause) {
          failures.push(cause instanceof Error ? cause.message : id);
        }
      }
      setSelectedCredentialIds([]);
      if (deleted > 0) onMutated();
      if (failures.length === 0) {
        onNotice(`Deleted ${deleted} account${deleted === 1 ? "" : "s"}`);
      } else {
        onError(`Deleted ${deleted}/${ids.length}. Failed: ${failures[0]}`);
      }
    } finally {
      setBulkDeleteBusy(false);
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
        <ProviderLogo
          className="provider-logo"
          providerId={provider.ID}
          providerType={provider.Type}
          style={{ color: catalog.color }}
          alt={`${provider.Name || provider.ID} logo`}
        />
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
            {catalog.website && (
              <a className="detail-link" href={catalog.website} target="_blank" rel="noreferrer">
                Website <span className="material-symbols-outlined" style={{ fontSize: 14 }}>open_in_new</span>
              </a>
            )}
            {catalog.apiKeyUrl && (
              <a className="detail-link" href={catalog.apiKeyUrl} target="_blank" rel="noreferrer">
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
      <Card
        pad="md"
        className="section"
        title={
          <span className="connection-card-head-title">
            Connections
            <ConnectionStatsInline credentials={credentials} />
          </span>
        }
        icon="vpn_key"
        action={
          <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap", justifyContent: "flex-end" }}>
            <ModelAvailabilityBadge credentials={credentials} />
            <ProviderConnectionActions
              profile={connectionProfile}
              onMethod={handleConnectionMethod}
              placement="footer"
              hideImports
            />
          </div>
        }
      >
        {credentials.length === 0 ? (
          <EmptyState
            icon="key_off"
            text="No connections yet."
            hint={connectionProfile.noAuth ? "Add a no-auth connection to enable routing." : "Choose a connection method above — OAuth, API key, or cookie."}
          />
        ) : (
          <>
            <div className="connections-toolbar">
              <Input
                icon="search"
                value={accountQuery}
                onChange={(event) => setAccountQuery(event.target.value)}
                placeholder="Search accounts by email, label, or id…"
                aria-label="Search accounts"
              />
              <Select
                className="connections-error-filter"
                value={errorCodeFilter}
                onChange={(event) => setErrorCodeFilter(event.target.value)}
                aria-label="Filter by error code"
              >
                <option value="">All error codes</option>
                <option value="__any__">Any error</option>
                {erroredWithoutCodeCount > 0 ? (
                  <option value="__none__">Error without code ({erroredWithoutCodeCount})</option>
                ) : null}
                {errorCodeOptions.map(({ code, count }) => (
                  <option key={code} value={code}>
                    {code} ({count})
                  </option>
                ))}
              </Select>
              {accountQuery.trim() || errorCodeFilter ? (
                <span className="connections-toolbar-meta">
                  {filteredCredentials.length}/{credentials.length}
                </span>
              ) : null}
            </div>
            {filteredCredentials.length === 0 ? (
              <EmptyState
                icon="search_off"
                text="No accounts match your filters."
                hint="Try email, label, credential id, status, or error code."
              />
            ) : (
              <>
                <div className="connections-selection-bar">
                  <label className="connections-select-all">
                    <input
                      type="checkbox"
                      checked={allFilteredSelected}
                      ref={(node) => {
                        if (node) node.indeterminate = someFilteredSelected;
                      }}
                      onChange={toggleSelectAllFiltered}
                      aria-label="Select all visible accounts"
                    />
                    <span>
                      {allFilteredSelected
                        ? "Deselect all"
                        : someFilteredSelected
                          ? `${selectedInView.length} selected`
                          : "Select all"}
                    </span>
                  </label>
                  {selectedCredentialIds.length > 0 ? (
                    <div className="connections-selection-actions">
                      <span className="connections-toolbar-meta">
                        {selectedCredentialIds.length} selected
                      </span>
                      <Button
                        variant="danger"
                        size="sm"
                        icon="delete"
                        loading={bulkDeleteBusy}
                        onClick={() => setConfirmBulkDelete(true)}
                      >
                        Delete selected
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => setSelectedCredentialIds([])}
                        disabled={bulkDeleteBusy}
                      >
                        Clear
                      </Button>
                    </div>
                  ) : null}
                </div>
                {filteredCredentials.map((cred) => (
                <ConnectionRow
                  key={cred.id}
                  providerId={provider.ID}
                  credential={cred}
                  accountNumber={credentialAccountNumbers.get(cred.id)}
                  secret={secret}
                  selected={selectedCredentialIds.includes(cred.id)}
                  onSelectedChange={toggleCredentialSelected}
                  supportsUpstreamQuota={supportsUpstreamQuota}
                  proxyUsage={proxyUsageById[cred.id]}
                  supportsOAuth={connectionProfile.methods.some((method) => method.kind === "oauth" && method.available)}
                  onEdit={(c) => setEditingCredential(c)}
                  onDeleted={(c) => setConfirmDeleteCred(c)}
                  onReAuth={(c) => setReAuthCredential(c)}
                  onShowModels={(c) => setModelsCredential(c)}
                  onMutated={onMutated}
                  onCredentialRefreshed={handleCredentialRefreshed}
                  onNotice={onNotice}
                  onError={onError}
                />
              ))}
              </>
            )}
          </>
        )}
      </Card>

      <ProviderRotationCard
        providerId={provider.ID}
        providerName={provider.Name || provider.ID}
        accountCount={credentials.length}
        secret={secret}
        onSaved={onNotice}
        onError={onError}
      />

      {/* Models routing + aliases */}
      <ModelsSection
        providerId={provider.ID}
        credentials={credentials}
        secret={secret}
        discoverNonce={discoverNonce}
      />

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
          onNotice("Credential saved");
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
          onNotice("OAuth credential connected");
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
          onNotice("Cursor token imported");
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
          onNotice("Kiro credential connected");
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
      <ConfirmDialog
        open={confirmBulkDelete}
        title={`Delete ${selectedCredentialIds.length} account${selectedCredentialIds.length === 1 ? "" : "s"}?`}
        message="This permanently removes the selected credentials and their encrypted secrets."
        confirmText={`Delete ${selectedCredentialIds.length}`}
        variant="danger"
        onConfirm={() => void handleBulkDeleteCredentials()}
        onClose={() => setConfirmBulkDelete(false)}
      />
    </div>
  );
}

/** A single credential row inside the connections card. */
function ConnectionRow({
  providerId,
  credential,
  accountNumber,
  secret,
  selected,
  onSelectedChange,
  supportsUpstreamQuota,
  proxyUsage,
  supportsOAuth,
  onDeleted,
  onMutated,
  onCredentialRefreshed,
  onEdit,
  onReAuth,
  onShowModels,
  onNotice,
  onError,
}: {
  providerId: string;
  credential: Credential;
  accountNumber?: number;
  secret: string;
  selected: boolean;
  onSelectedChange: (credentialId: string, selected: boolean) => void;
  supportsUpstreamQuota: boolean;
  proxyUsage?: CredentialProxyUsage;
  supportsOAuth: boolean;
  onEdit: (credential: Credential) => void;
  onDeleted: (credential: Credential) => void;
  onReAuth: (credential: Credential) => void;
  onShowModels: (credential: Credential) => void;
  onMutated: () => void;
  onCredentialRefreshed?: (credentialId: string, status?: string) => void;
  onNotice: (message: string) => void;
  onError: (message: string) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [quotaBusy, setQuotaBusy] = useState(false);
  const [servicePlan, setServicePlan] = useState("");
  const [quotaBadges, setQuotaBadges] = useState<Array<{ key: string; label: string; tone: "success" | "warning" | "error" | "info" | "default" }>>([]);
  const [quotaMessage, setQuotaMessage] = useState("");
  const status = credentialStatusLabel(credential);
  const authIcon = credential.auth_type === "oauth" ? "lock_person" : credential.auth_type === "none" ? "lock_open" : "key";
  const hasProxy = (credential.proxy_pool_ids?.length ?? 0) > 0;
  const onCooldown = credential.cooldown_until && isOnCooldown(credential.cooldown_until);
  const needsReAuth = credential.status === "auth_required" && credential.auth_type === "oauth";
  const proxyUsageLabel = formatProxyUsageLabel(proxyUsage);
  const connectionTitle = credential.email || credential.label || credential.id;

  const loadQuota = async (silent = false) => {
    if (!supportsUpstreamQuota) return;
    setQuotaBusy(true);
    try {
      const quota = await fetchCredentialQuota(secret, credential.id);
      const badges = quotaBadgesFromQuota(quota);
      setQuotaBadges(badges);
      setServicePlan(formatServicePlanLabel(quota.plan));
      setQuotaMessage(quota.message || "");
      if (!silent && quota.message && badges.length === 0) {
        onNotice(`${credential.id}: ${quota.message}`);
      }
    } catch (cause) {
      setQuotaBadges([]);
      setServicePlan("");
      setQuotaMessage(cause instanceof Error ? cause.message : "Quota check failed");
      if (!silent) {
        onError(cause instanceof Error ? cause.message : "Quota check failed");
      }
    } finally {
      setQuotaBusy(false);
    }
  };

  useEffect(() => {
    if (!supportsUpstreamQuota) return;
    void loadQuota(true);
    // Intentionally refresh when credential identity changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [supportsUpstreamQuota, credential.id, secret]);

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
          priority: credential.priority ?? 0,
          weight: credential.weight ?? 1,
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
      const result = await refreshCredential(secret, credential.id);
      if (onCredentialRefreshed) {
        onCredentialRefreshed(credential.id, result.status?.status);
      } else {
        onMutated();
      }
      onNotice(`Token refreshed for ${credential.id}`);
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
    <div className={`connection-row${credential.enabled ? " is-enabled" : " is-disabled"}${selected ? " is-selected" : ""}${credential.last_validated_at ? " has-updated-at" : ""}`}>
      {credential.last_validated_at ? (
        <span
          className="connection-updated-at"
          title={`Last updated: ${new Date(credential.last_validated_at).toLocaleString()}`}
        >
          Updated {formatRelativeTime(credential.last_validated_at)}
        </span>
      ) : null}
      <label className="connection-select">
        <input
          type="checkbox"
          checked={selected}
          onChange={(event) => onSelectedChange(credential.id, event.target.checked)}
          aria-label={`Select ${credential.email || credential.label || credential.id}`}
        />
      </label>
      <span
        className={`connection-plan-badge${servicePlan ? "" : " connection-plan-badge--fallback"}`}
        title={servicePlan ? `Service plan: ${servicePlan}` : credential.auth_type}
      >
        {supportsUpstreamQuota && quotaBusy && !servicePlan ? (
          <span className="connection-plan-badge-loading" aria-hidden="true">…</span>
        ) : servicePlan ? (
          servicePlan
        ) : (
          <span className="material-symbols-outlined">{authIcon}</span>
        )}
      </span>
      <div className="connection-main">
        <div className="connection-label">
          {accountNumber ? <span className="connection-label-index">#{accountNumber}</span> : null}
          <span className="connection-label-text">{connectionTitle}</span>
        </div>
        {(credential.email || credential.label || credential.created_at) && (
          <div className="connection-sub" title={`Credential ID: ${credential.id}`}>
            Added {formatCredentialAddedAt(credential.created_at)}
          </div>
        )}
        {credential.last_error && <div className="connection-error">{credential.last_error}</div>}
        <div className="connection-badges">
          <Badge variant={status.tone} size="sm" dot>{status.label}</Badge>
          {credential.last_error_code ? (
            <Badge variant="error" size="sm" title={credential.last_error || credential.last_error_code}>
              {credential.last_error_code}
            </Badge>
          ) : null}
          {quotaBadges.map((badge) => (
            <Badge key={badge.key} variant={badge.tone} size="sm" icon="donut_large" title={quotaMessage || badge.label}>
              {badge.label}
            </Badge>
          ))}
          {supportsUpstreamQuota && !quotaBusy && quotaBadges.length === 0 && quotaMessage ? (
            <Badge variant="warning" size="sm" title={quotaMessage}>
              quota unavailable
            </Badge>
          ) : null}
          {proxyUsageLabel ? (
            <Badge variant="default" size="sm" icon="monitoring" title="Usage through tproxy today">
              {proxyUsageLabel}
            </Badge>
          ) : null}
          <Badge variant="default" size="sm">{credential.auth_type}</Badge>
          {(credential.priority ?? 0) > 0 && (
            <Badge variant="default" size="sm">prio {credential.priority}</Badge>
          )}
          {(credential.consecutive_use_count ?? 0) > 0 && (
            <Badge variant="info" size="sm">sticky {credential.consecutive_use_count}</Badge>
          )}
          {credential.last_used_at && (
            <Badge variant="default" size="sm" title={new Date(credential.last_used_at).toLocaleString()}>
              used {formatRelativeTime(credential.last_used_at)}
            </Badge>
          )}
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
        {supportsUpstreamQuota ? (
          <Button
            variant="ghost"
            size="sm"
            icon="donut_large"
            onClick={() => void loadQuota(false)}
            loading={quotaBusy}
            aria-label="Refresh quota"
            title="Refresh upstream quota / balance"
          />
        ) : null}
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

function formatRelativeTime(value: string) {
  const target = Date.parse(value);
  if (Number.isNaN(target)) return "recently";
  const deltaMs = Date.now() - target;
  const minutes = Math.round(deltaMs / 60000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 48) return `${hours}h ago`;
  const days = Math.round(hours / 24);
  return `${days}d ago`;
}
