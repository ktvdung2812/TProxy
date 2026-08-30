import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useSearchParams } from "react-router-dom";
import { deleteCredential, reorderProviderCredentials, saveCredential } from "../providers/api";
import { getProviderTypeInfo } from "../providers/catalog";
import { ProviderLogo } from "../providers/ProviderLogo";
import { useUsageStream } from "../usage/useUsageStream";
import { ConfirmDialog, Toggle, cn } from "../ui";
import { CodexResetCreditsModal } from "./CodexResetCreditsModal";
import { QuotaAccountDetailModal } from "./QuotaAccountDetailModal";
import { consumeCodexResetCredit, fetchCredentialProxyUsage, fetchCredentialQuota, type CredentialProxyUsage, type CredentialQuota } from "./api";
import { QuotaRingGrid } from "./QuotaRingGrid";
import { QuotaStackedBar } from "./QuotaStackedBar";
import { QuotaTable } from "./QuotaTable";
import { moveCredentialBefore } from "../providers/types";
import {
  ACCOUNT_FILTER_OPTIONS,
  AUTO_REFRESH_STORAGE_KEY,
  QUOTA_VISIBILITY_KEY,
  REFRESH_INTERVAL_MS,
  type AccountFilter,
  type QuotaVisibility,
  buildProviderCountMap,
  earliestResetAt,
  filterQuotasByVisibility,
  getConnectionLabel,
  getHiddenQuotaRows,
  getQuotaVisibilityKey,
  isConnectionAtZero,
  isConnectionDepleted,
  isQuotaExpiringSort,
  parseQuotaAccountFilter,
  patchQuotaSearchParams,
  quotaEntries,
  resolveQuotaProviderFilter,
  runWithConcurrency,
  usesQuotaRingLayout,
  usesQuotaStackedLayout,
  type QuotaUrlSort,
} from "./utils";

