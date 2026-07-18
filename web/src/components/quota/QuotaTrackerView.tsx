import { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { deleteCredential, saveCredential } from "../providers/api";
import { getProviderTypeInfo } from "../providers/catalog";
import { ProviderLogo } from "../providers/ProviderLogo";
import { useUsageStream } from "../usage/useUsageStream";
import { ConfirmDialog, Toggle, cn } from "../ui";
import { CodexResetCreditsModal } from "./CodexResetCreditsModal";
import { consumeCodexResetCredit, fetchCredentialProxyUsage, fetchCredentialQuota, type CredentialProxyUsage, type CredentialQuota } from "./api";
import { QuotaTable } from "./QuotaTable";
import {
  ACCOUNT_FILTER_OPTIONS,
  AUTO_REFRESH_STORAGE_KEY,
  QUOTA_VISIBILITY_KEY,
  REFRESH_INTERVAL_MS,
  type AccountFilter,
  type QuotaVisibility,
  earliestResetAt,
  filterQuotasByVisibility,
  getConnectionLabel,
  getHiddenQuotaRows,
  getQuotaVisibilityKey,
  isConnectionDepleted,
  quotaEntries,
} from "./utils";

const QUOTA_PROVIDER_TYPES = new Set([
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

function quotaProviderKey(item: Pick<CredentialRow, "providerId" | "providerType">): string {
  if (QUOTA_PROVIDER_TYPES.has(item.providerId)) return item.providerId;
  return item.providerType;
}

function supportsQuotaTracker(item: Pick<CredentialRow, "providerId" | "providerType">): boolean {
  return QUOTA_PROVIDER_TYPES.has(item.providerId) || QUOTA_PROVIDER_TYPES.has(item.providerType);
}

type CredentialRow = {
  id: string;
  providerId: string;
  providerType: string;
  label?: string;
  email?: string;
  enabled: boolean;
  auth_type: string;
};

type Props = {
  secret: string;
  credentials: CredentialRow[];
  onError: (message: string) => void;
  onMutated?: () => void;
};

function loadVisibility(): QuotaVisibility {
  if (typeof window === "undefined") return {};
  try {
    return JSON.parse(window.localStorage.getItem(QUOTA_VISIBILITY_KEY) || "{}") as QuotaVisibility;
  } catch {
    return {};
  }
}

function getCodexResetCreditCount(quota?: CredentialQuota) {
  const value = quota?.reset_credits?.available_count;
  return typeof value === "number" && Number.isFinite(value) ? Math.max(0, value) : 0;
}

export function QuotaTrackerView({ secret, credentials, onError, onMutated }: Props) {
  const navigate = useNavigate();
  const [quotaById, setQuotaById] = useState<Record<string, CredentialQuota>>({});
  const [loading, setLoading] = useState<Record<string, boolean>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [refreshingAll, setRefreshingAll] = useState(false);
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [countdown, setCountdown] = useState(60);
  const [providerFilter, setProviderFilter] = useState("all");
  const [providerMenuOpen, setProviderMenuOpen] = useState(false);
  const [accountFilter, setAccountFilter] = useState<AccountFilter>("all");
  const [expiringFirst, setExpiringFirst] = useState(false);
  const [bulkToggling, setBulkToggling] = useState(false);
  const [togglingId, setTogglingId] = useState<string | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [quotaVisibility, setQuotaVisibility] = useState<QuotaVisibility>(() => loadVisibility());
  const [resetConfirmCredential, setResetConfirmCredential] = useState<CredentialRow | null>(null);
  const [resetCreditsCredential, setResetCreditsCredential] = useState<CredentialRow | null>(null);
  const [resettingLimitId, setResettingLimitId] = useState<string | null>(null);
  const [activeCredentialIds, setActiveCredentialIds] = useState<Set<string>>(() => new Set());
  const [proxyUsageById, setProxyUsageById] = useState<Record<string, CredentialProxyUsage>>({});

  const applyLiveUsage = useCallback((update: { activeRequests?: Array<{ credential_id?: string }> }) => {
    const next = new Set<string>();
    for (const item of update.activeRequests || []) {
      if (item.credential_id) next.add(item.credential_id);
    }
    setActiveCredentialIds(next);
  }, []);

  useUsageStream(secret, true, applyLiveUsage);

  const eligible = useMemo(
    () => credentials.filter((item) => supportsQuotaTracker(item)),
    [credentials],
  );

  const providerOptions = useMemo(() => {
    const types = new Set(eligible.map((item) => quotaProviderKey(item)));
    return [...types].sort();
  }, [eligible]);

  const filteredCredentials = useMemo(() => {
    let rows = eligible;
    if (providerFilter !== "all") {
      rows = rows.filter((item) => quotaProviderKey(item) === providerFilter);
    }
    if (accountFilter === "active") {
      rows = rows.filter((item) => item.enabled);
    } else if (accountFilter === "inactive") {
      rows = rows.filter((item) => !item.enabled);
    }
    return rows;
  }, [eligible, providerFilter, accountFilter]);

  const sortedCredentials = useMemo(() => {
    if (!expiringFirst) return filteredCredentials;
    return [...filteredCredentials].sort((a, b) => {
      const diff = earliestResetAt(quotaById[a.id]) - earliestResetAt(quotaById[b.id]);
      if (diff !== 0) return diff;
      return (getConnectionLabel(a) || a.id).localeCompare(getConnectionLabel(b) || b.id);
    });
  }, [filteredCredentials, expiringFirst, quotaById]);

  const loadQuota = useCallback(
    async (credentialId: string) => {
      setLoading((current) => ({ ...current, [credentialId]: true }));
      setErrors((current) => ({ ...current, [credentialId]: "" }));
      try {
        const quota = await fetchCredentialQuota(secret, credentialId);
        setQuotaById((current) => ({ ...current, [credentialId]: quota }));
      } catch (cause) {
        const message = cause instanceof Error ? cause.message : "Failed to fetch quota";
        setErrors((current) => ({ ...current, [credentialId]: message }));
      } finally {
        setLoading((current) => ({ ...current, [credentialId]: false }));
      }
    },
    [secret],
  );

  const loadProxyUsage = useCallback(async () => {
    try {
      const response = await fetchCredentialProxyUsage(secret, "all");
      setProxyUsageById(response.by_credential || {});
    } catch {
      // Keep the last known usage if the stats endpoint is temporarily unavailable.
    }
  }, [secret]);

  const refreshAll = useCallback(async () => {
    setRefreshingAll(true);
    setCountdown(60);
    try {
      await Promise.all([
        loadProxyUsage(),
        ...sortedCredentials.map((item) => loadQuota(item.id)),
      ]);
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "Failed to refresh quota");
    } finally {
      setRefreshingAll(false);
    }
  }, [sortedCredentials, loadQuota, loadProxyUsage, onError]);

  useEffect(() => {
    void refreshAll();
  }, [refreshAll]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const stored = window.localStorage.getItem(AUTO_REFRESH_STORAGE_KEY);
    if (stored !== null) setAutoRefresh(stored === "true");
  }, []);

  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem(AUTO_REFRESH_STORAGE_KEY, String(autoRefresh));
  }, [autoRefresh]);

  useEffect(() => {
    if (typeof window !== "undefined") {
      window.localStorage.setItem(QUOTA_VISIBILITY_KEY, JSON.stringify(quotaVisibility));
    }
  }, [quotaVisibility]);

  useEffect(() => {
    if (!autoRefresh) return;
    const refreshTimer = window.setInterval(() => {
      void refreshAll();
    }, REFRESH_INTERVAL_MS);
    const countdownTimer = window.setInterval(() => {
      setCountdown((value) => (value <= 1 ? 60 : value - 1));
    }, 1000);
    return () => {
      window.clearInterval(refreshTimer);
      window.clearInterval(countdownTimer);
    };
  }, [autoRefresh, refreshAll]);

  const setCredentialEnabled = async (credential: CredentialRow, enabled: boolean) => {
    setTogglingId(credential.id);
    try {
      await saveCredential(secret, {
        provider_id: credential.providerId,
        credential: {
          id: credential.id,
          auth_type: credential.auth_type as "api_key" | "oauth" | "service_account" | "none",
          enabled,
          label: credential.label,
          email: credential.email,
        },
      });
      onMutated?.();
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "Failed to update account");
    } finally {
      setTogglingId(null);
    }
  };

  const handleDelete = async (credential: CredentialRow) => {
    if (!window.confirm("Delete this connection?")) return;
    setDeletingId(credential.id);
    try {
      await deleteCredential(secret, credential.id);
      setQuotaById((current) => {
        const next = { ...current };
        delete next[credential.id];
        return next;
      });
      onMutated?.();
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "Delete failed");
    } finally {
      setDeletingId(null);
    }
  };

  const bulkSetEnabled = async (targets: CredentialRow[], enabled: boolean) => {
    if (!targets.length || bulkToggling) return;
    setBulkToggling(true);
    try {
      await Promise.all(targets.map((credential) => setCredentialEnabled(credential, enabled)));
    } finally {
      setBulkToggling(false);
    }
  };

  const handleDisableDepleted = () => {
    const targets = sortedCredentials.filter(
      (credential) => credential.enabled && isConnectionDepleted(quotaById[credential.id]),
    );
    void bulkSetEnabled(targets, false);
  };

  const handleEnableAvailable = () => {
    const targets = sortedCredentials.filter(
      (credential) => !credential.enabled && !isConnectionDepleted(quotaById[credential.id]),
    );
    void bulkSetEnabled(targets, true);
  };

  const handleHideQuota = (providerType: string, key: string) => {
    setQuotaVisibility((current) => {
      const hidden = new Set(current[providerType]?.hidden || []);
      hidden.add(key);
      return { ...current, [providerType]: { hidden: [...hidden] } };
    });
  };

  const handleShowQuota = (providerType: string, key: string) => {
    setQuotaVisibility((current) => {
      const hidden = new Set(current[providerType]?.hidden || []);
      hidden.delete(key);
      return { ...current, [providerType]: { hidden: [...hidden] } };
    });
  };

  const handleConsumeCodexReset = async (credential: CredentialRow) => {
    setResetConfirmCredential(null);
    setResettingLimitId(credential.id);
    try {
      const result = await consumeCodexResetCredit(secret, credential.id);
      if (!result.ok) {
        onError(result.message || "No Codex reset credits available.");
        return;
      }
      await loadQuota(credential.id);
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : "Failed to reset Codex limit");
    } finally {
      setResettingLimitId(null);
    }
  };

  const selectedProviderLabel =
    providerFilter === "all" ? "All providers" : getProviderTypeInfo(providerFilter).name;

  if (eligible.length === 0) {
    return (
      <section className="quota-tracker-page">
        <div className="quota-tracker-empty">
          <span className="material-symbols-outlined">cloud_off</span>
          <h3>No Providers Connected</h3>
          <p>Connect Codex, Claude, Copilot, or Antigravity accounts to track upstream quota limits.</p>
        </div>
      </section>
    );
  }

  return (
    <section className="quota-tracker-page">
      <div className="quota-tracker-controls">
        <div className="quota-tracker-filter-group">
          <div className="quota-tracker-dropdown">
            <button
              type="button"
              className="quota-tracker-chip"
              onClick={() => setProviderMenuOpen((open) => !open)}
              aria-expanded={providerMenuOpen}
            >
              <span className="material-symbols-outlined">apps</span>
              <span className="quota-tracker-chip-label">{selectedProviderLabel}</span>
              <span className="material-symbols-outlined">expand_more</span>
            </button>
            {providerMenuOpen ? (
              <>
                <button type="button" className="quota-tracker-backdrop" aria-label="Close provider filter" onClick={() => setProviderMenuOpen(false)} />
                <div className="quota-tracker-menu">
                  <button
                    type="button"
                    className={cn("quota-tracker-menu-item", providerFilter === "all" && "is-active")}
                    onClick={() => {
                      setProviderFilter("all");
                      setProviderMenuOpen(false);
                    }}
                  >
                    <span className="material-symbols-outlined">apps</span>
                    <span>All providers</span>
                  </button>
                  {providerOptions.map((providerType) => {
                    const info = getProviderTypeInfo(providerType);
                    return (
                      <button
                        key={providerType}
                        type="button"
                        className={cn("quota-tracker-menu-item", providerFilter === providerType && "is-active")}
                        onClick={() => {
                          setProviderFilter(providerType);
                          setProviderMenuOpen(false);
                        }}
                      >
                        <ProviderLogo
                          className="quota-tracker-provider-icon"
                          providerType={providerType}
                          style={{ color: info.color }}
                        />
                        <span>{info.name}</span>
                      </button>
                    );
                  })}
                </div>
              </>
            ) : null}
          </div>

          <select
            className="quota-tracker-select"
            value={accountFilter}
            onChange={(event) => setAccountFilter(event.target.value as AccountFilter)}
            aria-label="Filter accounts by status"
          >
            {ACCOUNT_FILTER_OPTIONS.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>

          <button
            type="button"
            className={cn("quota-tracker-chip", expiringFirst && "quota-tracker-chip-amber")}
            onClick={() => setExpiringFirst((value) => !value)}
            aria-pressed={expiringFirst}
          >
            <span className="material-symbols-outlined">hourglass_top</span>
            <span>Expiring first</span>
          </button>

          <button
            type="button"
            className="quota-tracker-chip quota-tracker-chip-danger"
            disabled={bulkToggling}
            onClick={handleDisableDepleted}
          >
            <span className="material-symbols-outlined">block</span>
            <span>Turn off Empty</span>
          </button>

          <button
            type="button"
            className="quota-tracker-chip quota-tracker-chip-success"
            disabled={bulkToggling}
            onClick={handleEnableAvailable}
          >
            <span className="material-symbols-outlined">check_circle</span>
            <span>Turn on Available</span>
          </button>

          <button
            type="button"
            className="quota-tracker-chip"
            onClick={() => setAutoRefresh((value) => !value)}
          >
            <span className={cn("material-symbols-outlined", autoRefresh && "quota-tracker-icon-active")}>
              {autoRefresh ? "toggle_on" : "toggle_off"}
            </span>
            <span>Auto-refresh</span>
            {autoRefresh ? <span className="quota-tracker-countdown">({countdown}s)</span> : null}
          </button>

          <button
            type="button"
            className="quota-tracker-chip"
            disabled={refreshingAll}
            onClick={() => void refreshAll()}
            aria-label="Refresh all"
          >
            <span className={cn("material-symbols-outlined", refreshingAll && "animate-spin")}>refresh</span>
          </button>
        </div>
      </div>

      {expiringFirst ? (
        <div className="quota-tracker-banner">
          Expiring-first reorders accounts on this page by earliest quota reset time.
        </div>
      ) : null}

      {sortedCredentials.length === 0 ? (
        <div className="quota-tracker-empty">
          <span className="material-symbols-outlined">filter_alt_off</span>
          <h3>No accounts match this filter</h3>
          <p>Try changing the provider or account status filter.</p>
        </div>
      ) : (
        <div className="quota-tracker-grid">
          {sortedCredentials.map((credential) => {
            const quotaKey = quotaProviderKey(credential);
            const info = getProviderTypeInfo(quotaKey);
            const quota = quotaById[credential.id];
            const busy = loading[credential.id];
            const error = errors[credential.id];
            const rowBusy = deletingId === credential.id || togglingId === credential.id || resettingLimitId === credential.id;
            const allEntries = quotaEntries(quota);
            const visibleEntries = filterQuotasByVisibility(quotaKey, allEntries, quotaVisibility);
            const hiddenEntries = getHiddenQuotaRows(quotaKey, allEntries, quotaVisibility);
            const secondary = credential.email && credential.label && credential.email !== credential.label ? credential.email : null;
            const isCodex = quotaKey === "codex" || credential.providerType === "codex";
            const resetCreditCount = getCodexResetCreditCount(quota);
            const isResettingLimit = resettingLimitId === credential.id;

            return (
              <article
                key={credential.id}
                className={cn("quota-tracker-card", !credential.enabled && "quota-tracker-card-inactive")}
              >
                <div className="quota-tracker-card-head">
                  <div className="quota-tracker-card-head-row">
                    <div className="quota-tracker-card-ident">
                      <ProviderLogo
                        className="quota-tracker-provider-icon quota-tracker-provider-icon-lg"
                        providerType={quotaKey}
                        style={{ color: info.color }}
                      />
                      <div className="quota-tracker-card-titles">
                        <h3>{info.name}</h3>
                        {getConnectionLabel(credential) ? <p>{getConnectionLabel(credential)}</p> : null}
                        {secondary ? <p className="quota-tracker-card-email">{secondary}</p> : null}
                      </div>
                    </div>
                    <Toggle
                      checked={credential.enabled}
                      disabled={rowBusy}
                      onChange={(event) => void setCredentialEnabled(credential, event.target.checked)}
                      aria-label={credential.enabled ? "Disable connection" : "Enable connection"}
                    />
                  </div>

                  <div className="quota-tracker-card-actions">
                    {isCodex ? (
                      <>
                        <button
                          type="button"
                          className={cn(
                            "quota-tracker-reset-btn",
                            resetCreditCount > 0 && "quota-tracker-reset-btn-active",
                          )}
                          disabled={resetCreditCount <= 0 || busy || rowBusy}
                          onClick={() => setResetConfirmCredential(credential)}
                          aria-label={
                            resetCreditCount > 0
                              ? `Use one Codex reset credit. ${resetCreditCount} available.`
                              : "No Codex reset credits available"
                          }
                          title={
                            resetCreditCount > 0
                              ? `Use one Codex reset credit (${resetCreditCount} available)`
                              : "No Codex reset credits available"
                          }
                        >
                          <span className={cn("material-symbols-outlined", isResettingLimit && "animate-spin")}>
                            {isResettingLimit ? "progress_activity" : "restart_alt"}
                          </span>
                          <span>{resetCreditCount}</span>
                        </button>
                        <button
                          type="button"
                          className="quota-tracker-icon-btn"
                          disabled={busy || rowBusy}
                          onClick={() => setResetCreditsCredential(credential)}
                          aria-label="View Codex reset credit expiry"
                          title="View Codex reset credit expiry"
                        >
                          <span className="material-symbols-outlined">schedule</span>
                        </button>
                      </>
                    ) : null}
                    <button
                      type="button"
                      className="quota-tracker-icon-btn"
                      disabled={busy || rowBusy}
                      onClick={() => void loadQuota(credential.id)}
                      aria-label="Refresh quota"
                      title="Refresh quota"
                    >
                      <span className={cn("material-symbols-outlined", busy && "animate-spin")}>refresh</span>
                    </button>
                    <button
                      type="button"
                      className="quota-tracker-icon-btn"
                      disabled={rowBusy}
                      onClick={() => navigate(`/providers/${encodeURIComponent(credential.providerId)}`)}
                      aria-label="Edit connection"
                      title="Edit connection"
                    >
                      <span className="material-symbols-outlined">edit</span>
                    </button>
                    <button
                      type="button"
                      className="quota-tracker-icon-btn quota-tracker-icon-btn-danger"
                      disabled={rowBusy}
                      onClick={() => void handleDelete(credential)}
                      aria-label="Delete connection"
                      title="Delete connection"
                    >
                      <span className={cn("material-symbols-outlined", deletingId === credential.id && "animate-pulse")}>delete</span>
                    </button>
                  </div>
                </div>

                <div className="quota-tracker-card-body">
                  {busy && !quota ? (
                    <div className="quota-tracker-card-loading">
                      <span className="material-symbols-outlined animate-spin">progress_activity</span>
                    </div>
                  ) : error ? (
                    <div className="quota-tracker-card-error">
                      <span className="material-symbols-outlined">error</span>
                      <p>{error}</p>
                    </div>
                  ) : quota?.message && allEntries.length === 0 ? (
                    <div className="quota-tracker-card-message">{quota.message}</div>
                  ) : (
                    <QuotaTable
                      rows={visibleEntries}
                      credentialActive={activeCredentialIds.has(credential.id)}
                      proxyUsage={
                        proxyUsageById[credential.id] ?? { requests: 0, promptTokens: 0, completionTokens: 0 }
                      }
                      onHide={(row) => handleHideQuota(quotaKey, getQuotaVisibilityKey(row))}
                    />
                  )}

                  {hiddenEntries.length > 0 ? (
                    <div className="quota-tracker-hidden">
                      <span className="material-symbols-outlined">visibility_off</span>
                      <span>Hidden:</span>
                      {hiddenEntries.map((row) => (
                        <button
                          key={row.key}
                          type="button"
                          className="quota-tracker-hidden-chip"
                          onClick={() => handleShowQuota(quotaKey, getQuotaVisibilityKey(row))}
                        >
                          {row.name}
                        </button>
                      ))}
                    </div>
                  ) : null}
                </div>
              </article>
            );
          })}
        </div>
      )}

      <ConfirmDialog
        open={Boolean(resetConfirmCredential)}
        title="Reset Codex limit?"
        message={
          resetConfirmCredential
            ? `Use 1 Codex reset credit for ${getConnectionLabel(resetConfirmCredential) || resetConfirmCredential.email || "this account"}. This cannot be undone. Remaining credits: ${getCodexResetCreditCount(quotaById[resetConfirmCredential.id])}.`
            : ""
        }
        confirmText="Reset limit"
        cancelText="Cancel"
        variant="danger"
        onClose={() => {
          if (!resettingLimitId) setResetConfirmCredential(null);
        }}
        onConfirm={() => {
          if (resetConfirmCredential) void handleConsumeCodexReset(resetConfirmCredential);
        }}
      />

      <CodexResetCreditsModal
        open={Boolean(resetCreditsCredential)}
        secret={secret}
        credential={resetCreditsCredential}
        onClose={() => setResetCreditsCredential(null)}
      />
    </section>
  );
}
