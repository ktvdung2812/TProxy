import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useSearchParams } from "react-router-dom";
import { clearCredentialCooldown, deleteCredential, reorderProviderCredentials, saveCredential } from "../providers/api";
import { getProviderTypeInfo } from "../providers/catalog";
import { ProviderLogo } from "../providers/ProviderLogo";
import { useUsageStream } from "../usage/useUsageStream";
import { Button, ConfirmDialog, Modal, cn } from "../ui";
import { CodexResetCreditsModal } from "./CodexResetCreditsModal";
import { QuotaAccountDetailModal } from "./QuotaAccountDetailModal";
import { QuotaAccountCard, QuotaAccountRow, type QuotaAccountHandlers, type QuotaAccountView, type QuotaCredential } from "./QuotaAccountItem";
import {
  ActionMenu,
  AutoRefreshToggle,
  BulkBar,
  BulkButton,
  EmptyPanel,
  FilterTabs,
  GuideToggle,
  LegendItem,
  LinkButton,
  PageGuide,
  PageNote,
  PageStats,
  ProgressLine,
  SearchField,
  SectionHead,
  Segmented,
  SelectField,
  ToneCounts,
  Toolbar,
  ToolbarEnd,
  ToolbarRow,
  ToolButton,
  usePageGuide,
  useStoredState,
} from "../page-kit";
import type { QuotaMeterRow } from "./QuotaMeters";
import { runQuotaActions, consumeCodexResetCredit, fetchCredentialProxyUsage, type CredentialProxyUsage, type CredentialQuota } from "./api";
import { clearCachedQuota, loadCredentialQuotaOnce, peekCachedQuota } from "./quotaOnce";
import { formatServicePlanLabel, isOnCooldown, moveCredentialBefore } from "../providers/types";
import { maskAccountText } from "../../lib/accountMask";
import { useAccountMask } from "../../hooks/useAccountMask";
import {
  AUTO_REFRESH_STORAGE_KEY,
  QUOTA_VIEW_MODE_KEY,
  QUOTA_VISIBILITY_KEY,
  CLAUDE_REFRESH_INTERVAL_MS,
  REFRESH_INTERVAL_MS,
  type AccountFilter,
  type AccountHealth,
  type AccountHealthBucket,
  type QuotaVisibility,
  accountHealthBucket,
  buildProviderCountMap,
  earliestResetAt,
  filterQuotasByVisibility,
  findEarliestQuotaReset,
  formatProxyUsageLabel,
  formatQuotaName,
  formatResetTime,
  getAccountHealth,
  getConnectionLabel,
  getHiddenQuotaRows,
  getQuotaVisibilityKey,
  isConnectionAtZero,
  isConnectionDepleted,
  lowestRemaining,
  matchesAccountFilter,
  parseQuotaAccountFilter,
  parseQuotaSort,
  patchQuotaSearchParams,
  quotaEntries,
  resolveQuotaProviderFilter,
  compareQuotaAccountOrder,
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
  "devin",
  "opencode-go",
]);

function quotaProviderKey(item: Pick<CredentialRow, "providerId" | "providerType">): string {
  if (QUOTA_PROVIDER_TYPES.has(item.providerId)) return item.providerId;
  return item.providerType;
}

function supportsQuotaTracker(item: Pick<CredentialRow, "providerId" | "providerType">): boolean {
  return QUOTA_PROVIDER_TYPES.has(item.providerId) || QUOTA_PROVIDER_TYPES.has(item.providerType);
}

type CredentialRow = QuotaCredential;

type ViewMode = "grid" | "list";
const VIEW_MODES = ["grid", "list"] as const;

type OverviewCounts = Record<AccountHealthBucket, number> & { total: number; checking: number };

type Props = {
  secret: string;
  credentials: CredentialRow[];
  accountsLoading?: boolean;
  onError: (message: string) => void;
  onNotice?: (message: string) => void;
  onMutated?: () => void;
};

const EMPTY_USAGE: CredentialProxyUsage = { requests: 0, promptTokens: 0, completionTokens: 0 };
const GROK_PRODUCT_BREAKDOWN_PREFIX = "product_";

function readStorage(key: string): string | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

function writeStorage(key: string, value: string) {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(key, value);
  } catch {
    // Private mode or a full quota: the preference just does not persist.
  }
}