const QUOTA_PROVIDER_TYPES = new Set([
  "codex",
  "claude",
  "copilot",
  "cursor",
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
  "deepseek",
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
  priority?: number;
  weight?: number;
  proxy_pool_ids?: string[];
  created_at?: string;
};

type Props = {
  secret: string;
  credentials: CredentialRow[];
  onError: (message: string) => void;
  onNotice?: (message: string) => void;
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

function formatRenewalDate(value?: string): string | null {
  if (!value) return null;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return null;
  return date.toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

export function QuotaTrackerView({ secret, credentials, onError, onNotice, onMutated }: Props) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const [quotaById, setQuotaById] = useState<Record<string, CredentialQuota>>({});
  const [loading, setLoading] = useState<Record<string, boolean>>({});
  const [settledQuotaCredentialIds, setSettledQuotaCredentialIds] = useState<Set<string>>(() => new Set());
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [refreshingAll, setRefreshingAll] = useState(false);
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [countdown, setCountdown] = useState(60);
  const [providerMenuOpen, setProviderMenuOpen] = useState(false);
  const [bulkToggling, setBulkToggling] = useState(false);
  const [togglingId, setTogglingId] = useState<string | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [quotaVisibility, setQuotaVisibility] = useState<QuotaVisibility>(() => loadVisibility());
  const [resetConfirmCredential, setResetConfirmCredential] = useState<CredentialRow | null>(null);
  const [resetCreditsCredential, setResetCreditsCredential] = useState<CredentialRow | null>(null);
  const [detailCredential, setDetailCredential] = useState<CredentialRow | null>(null);
  const [resettingLimitId, setResettingLimitId] = useState<string | null>(null);
  const [activeCredentialIds, setActiveCredentialIds] = useState<Set<string>>(() => new Set());
  const [proxyUsageById, setProxyUsageById] = useState<Record<string, CredentialProxyUsage>>({});
  const [credentialOrderByProvider, setCredentialOrderByProvider] = useState<Record<string, string[]>>({});
  const [draggingCredentialId, setDraggingCredentialId] = useState<string | null>(null);
  const [dragOverCredentialId, setDragOverCredentialId] = useState<string | null>(null);
  const [reorderingProviderId, setReorderingProviderId] = useState<string | null>(null);
  const credentialsRef = useRef(credentials);
  const refreshingRef = useRef(false);
  const refreshAllRef = useRef<() => Promise<void>>(async () => {});

  credentialsRef.current = credentials;

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

  // Drop optimistic orders that no longer describe the current snapshot (for
  // example after an account is added or deleted elsewhere).
  useEffect(() => {
    const idsByProvider = new Map<string, Set<string>>();
    for (const credential of eligible) {
      const ids = idsByProvider.get(credential.providerId) || new Set<string>();
      ids.add(credential.id);
      idsByProvider.set(credential.providerId, ids);
    }
    setCredentialOrderByProvider((current) => {
      let changed = false;
      const next: Record<string, string[]> = {};
      for (const [providerId, order] of Object.entries(current)) {
        const ids = idsByProvider.get(providerId);
        if (!ids || order.length !== ids.size || order.some((id) => !ids.has(id))) {
          changed = true;
          continue;
        }
        next[providerId] = order;
      }
      return changed ? next : current;
    });
  }, [eligible]);

  const providerOptions = useMemo(() => {
    const types = new Set(eligible.map((item) => quotaProviderKey(item)));
    return [...types].sort();
  }, [eligible]);

  const providerFilter = useMemo(
    () => resolveQuotaProviderFilter(searchParams.get("provider"), providerOptions),
    [searchParams, providerOptions],
  );

  const accountFilter = useMemo(
    () => parseQuotaAccountFilter(searchParams.get("status")),
    [searchParams],
  );

  const expiringFirst = isQuotaExpiringSort(searchParams.get("sort"));

  const updateQuotaFilters = useCallback(
    (patch: Partial<{ provider: string; status: AccountFilter; sort: QuotaUrlSort }>) => {
      setSearchParams((current) => patchQuotaSearchParams(current, patch), { replace: true });
    },
    [setSearchParams],
  );

  useEffect(() => {
    const raw = searchParams.get("provider")?.trim().toLowerCase();
    if (!raw || raw === "all" || providerOptions.length === 0) return;
    if (!providerOptions.includes(raw)) {
      setSearchParams((current) => patchQuotaSearchParams(current, { provider: "all" }), { replace: true });
    }
  }, [providerOptions, searchParams, setSearchParams]);

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

  const providerCounts = useMemo(
    () => buildProviderCountMap(eligible, quotaProviderKey),
    [eligible],
  );

  const accountCountByProviderId = useMemo(() => {
    const counts: Record<string, number> = {};
    for (const credential of eligible) {
      counts[credential.providerId] = (counts[credential.providerId] || 0) + 1;
    }
    return counts;
  }, [eligible]);

  const scopeCredentials = useMemo(() => {
    if (providerFilter === "all") return eligible;
    return eligible.filter((item) => quotaProviderKey(item) === providerFilter);
  }, [eligible, providerFilter]);

  const accountStats = useMemo(() => {
    const enabled = scopeCredentials.filter((item) => item.enabled).length;
    return {
      total: scopeCredentials.length,
      enabled,
      disabled: scopeCredentials.length - enabled,
    };
  }, [scopeCredentials]);

  const sortedCredentials = useMemo(() => {
    return [...filteredCredentials].sort((a, b) => {
      const providerA = quotaProviderKey(a);
      const providerB = quotaProviderKey(b);
      if (a.providerId === b.providerId) {
        if (expiringFirst) {
          const diff = earliestResetAt(quotaById[a.id]) - earliestResetAt(quotaById[b.id]);
          if (diff !== 0) return diff;
        }
        const providerOrder = credentialOrderByProvider[a.providerId];
        if (providerOrder) {
          const leftIndex = providerOrder.indexOf(a.id);
          const rightIndex = providerOrder.indexOf(b.id);
          if (leftIndex >= 0 && rightIndex >= 0 && leftIndex !== rightIndex) return leftIndex - rightIndex;
        }
        const priorityDiff = (b.priority ?? 0) - (a.priority ?? 0);
        if (priorityDiff !== 0) return priorityDiff;
        return (getConnectionLabel(a) || a.id).localeCompare(getConnectionLabel(b) || b.id);
      }
      if (a.enabled !== b.enabled) return a.enabled ? -1 : 1;
      const countDiff = (providerCounts[providerB] || 0) - (providerCounts[providerA] || 0);
      if (countDiff !== 0) {
        return countDiff;
      }
      if (providerA !== providerB) {
        return providerA.localeCompare(providerB);
      }
      return (getConnectionLabel(a) || a.id).localeCompare(getConnectionLabel(b) || b.id);
    });
  }, [filteredCredentials, expiringFirst, quotaById, providerCounts, credentialOrderByProvider]);

  const credentialIdsKey = useMemo(
    () => sortedCredentials.map((item) => item.id).join("\u0000"),
    [sortedCredentials],
  );

  // Account routing controls stay locked until every account currently shown
  // on the page has completed its first quota request. A failed request still
  // counts as settled because its error is visible and the operator can retry.
  const quotaDataReady = useMemo(
    () => sortedCredentials.every((credential) => settledQuotaCredentialIds.has(credential.id)),
    [sortedCredentials, settledQuotaCredentialIds],
  );

  const loadQuota = useCallback(
    async (credentialId: string): Promise<boolean> => {
      setLoading((current) => ({ ...current, [credentialId]: true }));
      setErrors((current) => ({ ...current, [credentialId]: "" }));
      try {
        const quota = await fetchCredentialQuota(secret, credentialId);
        setQuotaById((current) => ({ ...current, [credentialId]: quota }));
        if (typeof quota.credential_enabled === "boolean") {
          const credential = credentialsRef.current.find((item) => item.id === credentialId);
          return Boolean(credential && credential.enabled !== quota.credential_enabled);
        }
        return false;
      } catch (cause) {
        const message = cause instanceof Error ? cause.message : t("quota.failedToFetch");
        setErrors((current) => ({ ...current, [credentialId]: message }));
        return false;
      } finally {
        setLoading((current) => ({ ...current, [credentialId]: false }));
        setSettledQuotaCredentialIds((current) => {
          if (current.has(credentialId)) return current;
          const next = new Set(current);
          next.add(credentialId);
          return next;
        });
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
    if (refreshingRef.current) return;
    refreshingRef.current = true;
    setRefreshingAll(true);
    setCountdown(60);
    try {
      await loadProxyUsage();
      const changed = await runWithConcurrency(
        sortedCredentials.map((item) => item.id),
        (credentialId) => loadQuota(credentialId),
      );
      if (changed.some(Boolean)) {
        onMutated?.();
      }
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("quota.failedToRefresh"));
    } finally {
      refreshingRef.current = false;
      setRefreshingAll(false);
    }
  }, [sortedCredentials, loadQuota, loadProxyUsage, onError, onMutated]);

  refreshAllRef.current = refreshAll;

  useEffect(() => {
    void refreshAllRef.current();
  }, [credentialIdsKey]);

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
      void refreshAllRef.current();
    }, REFRESH_INTERVAL_MS);
    const countdownTimer = window.setInterval(() => {
      setCountdown((value) => (value <= 1 ? 60 : value - 1));
    }, 1000);
    return () => {
      window.clearInterval(refreshTimer);
      window.clearInterval(countdownTimer);
    };
  }, [autoRefresh]);

  const setCredentialEnabled = async (credential: CredentialRow, enabled: boolean) => {
    if (!quotaDataReady) return;
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
          priority: credential.priority,
          weight: credential.weight,
          proxy_pools: credential.proxy_pool_ids,
        },
      });
      onMutated?.();
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("quota.failedToUpdate"));
    } finally {
      setTogglingId(null);
    }
  };

  const handleDelete = async (credential: CredentialRow) => {
    if (!window.confirm(t("quota.deleteConnectionConfirm"))) return;
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
      onError(cause instanceof Error ? cause.message : t("quota.deleteFailed"));
    } finally {
      setDeletingId(null);
    }
  };

  const handleCredentialDrop = async (event: React.DragEvent<HTMLElement>, target: CredentialRow) => {
    event.preventDefault();
    const sourceId = event.dataTransfer.getData("text/plain") || draggingCredentialId;
    setDraggingCredentialId(null);
    setDragOverCredentialId(null);
    if (!sourceId || sourceId === target.id || reorderingProviderId) return;
    const source = credentials.find((credential) => credential.id === sourceId);
    if (!source || source.providerId !== target.providerId) return;

    const previousOrder = credentialOrderByProvider[target.providerId];
    const savedOrder = previousOrder;
    const providerOrder = [...credentials]
      .filter((credential) => credential.providerId === target.providerId)
      .sort((left, right) => {
        if (savedOrder) {
          const leftIndex = savedOrder.indexOf(left.id);
          const rightIndex = savedOrder.indexOf(right.id);
          if (leftIndex >= 0 && rightIndex >= 0 && leftIndex !== rightIndex) return leftIndex - rightIndex;
        }
        const priorityDiff = (right.priority ?? 0) - (left.priority ?? 0);
        if (priorityDiff !== 0) return priorityDiff;
        return (getConnectionLabel(left) || left.id).localeCompare(getConnectionLabel(right) || right.id);
      });
    const next = moveCredentialBefore(providerOrder, sourceId, target.id);
    if (next === providerOrder) return;
    const nextIds = next.map((credential) => credential.id);
    setCredentialOrderByProvider((current) => ({ ...current, [target.providerId]: nextIds }));
    setReorderingProviderId(target.providerId);
    try {
      await reorderProviderCredentials(secret, target.providerId, nextIds);
      onNotice?.("Account order saved");
      onMutated?.();
    } catch (cause) {
      setCredentialOrderByProvider((current) => {
        const nextState = { ...current };
        if (previousOrder) nextState[target.providerId] = previousOrder;
        else delete nextState[target.providerId];
        return nextState;
      });
      onError(cause instanceof Error ? cause.message : "Failed to save account order");
    } finally {
      setReorderingProviderId(null);
    }
  };

  const bulkSetEnabled = async (targets: CredentialRow[], enabled: boolean) => {
    if (!targets.length || bulkToggling || !quotaDataReady) return;
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
      (credential) => !credential.enabled && !isConnectionAtZero(quotaById[credential.id], quotaProviderKey(credential)),
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
      await loadQuota(credential.id).then((changed) => {
        if (changed) onMutated?.();
      });
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("quota.failedToResetCodex"));
    } finally {
      setResettingLimitId(null);
    }
  };

  const selectedCredential = useMemo(
    () => (detailCredential ? credentials.find((item) => item.id === detailCredential.id) ?? detailCredential : null),
    [credentials, detailCredential],
  );

  const selectedProviderLabel =
    providerFilter === "all" ? t("quota.allProviders") : getProviderTypeInfo(providerFilter).name;

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
        <div className="quota-tracker-stats" aria-label={t("quota.accountStatistics")}>
          <span className="quota-tracker-stat quota-tracker-stat-on">
            <span className="quota-tracker-stat-dot" aria-hidden="true" />
            <strong>{accountStats.enabled}</strong> on
          </span>
          <span className="quota-tracker-stat quota-tracker-stat-off">
            <span className="quota-tracker-stat-dot" aria-hidden="true" />
            <strong>{accountStats.disabled}</strong> off
          </span>
          {providerFilter === "all" ? (
            <>
              <span className="quota-tracker-stats-divider" aria-hidden="true" />
              <div className="quota-tracker-provider-stats">
                {providerOptions.map((providerType) => {
                  const info = getProviderTypeInfo(providerType);
                  const count = providerCounts[providerType] || 0;
                  const enabledCount = eligible.filter(
                    (item) => quotaProviderKey(item) === providerType && item.enabled,
                  ).length;
                  return (
                    <span
                      key={providerType}
                      className="quota-tracker-provider-stat"
                      title={`${info.name}: ${count} accounts (${enabledCount} on, ${count - enabledCount} off)`}
                    >
                      <ProviderLogo
                        className="quota-tracker-provider-icon"
                        providerType={providerType}
                        style={{ color: info.color }}
                      />
                      <span className="quota-tracker-provider-stat-name">{info.name}</span>
                      <span className="quota-tracker-provider-stat-count">{count}</span>
                    </span>
                  );
                })}
              </div>
            </>
          ) : (
            <span className="quota-tracker-stat-meta">
              {accountStats.total} in {selectedProviderLabel}
            </span>
          )}
        </div>
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
                <button type="button" className="quota-tracker-backdrop" aria-label={t("quota.closeProviderFilter")} onClick={() => setProviderMenuOpen(false)} />
                <div className="quota-tracker-menu">
                  <button
                    type="button"
                    className={cn("quota-tracker-menu-item", providerFilter === "all" && "is-active")}
                    onClick={() => {
                      updateQuotaFilters({ provider: "all" });
                      setProviderMenuOpen(false);
                    }}
                  >
                    <span className="material-symbols-outlined">apps</span>
                    <span className="quota-tracker-menu-item-text">All providers</span>
                    <span className="quota-tracker-menu-count">{eligible.length}</span>
                  </button>
                  {providerOptions.map((providerType) => {
                    const info = getProviderTypeInfo(providerType);
                    return (
                      <button
                        key={providerType}
                        type="button"
                        className={cn("quota-tracker-menu-item", providerFilter === providerType && "is-active")}
                        onClick={() => {
                          updateQuotaFilters({ provider: providerType });
                          setProviderMenuOpen(false);
                        }}
                      >
                        <ProviderLogo
                          className="quota-tracker-provider-icon"
                          providerType={providerType}
                          style={{ color: info.color }}
                        />
                        <span className="quota-tracker-menu-item-text">{info.name}</span>
                        <span className="quota-tracker-menu-count">{providerCounts[providerType] || 0}</span>
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
            onChange={(event) => updateQuotaFilters({ status: event.target.value as AccountFilter })}
            aria-label={t("quota.filterByStatus")}
          >
            {ACCOUNT_FILTER_OPTIONS.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
                {option.value === "all"
                  ? ` (${accountStats.total})`
                  : option.value === "active"
                    ? ` (${accountStats.enabled})`
                    : ` (${accountStats.disabled})`}
              </option>
            ))}
          </select>

          <button
            type="button"
            className={cn("quota-tracker-chip", expiringFirst && "quota-tracker-chip-amber")}
            onClick={() => updateQuotaFilters({ sort: expiringFirst ? "default" : "expiring" })}
            aria-pressed={expiringFirst}
          >
            <span className="material-symbols-outlined">hourglass_top</span>
            <span>Expiring first</span>
          </button>

          <button
            type="button"
            className="quota-tracker-chip quota-tracker-chip-danger"
            disabled={bulkToggling || !quotaDataReady}
            onClick={handleDisableDepleted}
          >
            <span className="material-symbols-outlined">block</span>
            <span>Turn off Empty</span>
          </button>

          <button
            type="button"
            className="quota-tracker-chip quota-tracker-chip-success"
            disabled={bulkToggling || !quotaDataReady}
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
            aria-label={t("quota.refreshAll")}
          >
            <span className={cn("material-symbols-outlined", refreshingAll && "animate-spin")}>refresh</span>
          </button>
        </div>
      </div>

      {!quotaDataReady ? (
        <div className="quota-tracker-banner quota-tracker-order-hint">
          <span className="material-symbols-outlined animate-spin" aria-hidden="true">progress_activity</span>
          Loading account quota data… account toggles will unlock when loading completes.
        </div>
      ) : expiringFirst ? (
        <div className="quota-tracker-banner">
          Expiring-first reorders accounts on this page by earliest quota reset time.
        </div>
      ) : accountStats.total > 1 ? (
        <div className="quota-tracker-banner quota-tracker-order-hint">
          <span className="material-symbols-outlined" aria-hidden="true">drag_indicator</span>
          Drag accounts within a provider to choose the order tproxy tries them. The first account is used first.
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
            const renewalDate = formatRenewalDate(quota?.renews_at);

            const isQuotaAutoDisabled = quota?.quota_auto_disabled === true;
            const canReorder = !expiringFirst && (accountCountByProviderId[credential.providerId] || 0) > 1;
            const isDragging = draggingCredentialId === credential.id;
            const isDragOver = dragOverCredentialId === credential.id;

            return (
              <article
                key={credential.id}
                className={cn(
                  "quota-tracker-card",
                  "quota-tracker-card-selectable",
                  !credential.enabled && "quota-tracker-card-inactive",
                  detailCredential?.id === credential.id && "quota-tracker-card-selected",
                  isDragging && "is-dragging",
                  isDragOver && "is-drag-over",
                )}
                role="button"
                tabIndex={0}
                aria-label={`View details for ${getConnectionLabel(credential) || credential.id}`}
                onDragOver={(event) => {
                  const source = credentials.find((item) => item.id === draggingCredentialId);
                  if (!reorderingProviderId && source && source.providerId === credential.providerId && source.id !== credential.id) {
                    event.preventDefault();
                    event.dataTransfer.dropEffect = "move";
                    setDragOverCredentialId(credential.id);
                  }
                }}
                onDrop={(event) => void handleCredentialDrop(event, credential)}
                onClick={() => setDetailCredential(credential)}
                onKeyDown={(event) => {
                  if (event.key === "Enter" || event.key === " ") {
                    event.preventDefault();
                    setDetailCredential(credential);
                  }
                }}
              >
                <div
                  className="quota-tracker-card-head"
                  onClick={(event) => event.stopPropagation()}
                  onKeyDown={(event) => event.stopPropagation()}
                >
                  <div className="quota-tracker-card-head-row">
                    {canReorder ? (
                      <button
                        type="button"
                        className="quota-tracker-drag-handle"
                        draggable={!reorderingProviderId && !expiringFirst}
                        disabled={Boolean(reorderingProviderId) || expiringFirst}
                        onClick={(event) => event.stopPropagation()}
                        onDragStart={(event) => {
                          event.stopPropagation();
                          event.dataTransfer.effectAllowed = "move";
                          event.dataTransfer.setData("text/plain", credential.id);
                          setDraggingCredentialId(credential.id);
                          setDragOverCredentialId(null);
                        }}
                        onDragEnd={() => {
                          setDraggingCredentialId(null);
                          setDragOverCredentialId(null);
                        }}
                        aria-label={`Drag ${getConnectionLabel(credential) || credential.id} to change account order`}
                        title="Drag to change account order"
                      >
                        <span className="material-symbols-outlined" aria-hidden="true">drag_indicator</span>
                      </button>
                    ) : null}
                    <div className="quota-tracker-card-ident">
                      <ProviderLogo
                        className="quota-tracker-provider-icon quota-tracker-provider-icon-lg"
                        providerType={quotaKey}
                        style={{ color: info.color }}
                      />
                      <div className="quota-tracker-card-titles">
                        <h3>{info.name}</h3>
                        {getConnectionLabel(credential) ? <p>{getConnectionLabel(credential)}</p> : null}
                        {isQuotaAutoDisabled ? (
                          <p className="quota-tracker-card-email">Paused automatically — quota at 0%</p>
                        ) : null}
                        {secondary ? <p className="quota-tracker-card-email">{secondary}</p> : null}
                      </div>
                    </div>
                    <Toggle
                      checked={credential.enabled}
                      disabled={rowBusy || !quotaDataReady}
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
                          aria-label={t("quota.viewResetExpiry")}
                          title={t("quota.viewResetExpiry")}
                        >
                          <span className="material-symbols-outlined">schedule</span>
                        </button>
                      </>
                    ) : null}
                    <button
                      type="button"
                      className="quota-tracker-icon-btn"
                      disabled={busy || rowBusy}
                      onClick={() => {
                        void loadQuota(credential.id).then((changed) => {
                          if (changed) onMutated?.();
                        });
                      }}
                      aria-label={t("quota.refreshQuota")}
                      title={t("quota.refreshQuota")}
                    >
                      <span className={cn("material-symbols-outlined", busy && "animate-spin")}>refresh</span>
                    </button>
                    <button
                      type="button"
                      className="quota-tracker-icon-btn"
                      disabled={rowBusy}
                      onClick={() => navigate(`/providers/${encodeURIComponent(credential.providerId)}`)}
                      aria-label={t("quota.editConnection")}
                      title={t("quota.editConnection")}
                    >
                      <span className="material-symbols-outlined">edit</span>
                    </button>
                    <button
                      type="button"
                      className="quota-tracker-icon-btn quota-tracker-icon-btn-danger"
                      disabled={rowBusy}
                      onClick={() => void handleDelete(credential)}
                      aria-label={t("quota.deleteConnection")}
                      title={t("quota.deleteConnection")}
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
                    usesQuotaStackedLayout(quotaKey) ? (
                      <QuotaStackedBar
                        rows={visibleEntries}
                        proxyUsage={
                          proxyUsageById[credential.id] ?? { requests: 0, promptTokens: 0, completionTokens: 0 }
                        }
                        onHide={(row) => handleHideQuota(quotaKey, getQuotaVisibilityKey(row))}
                      />
                    ) : usesQuotaRingLayout(quotaKey) ? (
                      <QuotaRingGrid
                        rows={visibleEntries}
                        proxyUsage={
                          proxyUsageById[credential.id] ?? { requests: 0, promptTokens: 0, completionTokens: 0 }
                        }
                        onHide={(row) => handleHideQuota(quotaKey, getQuotaVisibilityKey(row))}
                      />
                    ) : (
                      <QuotaTable
                        rows={visibleEntries}
                        credentialActive={activeCredentialIds.has(credential.id)}
                        proxyUsage={
                          proxyUsageById[credential.id] ?? { requests: 0, promptTokens: 0, completionTokens: 0 }
                        }
                        onHide={(row) => handleHideQuota(quotaKey, getQuotaVisibilityKey(row))}
                      />
                    )
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

                  {renewalDate ? (
                    <div className="quota-tracker-card-renewal">
                      <span className="material-symbols-outlined">event_repeat</span>
                      <span>Renews {renewalDate}</span>
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
        title={t("quota.resetCodexConfirm")}
        message={
          resetConfirmCredential
            ? `Use 1 Codex reset credit for ${getConnectionLabel(resetConfirmCredential) || resetConfirmCredential.email || "this account"}. This cannot be undone. Remaining credits: ${getCodexResetCreditCount(quotaById[resetConfirmCredential.id])}.`
            : ""
        }
        confirmText={t("quota.resetLimit")}
        cancelText={t("common.cancel")}
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

      <QuotaAccountDetailModal
        open={Boolean(selectedCredential)}
        secret={secret}
        credential={selectedCredential}
        quotaKey={selectedCredential ? quotaProviderKey(selectedCredential) : ""}
        quota={selectedCredential ? quotaById[selectedCredential.id] : undefined}
        proxyUsage={selectedCredential ? proxyUsageById[selectedCredential.id] : undefined}
        credentialActive={selectedCredential ? activeCredentialIds.has(selectedCredential.id) : false}
        toggling={selectedCredential ? togglingId === selectedCredential.id : false}
        accountToggleReady={quotaDataReady}
        onClose={() => setDetailCredential(null)}
        onToggleEnabled={(enabled) => {
          if (selectedCredential) void setCredentialEnabled(selectedCredential, enabled);
        }}
        onQuotaUpdated={(quota) => {
          if (!selectedCredential) return;
          setQuotaById((current) => ({ ...current, [selectedCredential.id]: quota }));
        }}
        refreshCountdown={countdown}
      />
    </section>
  );
}