function loadVisibility(): QuotaVisibility {
  try {
    return JSON.parse(readStorage(QUOTA_VISIBILITY_KEY) || "{}") as QuotaVisibility;
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

export function QuotaTrackerView({ secret, credentials, accountsLoading = false, onError, onNotice, onMutated }: Props) {
  const { t } = useTranslation();
  const { masked } = useAccountMask();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const [quotaById, setQuotaById] = useState<Record<string, CredentialQuota>>({});
  const [loading, setLoading] = useState<Record<string, boolean>>({});
  const [settledQuotaCredentialIds, setSettledQuotaCredentialIds] = useState<Set<string>>(() => new Set());
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [refreshingAll, setRefreshingAll] = useState(false);
  const [autoRefresh, setAutoRefresh] = useState(() => readStorage(AUTO_REFRESH_STORAGE_KEY) !== "false");
  const [nextRefreshAt, setNextRefreshAt] = useState(() => Date.now() + REFRESH_INTERVAL_MS);
  const [clock, setClock] = useState(() => Date.now());
  const [search, setSearch] = useState("");
  const [viewMode, setViewMode] = useStoredState<ViewMode>(QUOTA_VIEW_MODE_KEY, "grid", VIEW_MODES);
  const guide = usePageGuide("quota");
  const [bulkToggling, setBulkToggling] = useState(false);
  const [togglingId, setTogglingId] = useState<string | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [deleteConfirmCredential, setDeleteConfirmCredential] = useState<CredentialRow | null>(null);
  const [quotaVisibility, setQuotaVisibility] = useState<QuotaVisibility>(() => loadVisibility());
  const [resetConfirmCredential, setResetConfirmCredential] = useState<CredentialRow | null>(null);
  const [bulkResetConfirmOpen, setBulkResetConfirmOpen] = useState(false);
  const [resetCreditsCredential, setResetCreditsCredential] = useState<CredentialRow | null>(null);
  const [detailCredential, setDetailCredential] = useState<CredentialRow | null>(null);
  const [resettingLimitId, setResettingLimitId] = useState<string | null>(null);
  const [clearingCooldownId, setClearingCooldownId] = useState<string | null>(null);
  const [selectedCredentialIds, setSelectedCredentialIds] = useState<Set<string>>(() => new Set());
  const [quotaBulkBusy, setQuotaBulkBusy] = useState(false);
  const [bulkResults, setBulkResults] = useState<{
    title: string;
    items: Array<{ id: string; label: string; ok: boolean; message: string }>;
  } | null>(null);
  const [activeCredentialIds, setActiveCredentialIds] = useState<Set<string>>(() => new Set());
  const [proxyUsageById, setProxyUsageById] = useState<Record<string, CredentialProxyUsage>>({});
  const [credentialOrderByProvider, setCredentialOrderByProvider] = useState<Record<string, string[]>>({});
  const [draggingCredentialId, setDraggingCredentialId] = useState<string | null>(null);
  const [dragOverCredentialId, setDragOverCredentialId] = useState<string | null>(null);
  const [reorderingProviderId, setReorderingProviderId] = useState<string | null>(null);
  const credentialsRef = useRef(credentials);
  const passRunningRef = useRef(false);
  const cancelledRef = useRef(false);
  const inFlightIdsRef = useRef(new Set<string>());
  const refreshAllRef = useRef<(options?: { auto?: boolean }) => Promise<void>>(async () => {});
  const loadedAtRef = useRef<Map<string, number>>(new Map());
  const ensureLoadedRef = useRef<() => Promise<void>>(async () => {});
  const quotaPassReadyRef = useRef(false);

  credentialsRef.current = credentials;

  const applyLiveUsage = useCallback((update: { activeRequests?: Array<{ credential_id?: string }> }) => {
    const next = new Set<string>();
    for (const item of update.activeRequests || []) {
      if (item.credential_id) next.add(item.credential_id);
    }
    setActiveCredentialIds(next);
  }, []);

  useUsageStream(secret, true, applyLiveUsage);

  // Relative reset times and cooldowns are recomputed twice a minute.
  useEffect(() => {
    const timer = window.setInterval(() => setClock(Date.now()), 30000);
    return () => window.clearInterval(timer);
  }, []);

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

  const sortMode = parseQuotaSort(searchParams.get("sort"));

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

  const resolveQuota = useCallback(
    (credentialId: string) => quotaById[credentialId] || peekCachedQuota(credentialId)?.quota,
    [quotaById],
  );

  const healthById = useMemo(() => {
    const result: Record<string, AccountHealth> = {};
    for (const credential of eligible) {
      result[credential.id] = getAccountHealth({
        enabled: credential.enabled,
        quota: resolveQuota(credential.id),
        providerType: quotaProviderKey(credential),
        error: errors[credential.id],
        settled: settledQuotaCredentialIds.has(credential.id),
        onCooldown: isOnCooldown(credential.cooldown_until),
      });
    }
    return result;
    // clock re-evaluates cooldowns that expire while the page is open.
  }, [eligible, resolveQuota, errors, settledQuotaCredentialIds, clock]);

  // Provider + search scope. The overview counts and the routing lock are
  // computed over this, so clicking a status tile does not change either.
  const scopeCredentials = useMemo(() => {
    const query = search.trim().toLowerCase();
    return eligible.filter((item) => {
      if (providerFilter !== "all" && quotaProviderKey(item) !== providerFilter) return false;
      if (!query) return true;
      const plan = resolveQuota(item.id)?.plan || "";
      return [item.label, item.email, item.id, item.providerId, plan]
        .some((value) => value?.toLowerCase().includes(query));
    });
  }, [eligible, providerFilter, search, resolveQuota]);

  const overviewCounts = useMemo<OverviewCounts>(() => {
    const counts: OverviewCounts = { total: scopeCredentials.length, available: 0, low: 0, attention: 0, off: 0, checking: 0 };
    for (const credential of scopeCredentials) {
      const bucket = accountHealthBucket(healthById[credential.id]);
      if (bucket) counts[bucket] += 1;
      else counts.checking += 1;
    }
    return counts;
  }, [scopeCredentials, healthById]);

  const filteredCredentials = useMemo(
    () => scopeCredentials.filter((item) => matchesAccountFilter(accountFilter, healthById[item.id], item.enabled)),
    [scopeCredentials, accountFilter, healthById],
  );

  const accountOrderOptions = useMemo(
    () => ({
      providerKey: quotaProviderKey,
      providerCounts,
      providerOrder: credentialOrderByProvider,
    }),
    [providerCounts, credentialOrderByProvider],
  );

  const loadOrderCredentials = useMemo(
    () => [...eligible].sort((a, b) => compareQuotaAccountOrder(a, b, accountOrderOptions)),
    [eligible, accountOrderOptions],
  );

  // Position of each account in its provider's routing order — what the
  // router tries first — independent of filters and display sort.
  const rankById = useMemo(() => {
    const ranks: Record<string, number> = {};
    const seen: Record<string, number> = {};
    for (const credential of loadOrderCredentials) {
      seen[credential.providerId] = (seen[credential.providerId] || 0) + 1;
      ranks[credential.id] = seen[credential.providerId];
    }
    return ranks;
  }, [loadOrderCredentials]);

  const loadOrderRef = useRef(loadOrderCredentials);
  loadOrderRef.current = loadOrderCredentials;

  const settledRef = useRef(settledQuotaCredentialIds);
  settledRef.current = settledQuotaCredentialIds;

  const eligibleIdsKey = useMemo(
    () => eligible.map((item) => item.id).slice().sort().join("\u0000"),
    [eligible],
  );

  // Account routing controls stay locked until every account in scope has
  // completed its first quota request. A failed request still counts as
  // settled because its error is visible and the operator can retry.
  const quotaDataReady = useMemo(
    () => scopeCredentials.every((credential) => settledQuotaCredentialIds.has(credential.id)),
    [scopeCredentials, settledQuotaCredentialIds],
  );
  const quotaPassReady = useMemo(
    () =>
      loadOrderCredentials.length > 0 &&
      loadOrderCredentials.every((credential) => settledQuotaCredentialIds.has(credential.id)),
    [loadOrderCredentials, settledQuotaCredentialIds],
  );
  quotaPassReadyRef.current = quotaPassReady;

  const sortedCredentials = useMemo(() => {
    const ordered = [...filteredCredentials].sort((a, b) => compareQuotaAccountOrder(a, b, accountOrderOptions));
    // Re-sorting while quota is still arriving would shuffle cards under the cursor.
    if (sortMode === "default" || !quotaDataReady) return ordered;
    const keyById: Record<string, number> = {};
    for (const row of ordered) {
      if (sortMode === "expiring") keyById[row.id] = earliestResetAt(resolveQuota(row.id));
      else keyById[row.id] = lowestRemaining(quotaEntries(resolveQuota(row.id))) ?? Number.POSITIVE_INFINITY;
    }
    return ordered.sort((a, b) => compareQuotaAccountOrder(a, b, { ...accountOrderOptions, resetAtById: keyById }));
  }, [filteredCredentials, accountOrderOptions, sortMode, quotaDataReady, resolveQuota]);

  const groups = useMemo(() => {
    const byProvider = new Map<string, CredentialRow[]>();
    for (const credential of sortedCredentials) {
      const items = byProvider.get(credential.providerId) || [];
      items.push(credential);
      byProvider.set(credential.providerId, items);
    }
    return [...byProvider.entries()]
      .map(([providerId, items]) => ({ providerId, quotaKey: quotaProviderKey(items[0]), items }))
      .sort((a, b) =>
        (accountCountByProviderId[b.providerId] || 0) - (accountCountByProviderId[a.providerId] || 0)
        || getProviderTypeInfo(a.quotaKey).name.localeCompare(getProviderTypeInfo(b.quotaKey).name)
        || a.providerId.localeCompare(b.providerId),
      );
  }, [sortedCredentials, accountCountByProviderId]);

  const selectedCredentials = useMemo(
    () => eligible.filter((credential) => selectedCredentialIds.has(credential.id)),
    [eligible, selectedCredentialIds],
  );
  const selectedCodexResetCredentials = useMemo(
    () => selectedCredentials.filter((credential) => {
      const isCodex = quotaProviderKey(credential) === "codex" || credential.providerType === "codex";
      return isCodex && getCodexResetCreditCount(quotaById[credential.id]) > 0;
    }),
    [selectedCredentials, quotaById],
  );
  const allVisibleSelected = sortedCredentials.length > 0 && sortedCredentials.every((credential) => selectedCredentialIds.has(credential.id));

  useEffect(() => {
    const eligibleIds = new Set(eligible.map((credential) => credential.id));
    setSelectedCredentialIds((current) => {
      const next = new Set([...current].filter((id) => eligibleIds.has(id)));
      return next.size === current.size ? current : next;
    });
  }, [eligible]);

  const quotaPassSettledCount = useMemo(
    () => loadOrderCredentials.filter((item) => settledQuotaCredentialIds.has(item.id)).length,
    [loadOrderCredentials, settledQuotaCredentialIds],
  );

  const markSettled = useCallback((credentialId: string) => {
    settledRef.current = new Set(settledRef.current).add(credentialId);
    setSettledQuotaCredentialIds((current) => {
      if (current.has(credentialId)) return current;
      const next = new Set(current);
      next.add(credentialId);
      return next;
    });
  }, []);

  const loadQuota = useCallback(
    async (credentialId: string, force = false, refreshUpstream = force): Promise<boolean> => {
      if (!force && (settledRef.current.has(credentialId) || inFlightIdsRef.current.has(credentialId))) {
        return false;
      }
      inFlightIdsRef.current.add(credentialId);
      loadedAtRef.current.set(credentialId, Date.now());
      setLoading((current) => ({ ...current, [credentialId]: true }));
      setErrors((current) => ({ ...current, [credentialId]: "" }));
      try {
        const cached = await loadCredentialQuotaOnce(secret, credentialId, force, refreshUpstream);
        if (cached.error) {
          setErrors((current) => ({ ...current, [credentialId]: cached.error || t("quota.failedToFetch") }));
          return false;
        }
        const quota = cached.quota;
        if (!quota) return false;
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
        inFlightIdsRef.current.delete(credentialId);
        setLoading((current) => ({ ...current, [credentialId]: false }));
        markSettled(credentialId);
      }
    },
    [markSettled, secret, t],
  );

  const loadProxyUsage = useCallback(async () => {
    try {
      const response = await fetchCredentialProxyUsage(secret, "all");
      setProxyUsageById(response.by_credential || {});
    } catch {
      // Keep the last known usage if the stats endpoint is temporarily unavailable.
    }
  }, [secret]);

  const runQuotaPass = useCallback(
    async (force: boolean, forcedIds?: string[], refreshUpstream = force) => {
      let changed = false;
      if (force) {
        const ids = forcedIds ?? loadOrderRef.current.map((item) => item.id);
        for (const id of ids) {
          if (cancelledRef.current) return null;
          if (await loadQuota(id, true, refreshUpstream)) changed = true;
        }
      } else {
        while (true) {
          if (cancelledRef.current) return null;
          const next = loadOrderRef.current.find(
            (item) => !settledRef.current.has(item.id) && !inFlightIdsRef.current.has(item.id),
          );
          if (!next) break;
          if (await loadQuota(next.id, false)) changed = true;
        }
      }
      if (cancelledRef.current) return null;
      return changed;
    },
    [loadQuota],
  );

  const ensureLoaded = useCallback(async () => {
    if (passRunningRef.current) return;
    if (loadOrderRef.current.length === 0) return;
    if (loadOrderRef.current.every((item) => settledRef.current.has(item.id))) return;
    passRunningRef.current = true;
    setRefreshingAll(true);
    try {
      await loadProxyUsage();
      if (cancelledRef.current) return;
      const changed = await runQuotaPass(false);
      if (changed) onMutated?.();
    } catch (cause) {
      if (!cancelledRef.current) {
        onError(cause instanceof Error ? cause.message : t("quota.failedToRefresh"));
      }
    } finally {
      passRunningRef.current = false;
      if (!cancelledRef.current) setRefreshingAll(false);
    }
  }, [loadProxyUsage, onError, onMutated, runQuotaPass, t]);

  const refreshAll = useCallback(async (options?: { auto?: boolean }) => {
    if (loadOrderRef.current.length === 0) return;
    // Auto-refresh leaves Claude accounts alone until their slower interval
    // has elapsed; a manual refresh always reloads everything.
    const now = Date.now();
    const skipped = options?.auto
      ? loadOrderRef.current.filter((item) => (
        quotaProviderKey(item) === "claude"
        && now - (loadedAtRef.current.get(item.id) ?? 0) < CLAUDE_REFRESH_INTERVAL_MS - 5000
      ))
      : [];
    const skippedIds = new Set(skipped.map((item) => item.id));
    const ids = loadOrderRef.current.map((item) => item.id).filter((id) => !skippedIds.has(id));
    cancelledRef.current = false;
    passRunningRef.current = true;
    inFlightIdsRef.current = new Set();
    clearCachedQuota(ids);
    settledRef.current = new Set(skippedIds);
    setSettledQuotaCredentialIds(new Set(skippedIds));
    setRefreshingAll(true);
    setNextRefreshAt(Date.now() + REFRESH_INTERVAL_MS);
    try {
      await loadProxyUsage();
      if (cancelledRef.current) return;
      const changed = await runQuotaPass(true, ids, !options?.auto);
      if (changed) onMutated?.();
    } catch (cause) {
      if (!cancelledRef.current) {
        onError(cause instanceof Error ? cause.message : t("quota.failedToRefresh"));
      }
    } finally {
      passRunningRef.current = false;
      if (!cancelledRef.current) setRefreshingAll(false);
    }
  }, [loadProxyUsage, onError, onMutated, runQuotaPass, t]);

  refreshAllRef.current = refreshAll;
  ensureLoadedRef.current = ensureLoaded;

  useEffect(() => {
    cancelledRef.current = false;
    void ensureLoadedRef.current();
    return () => {
      cancelledRef.current = true;
      passRunningRef.current = false;
    };
  }, [eligibleIdsKey]);

  useEffect(() => {
    writeStorage(AUTO_REFRESH_STORAGE_KEY, String(autoRefresh));
  }, [autoRefresh]);

  useEffect(() => {
    writeStorage(QUOTA_VISIBILITY_KEY, JSON.stringify(quotaVisibility));
  }, [quotaVisibility]);

  useEffect(() => {
    if (!autoRefresh) return;
    const timer = window.setTimeout(() => {
      setNextRefreshAt(Date.now() + REFRESH_INTERVAL_MS);
      if (!quotaPassReadyRef.current || passRunningRef.current) return;
      void refreshAllRef.current({ auto: true });
    }, Math.max(1000, nextRefreshAt - Date.now()));
    return () => window.clearTimeout(timer);
  }, [autoRefresh, nextRefreshAt]);

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
    setDeleteConfirmCredential(null);
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
    const providerOrder = [...credentials]
      .filter((credential) => credential.providerId === target.providerId)
      .sort((left, right) =>
        compareQuotaAccountOrder(left, right, {
          providerKey: quotaProviderKey,
          providerCounts,
          providerOrder: previousOrder ? { [target.providerId]: previousOrder } : undefined,
        }),
      );
    const next = moveCredentialBefore(providerOrder, sourceId, target.id);
    if (next === providerOrder) return;
    const nextIds = next.map((credential) => credential.id);
    setCredentialOrderByProvider((current) => ({ ...current, [target.providerId]: nextIds }));
    setReorderingProviderId(target.providerId);
    try {
      await reorderProviderCredentials(secret, target.providerId, nextIds);
      onNotice?.(t("quota.orderSaved"));
      onMutated?.();
    } catch (cause) {
      setCredentialOrderByProvider((current) => {
        const nextState = { ...current };
        if (previousOrder) nextState[target.providerId] = previousOrder;
        else delete nextState[target.providerId];
        return nextState;
      });
      onError(cause instanceof Error ? cause.message : t("quota.orderSaveFailed"));
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

  const depletedTargets = useMemo(
    () => sortedCredentials.filter((credential) => credential.enabled && isConnectionDepleted(resolveQuota(credential.id))),
    [sortedCredentials, resolveQuota],
  );
  const availableTargets = useMemo(
    () => sortedCredentials.filter(
      (credential) => !credential.enabled && !isConnectionAtZero(resolveQuota(credential.id), quotaProviderKey(credential)),
    ),
    [sortedCredentials, resolveQuota],
  );

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
        onError(result.message || t("quota.noResetCreditsToast"));
        return;
      }
      await loadQuota(credential.id, true).then((changed) => {
        if (changed) onMutated?.();
      });
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("quota.failedToResetCodex"));
    } finally {
      setResettingLimitId(null);
    }
  };

  const handleClearCooldown = async (credential: CredentialRow) => {
    setClearingCooldownId(credential.id);
    try {
      await clearCredentialCooldown(secret, credential.id);
      clearCachedQuota([credential.id]);
      const changed = await loadQuota(credential.id, true);
      if (changed) onMutated?.();
      onNotice?.(t("quota.cooldownCleared", { label: maskAccountText(getConnectionLabel(credential) || credential.email || credential.id, masked) }));
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("quota.failedToClearCooldown"));
    } finally {
      setClearingCooldownId(null);
    }
  };

  // A cooled-down account cannot be used until the cooldown is cleared, so
  // "refresh" on one clears it and rechecks instead of only re-reading quota.
  const refreshCredential = (credential: CredentialRow) => {
    if (credential.cooldown_until && isOnCooldown(credential.cooldown_until)) {
      void handleClearCooldown(credential);
      return;
    }
    void loadQuota(credential.id, true).then((changed) => {
      if (changed) onMutated?.();
    });
  };

  const runBulkQuotaAction = async (
    action: "refresh" | "cooldown" | "codex-reset",
    targets: CredentialRow[] = selectedCredentials,
  ) => {
    if (quotaBulkBusy || refreshingAll || passRunningRef.current || targets.length === 0) return;
    setQuotaBulkBusy(true);
    const results: NonNullable<typeof bulkResults>["items"] = [];
    let credentialStateChanged = false;
    try {
      for (let offset = 0; offset < targets.length; offset += 100) {
        const chunk = targets.slice(offset, offset + 100);
        try {
          const items = await runQuotaActions(
            secret,
            action === "cooldown" ? "clear-cooldown" : action === "codex-reset" ? "reset-upstream" : "refresh",
            chunk.map((credential) => credential.id),
          );
          clearCachedQuota(chunk.map((credential) => credential.id));
          for (const item of items) {
            const credential = chunk.find((candidate) => candidate.id === item.credential_id);
            if (!credential) continue;
            if (item.quota) {
              const quota = item.quota;
              setQuotaById((current) => ({ ...current, [credential.id]: quota }));
              markSettled(credential.id);
              credentialStateChanged ||= quota.credential_enabled !== undefined && credential.enabled !== quota.credential_enabled;
            }
            const message = item.ok
              ? t(action === "refresh" ? "quota.refreshed" : action === "cooldown" ? "quota.cooldownClearedAndRefreshed" : "quota.resetAndRefreshed")
              : item.action_applied
                ? t(action === "cooldown" ? "quota.cooldownClearedRefreshFailed" : "quota.resetRefreshFailed", { error: item.error })
                : item.error || t("quota.bulkActionFailed");
            setErrors((current) => ({ ...current, [credential.id]: item.ok ? "" : message }));
            results.push({ id: credential.id, label: maskAccountText(getConnectionLabel(credential) || credential.email || credential.id, masked), ok: item.ok, message });
          }
        } catch (cause) {
          for (const credential of chunk) {
            results.push({
              id: credential.id,
              label: maskAccountText(getConnectionLabel(credential) || credential.email || credential.id, masked),
              ok: false,
              message: cause instanceof Error ? cause.message : t("quota.bulkActionFailed"),
            });
          }
        }
      }
      if (credentialStateChanged) onMutated?.();
      const title = action === "refresh"
        ? t("quota.bulkRefreshResults")
        : action === "cooldown"
          ? t("quota.bulkCooldownResults")
          : t("quota.bulkResetResults");
      // Only interrupt with a results dialog when something needs reading.
      if (results.every((item) => item.ok)) onNotice?.(t("quota.bulk.allSucceeded", { title, count: results.length }));
      else setBulkResults({ title, items: results });
      if (action === "codex-reset") setBulkResetConfirmOpen(false);
    } finally {
      setQuotaBulkBusy(false);
    }
  };

  const toggleSelection = (targets: CredentialRow[], select: boolean) => {
    setSelectedCredentialIds((current) => {
      const next = new Set(current);
      for (const credential of targets) {
        if (select) next.add(credential.id);
        else next.delete(credential.id);
      }
      return next;
    });
  };

  const selectedCredential = useMemo(
    () => (detailCredential ? credentials.find((item) => item.id === detailCredential.id) ?? detailCredential : null),
    [credentials, detailCredential],
  );

  const nextReset = useMemo(() => {
    // Prefer the account that comes back soonest among those that need it;
    // otherwise the next window to roll over anywhere in scope.
    const candidates = scopeCredentials.filter((credential) => credential.enabled);
    const pick = (items: CredentialRow[]) => {
      let best: { time: number; credential: CredentialRow; name: string; resetAt: string } | null = null;
      for (const credential of items) {
        const earliest = findEarliestQuotaReset(quotaEntries(resolveQuota(credential.id)));
        if (earliest && (!best || earliest.time < best.time)) best = { ...earliest, credential };
      }
      return best;
    };
    const needy = candidates.filter((credential) => {
      const bucket = accountHealthBucket(healthById[credential.id]);
      return bucket === "low" || bucket === "attention";
    });
    const best = pick(needy) || pick(candidates);
    if (!best) return null;
    const providerName = getProviderTypeInfo(quotaProviderKey(best.credential)).name;
    const account = maskAccountText(getConnectionLabel(best.credential) || best.credential.id, masked);
    return {
      time: formatResetTime(best.resetAt),
      label: `${providerName} · ${formatQuotaName(best.name)} · ${account}`,
    };
  }, [scopeCredentials, resolveQuota, healthById, masked, clock]);

  const buildView = (credential: CredentialRow): QuotaAccountView => {
    const quotaKey = quotaProviderKey(credential);
    const quota = resolveQuota(credential.id);
    const allEntries = quotaEntries(quota);
    const rows: QuotaMeterRow[] = filterQuotasByVisibility(quotaKey, allEntries, quotaVisibility);
    const hiddenRows: QuotaMeterRow[] = getHiddenQuotaRows(quotaKey, allEntries, quotaVisibility);
    const stacked = usesQuotaStackedLayout(quotaKey) && rows.some((row) => row.key === "weekly");
    const meterRows = usesQuotaStackedLayout(quotaKey)
      ? rows.filter((row) => !row.key.startsWith(GROK_PRODUCT_BREAKDOWN_PREFIX))
      : rows;
    const connectionLabel = getConnectionLabel(credential) || credential.id;
    const secondary = credential.email && credential.label && credential.email !== credential.label ? credential.email : null;
    const isCodex = quotaKey === "codex" || credential.providerType === "codex";
    const busy = deletingId === credential.id || togglingId === credential.id || resettingLimitId === credential.id
      || clearingCooldownId === credential.id || quotaBulkBusy || bulkToggling;
    const showRank = (accountCountByProviderId[credential.providerId] || 0) > 1;
    return {
      credential,
      quotaKey,
      label: maskAccountText(connectionLabel, masked),
      secondary: secondary ? maskAccountText(secondary, masked) : null,
      plan: formatServicePlanLabel(quota?.plan) || quota?.plan?.trim() || undefined,
      health: healthById[credential.id] ?? "loading",
      quota,
      error: errors[credential.id] || undefined,
      loading: Boolean(loading[credential.id]),
      busy,
      live: activeCredentialIds.has(credential.id),
      rank: showRank ? rankById[credential.id] ?? null : null,
      canReorder: showRank && sortMode === "default",
      rows,
      meterRows,
      hiddenRows,
      stacked,
      usageLabel: formatProxyUsageLabel(proxyUsageById[credential.id] ?? EMPTY_USAGE),
      renewalDate: formatRenewalDate(quota?.renews_at),
      isCodex,
      resetCredits: getCodexResetCreditCount(quota),
      refreshing: Boolean(loading[credential.id]) || clearingCooldownId === credential.id,
      deleting: deletingId === credential.id,
      selected: selectedCredentialIds.has(credential.id),
      dragging: draggingCredentialId === credential.id,
      dragOver: dragOverCredentialId === credential.id,
    };
  };

  const handlers: QuotaAccountHandlers = {
    open: (credential) => setDetailCredential(credential),
    select: (credential, select) => toggleSelection([credential], select),
    setEnabled: (credential, enabled) => void setCredentialEnabled(credential, enabled),
    refresh: refreshCredential,
    consumeResetCredit: (credential) => setResetConfirmCredential(credential),
    viewResetCredits: (credential) => setResetCreditsCredential(credential),
    edit: (credential) => navigate(`/providers/${encodeURIComponent(credential.providerId)}`),
    remove: (credential) => setDeleteConfirmCredential(credential),
    hideRow: (quotaKey, row) => handleHideQuota(quotaKey, getQuotaVisibilityKey(row)),
    showRow: (quotaKey, row) => handleShowQuota(quotaKey, getQuotaVisibilityKey(row)),
    dragStart: (credential, event) => {
      event.stopPropagation();
      event.dataTransfer.effectAllowed = "move";
      event.dataTransfer.setData("text/plain", credential.id);
      setDraggingCredentialId(credential.id);
      setDragOverCredentialId(null);
    },
    dragEnd: () => {
      setDraggingCredentialId(null);
      setDragOverCredentialId(null);
    },
    dragOver: (credential, event) => {
      const source = credentials.find((item) => item.id === draggingCredentialId);
      if (!reorderingProviderId && source && source.providerId === credential.providerId && source.id !== credential.id) {
        event.preventDefault();
        event.dataTransfer.dropEffect = "move";
        setDragOverCredentialId(credential.id);
      }
    },
    drop: (credential, event) => void handleCredentialDrop(event, credential),
  };

  const hasActiveFilters = accountFilter !== "all" || providerFilter !== "all" || search.trim() !== "";
  const clearFilters = () => {
    setSearch("");
    updateQuotaFilters({ provider: "all", status: "all" });
  };

  if (eligible.length === 0) {
    return (
      <section className="cp-page">
        {accountsLoading ? (
          <EmptyPanel loading title={t("quota.loadingAccounts")} description={t("quota.loadingAccountsHint")} />
        ) : (
          <EmptyPanel
            icon="cloud_off"
            title={t("quota.noProvidersConnected")}
            description={t("quota.connectProviders")}
            action={
              <Button variant="primary" icon="add_link" onClick={() => navigate("/providers")}>
                {t("quota.connectProvidersAction")}
              </Button>
            }
          />
        )}
      </section>
    );
  }

  const toggleLocked = !quotaDataReady;
  const reorderLocked = Boolean(reorderingProviderId);
  const quickActionCount = (quotaPassReady ? depletedTargets.length + availableTargets.length : 0);
  const filterTile = (status: AccountFilter) => ({
    active: accountFilter === status,
    onClick: () => updateQuotaFilters({ status: accountFilter === status && status !== "all" ? "all" : status }),
    title: t(`quota.overview.${status}Hint`),
  });

  return (
    <section className="cp-page">
      <PageStats
        label={t("quota.accountStatistics")}
        items={[
          {
            key: "all",
            icon: "stacks",
            label: t("quota.overview.all"),
            value: overviewCounts.total,
            hint: overviewCounts.checking > 0 ? t("quota.overview.checking", { count: overviewCounts.checking }) : t("quota.overview.allHint"),
            ...filterTile("all"),
          },
          {
            key: "available",
            icon: "check_circle",
            tone: "success",
            label: t("quota.overview.available"),
            value: overviewCounts.available,
            hint: t("quota.overview.availableHint"),
            muted: overviewCounts.available === 0,
            ...filterTile("available"),
          },
          {
            key: "low",
            icon: "battery_2_bar",
            tone: "warning",
            label: t("quota.overview.low"),
            value: overviewCounts.low,
            hint: t("quota.overview.lowHint"),
            muted: overviewCounts.low === 0,
            ...filterTile("low"),
          },
          {
            key: "attention",
            icon: "report",
            tone: "danger",
            label: t("quota.overview.attention"),
            value: overviewCounts.attention,
            hint: t("quota.overview.attentionHint"),
            muted: overviewCounts.attention === 0,
            ...filterTile("attention"),
          },
          {
            key: "inactive",
            icon: "power_settings_new",
            tone: "muted",
            label: t("quota.overview.inactive"),
            value: overviewCounts.off,
            hint: t("quota.overview.inactiveHint"),
            ...filterTile("inactive"),
          },
          {
            key: "next-reset",
            icon: "update",
            label: t("quota.overview.nextReset"),
            value: nextReset ? nextReset.time : "—",
            hint: nextReset ? nextReset.label : t("quota.overview.nextResetNone"),
            title: nextReset?.label,
            mono: true,
          },
        ]}
      />

      <Toolbar>
        <ToolbarRow>
          <FilterTabs
            label={t("quota.filterByProvider")}
            value={providerFilter}
            onChange={(provider) => updateQuotaFilters({ provider: provider === providerFilter ? "all" : provider })}
            items={[
              { value: "all", icon: "apps", label: t("quota.allProviders"), count: eligible.length },
              ...providerOptions.map((providerType) => {
                const info = getProviderTypeInfo(providerType);
                return {
                  value: providerType,
                  label: info.name,
                  count: providerCounts[providerType] || 0,
                  logo: <ProviderLogo className="cp-tab-logo" providerType={providerType} style={{ color: info.color }} />,
                };
              }),
            ]}
          />
          <ToolbarEnd>
            <AutoRefreshToggle
              enabled={autoRefresh}
              nextAt={nextRefreshAt}
              title={t("quota.autoRefreshHint")}
              onToggle={() => {
                setNextRefreshAt(Date.now() + REFRESH_INTERVAL_MS);
                setAutoRefresh((value) => !value);
              }}
            />
            <Button
              variant="primary"
              size="sm"
              icon="refresh"
              loading={refreshingAll}
              onClick={() => void refreshAll()}
              title={t("quota.refreshAllHint")}
            >
              {t("quota.refreshAll")}
            </Button>
          </ToolbarEnd>
        </ToolbarRow>

        <ToolbarRow>
          <SearchField value={search} onChange={setSearch} placeholder={t("quota.searchPlaceholder")} />
          <ToolbarEnd>
            <SelectField
              label={t("quota.sortBy")}
              value={sortMode}
              onChange={(sort) => updateQuotaFilters({ sort })}
              options={[
                { value: "default", label: t("quota.sort.default") },
                { value: "expiring", label: t("quota.sort.expiring") },
                { value: "lowest", label: t("quota.sort.lowest") },
              ]}
            />
            <Segmented
              label={t("quota.viewMode")}
              value={viewMode}
              onChange={setViewMode}
              items={[
                { value: "grid", icon: "grid_view", label: t("quota.viewGrid") },
                { value: "list", icon: "view_list", label: t("quota.viewList") },
              ]}
            />
            <ActionMenu
              label={t("quota.quickActions")}
              text={t("quota.quickActions")}
              icon="auto_fix_high"
              badge={quickActionCount || undefined}
              items={[
                {
                  key: "disable-depleted",
                  icon: "block",
                  label: t("quota.turnOffEmpty"),
                  hint: t("quota.turnOffEmptyHint", { count: depletedTargets.length }),
                  disabled: bulkToggling || toggleLocked || depletedTargets.length === 0,
                  onSelect: () => void bulkSetEnabled(depletedTargets, false),
                },
                {
                  key: "enable-available",
                  icon: "check_circle",
                  label: t("quota.turnOnAvailable"),
                  hint: t("quota.turnOnAvailableHint", { count: availableTargets.length }),
                  disabled: bulkToggling || toggleLocked || availableTargets.length === 0,
                  onSelect: () => void bulkSetEnabled(availableTargets, true),
                },
                {
                  key: "select-visible",
                  icon: "select_all",
                  label: t("quota.selectVisibleCount", { count: sortedCredentials.length }),
                  hint: t("quota.selectVisibleHint"),
                  disabled: sortedCredentials.length === 0 || allVisibleSelected,
                  onSelect: () => toggleSelection(sortedCredentials, true),
                },
              ]}
            />
            <GuideToggle open={guide.open} onToggle={guide.toggle} />
          </ToolbarEnd>
        </ToolbarRow>

        {!quotaPassReady && loadOrderCredentials.length > 0 ? (
          <ProgressLine value={quotaPassSettledCount} total={loadOrderCredentials.length}>
            {t("quota.loadingQuotaProgress", { current: quotaPassSettledCount, total: loadOrderCredentials.length })}
            {" · "}
            {t("quota.toggleLockedShort")}
          </ProgressLine>
        ) : null}
      </Toolbar>

      <PageGuide
        open={guide.open}
        onDismiss={guide.dismiss}
        title={t("quota.guide.title")}
        steps={[
          { icon: "account_circle", text: t("quota.guide.cards") },
          { icon: "low_priority", text: t("quota.guide.routing") },
          { icon: "bolt", text: t("quota.guide.actions") },
        ]}
        legend={
          <>
            <LegendItem tone="success">{t("quota.guide.legendHigh")}</LegendItem>
            <LegendItem tone="warning">{t("quota.guide.legendMid")}</LegendItem>
            <LegendItem tone="danger">{t("quota.guide.legendLow")}</LegendItem>
          </>
        }
      />

      {sortMode !== "default" && groups.some((group) => group.items.length > 1) ? (
        <PageNote action={<LinkButton onClick={() => updateQuotaFilters({ sort: "default" })}>{t("quota.sortBackToRouting")}</LinkButton>}>
          {t("quota.sortNote")}
        </PageNote>
      ) : null}

      {groups.length === 0 ? (
        <EmptyPanel
          icon="filter_alt_off"
          title={t("quota.noAccountsMatch")}
          description={t("quota.tryChangingFilter")}
          action={hasActiveFilters ? <Button variant="secondary" icon="filter_alt_off" onClick={clearFilters}>{t("quota.clearFilters")}</Button> : null}
        />
      ) : (
        <div className="qt-groups">
          {groups.map((group) => {
            const info = getProviderTypeInfo(group.quotaKey);
            const total = accountCountByProviderId[group.providerId] || group.items.length;
            const enabledCount = eligible.filter((item) => item.providerId === group.providerId && item.enabled).length;
            const groupSelected = group.items.every((item) => selectedCredentialIds.has(item.id));
            const groupPartlySelected = !groupSelected && group.items.some((item) => selectedCredentialIds.has(item.id));
            const buckets = { available: 0, low: 0, attention: 0 };
            for (const item of group.items) {
              const bucket = accountHealthBucket(healthById[item.id]);
              if (bucket && bucket !== "off") buckets[bucket] += 1;
            }
            const views = group.items.map(buildView);
            return (
              <section key={group.providerId} className="qt-group" aria-label={info.name}>
                <SectionHead
                  leading={
                    <>
                      <input
                        type="checkbox"
                        className="cp-check"
                        checked={groupSelected}
                        ref={(element) => {
                          if (element) element.indeterminate = groupPartlySelected;
                        }}
                        onChange={(event) => toggleSelection(group.items, event.target.checked)}
                        aria-label={t("quota.group.select", { name: info.name })}
                      />
                      <ProviderLogo className="cp-section-logo" providerType={group.quotaKey} style={{ color: info.color }} />
                    </>
                  }
                  title={info.name}
                  titleExtra={group.providerId !== group.quotaKey ? <code>{group.providerId}</code> : null}
                  subtitle={
                    group.items.length === total
                      ? t("quota.group.summary", { count: total, on: enabledCount })
                      : t("quota.group.summaryFiltered", { shown: group.items.length, count: total, on: enabledCount })
                  }
                  meta={
                    <ToneCounts
                      label={t("quota.group.health")}
                      items={[
                        { tone: "success", count: buckets.available, label: t("quota.overview.available") },
                        { tone: "warning", count: buckets.low, label: t("quota.overview.low") },
                        { tone: "danger", count: buckets.attention, label: t("quota.overview.attention") },
                      ]}
                    />
                  }
                  actions={
                    <ToolButton
                      icon="refresh"
                      disabled={quotaBulkBusy || refreshingAll}
                      onClick={() => void runBulkQuotaAction("refresh", group.items)}
                      title={t("quota.group.refreshHint", { name: info.name })}
                    >
                      {t("quota.group.refresh")}
                    </ToolButton>
                  }
                />

                {viewMode === "grid" ? (
                  <div className="cp-grid" style={{ ["--cp-card-min" as string]: "330px" }}>
                    {views.map((view) => (
                      <QuotaAccountCard
                        key={view.credential.id}
                        view={view}
                        handlers={handlers}
                        toggleLocked={toggleLocked}
                        reorderLocked={reorderLocked}
                      />
                    ))}
                  </div>
                ) : (
                  <div className="cp-list qt-list">
                    <div className="cp-list-head" aria-hidden>
                      <span />
                      <span>#</span>
                      <span>{t("quota.columns.account")}</span>
                      <span>{t("quota.columns.status")}</span>
                      <span>{t("quota.columns.quota")}</span>
                      <span>{t("quota.columns.usage")}</span>
                      <span>{t("quota.columns.routing")}</span>
                      <span />
                    </div>
                    {views.map((view) => (
                      <QuotaAccountRow
                        key={view.credential.id}
                        view={view}
                        handlers={handlers}
                        toggleLocked={toggleLocked}
                        reorderLocked={reorderLocked}
                      />
                    ))}
                  </div>
                )}
              </section>
            );
          })}
        </div>
      )}

      <BulkBar
        count={selectedCredentials.length}
        busy={quotaBulkBusy || bulkToggling}
        onClear={() => setSelectedCredentialIds(new Set())}
        selectAll={
          allVisibleSelected
            ? null
            : { label: t("quota.bulk.selectAllVisible", { count: sortedCredentials.length }), onSelect: () => toggleSelection(sortedCredentials, true) }
        }
      >
        <BulkButton icon="refresh" spinning={quotaBulkBusy} disabled={quotaBulkBusy || bulkToggling} onClick={() => void runBulkQuotaAction("refresh")}>
          {t("quota.bulk.refresh")}
        </BulkButton>
        <BulkButton icon="restart_alt" disabled={quotaBulkBusy || bulkToggling} onClick={() => void runBulkQuotaAction("cooldown")}>
          {t("quota.clearCooldownSelected")}
        </BulkButton>
        <BulkButton
          icon="toggle_on"
          disabled={quotaBulkBusy || bulkToggling || toggleLocked}
          onClick={() => void bulkSetEnabled(selectedCredentials.filter((item) => !item.enabled), true)}
        >
          {t("quota.bulk.enable")}
        </BulkButton>
        <BulkButton
          icon="toggle_off"
          disabled={quotaBulkBusy || bulkToggling || toggleLocked}
          onClick={() => void bulkSetEnabled(selectedCredentials.filter((item) => item.enabled), false)}
        >
          {t("quota.bulk.disable")}
        </BulkButton>
        {selectedCodexResetCredentials.length > 0 ? (
          <BulkButton icon="bolt" tone="warning" disabled={quotaBulkBusy || bulkToggling} onClick={() => setBulkResetConfirmOpen(true)}>
            {t("quota.resetCodexSelected", { count: selectedCodexResetCredentials.length })}
          </BulkButton>
        ) : null}
      </BulkBar>

      <ConfirmDialog
        open={Boolean(deleteConfirmCredential)}
        title={t("quota.deleteConnection")}
        message={
          deleteConfirmCredential
            ? t("quota.deleteConfirmMessage", {
                label: maskAccountText(getConnectionLabel(deleteConfirmCredential) || deleteConfirmCredential.id, masked),
              })
            : ""
        }
        confirmText={t("quota.deleteConnection")}
        cancelText={t("common.cancel")}
        variant="danger"
        onClose={() => setDeleteConfirmCredential(null)}
        onConfirm={() => {
          if (deleteConfirmCredential) void handleDelete(deleteConfirmCredential);
        }}
      />

      <ConfirmDialog
        open={Boolean(resetConfirmCredential)}
        title={t("quota.resetCodexLimit")}
        message={
          resetConfirmCredential
            ? t("quota.resetCodexConfirm", {
                label: maskAccountText(getConnectionLabel(resetConfirmCredential) || resetConfirmCredential.email || resetConfirmCredential.id, masked),
                count: getCodexResetCreditCount(quotaById[resetConfirmCredential.id]),
              })
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

      <ConfirmDialog
        open={bulkResetConfirmOpen}
        title={t("quota.resetCodexLimit")}
        message={t("quota.bulkResetConfirm", {
          count: selectedCodexResetCredentials.length,
          credits: selectedCodexResetCredentials.reduce((sum, credential) => sum + getCodexResetCreditCount(quotaById[credential.id]), 0),
        })}
        confirmText={t("quota.resetLimit")}
        cancelText={t("common.cancel")}
        variant="danger"
        onClose={() => {
          if (!quotaBulkBusy) setBulkResetConfirmOpen(false);
        }}
        onConfirm={() => void runBulkQuotaAction("codex-reset", selectedCodexResetCredentials)}
      />

      <Modal
        open={Boolean(bulkResults)}
        onClose={() => setBulkResults(null)}
        title={bulkResults?.title || t("quota.bulkActionResults")}
        subtitle={
          bulkResults
            ? t("quota.bulk.resultSummary", {
                ok: bulkResults.items.filter((item) => item.ok).length,
                failed: bulkResults.items.filter((item) => !item.ok).length,
              })
            : undefined
        }
        size="md"
        footer={<Button variant="secondary" onClick={() => setBulkResults(null)}>{t("common.close")}</Button>}
      >
        <div className="quota-bulk-results">
          {bulkResults
            ? [...bulkResults.items]
                .sort((a, b) => Number(a.ok) - Number(b.ok))
                .map((item) => (
                  <div key={item.id} className={cn("quota-bulk-result", item.ok ? "quota-bulk-result-ok" : "quota-bulk-result-error")}>
                    <span className="material-symbols-outlined" aria-hidden="true">{item.ok ? "check_circle" : "error"}</span>
                    <div>
                      <strong>{item.label}</strong>
                      <span>{item.message}</span>
                    </div>
                  </div>
                ))
            : null}
        </div>
      </Modal>

      <CodexResetCreditsModal
        open={Boolean(resetCreditsCredential)}
        secret={secret}
        credential={resetCreditsCredential}
        onClose={() => setResetCreditsCredential(null)}
        onUse={() => {
          if (resetCreditsCredential) setResetConfirmCredential(resetCreditsCredential);
        }}
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
        onClearCooldown={() => {
          if (selectedCredential) refreshCredential(selectedCredential);
        }}
        clearingCooldown={selectedCredential ? clearingCooldownId === selectedCredential.id : false}
        resetCreditCount={selectedCredential ? getCodexResetCreditCount(quotaById[selectedCredential.id]) : 0}
        resettingQuota={selectedCredential ? resettingLimitId === selectedCredential.id : false}
        onResetQuota={() => {
          if (selectedCredential) setResetConfirmCredential(selectedCredential);
        }}
        onQuotaUpdated={(quota) => {
          if (!selectedCredential) return;
          setQuotaById((current) => ({ ...current, [selectedCredential.id]: quota }));
        }}
        clock={clock}
      />
    </section>
  );
}
