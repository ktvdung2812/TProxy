import { useEffect, useMemo, useRef, useState, type DragEvent } from "react";
import { useTranslation } from "react-i18next";
import { Button, ConfirmDialog } from "../ui";
import {
  ActionMenu,
  BulkBar,
  BulkButton,
  EmptyPanel,
  GuideToggle,
  LegendItem,
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
  usePageGuide,
  useStoredState,
} from "../page-kit";
import { fetchCredentialProxyUsage, type CredentialProxyUsage } from "../quota/api";
import { formatProxyUsageLabel } from "../quota/utils";
import { maskAccountText } from "../../lib/accountMask";
import { useAccountMask } from "../../hooks/useAccountMask";
import {
  checkCredentialHealth,
  clearCredentialCooldown,
  deleteCredential,
  exportSub2apiAccounts,
  refreshCredential,
  reorderProviderCredentials,
  saveCredential,
  type NinerouterPreset,
} from "./api";
import type { ConnectionMethod, ConnectionProfile } from "./connectionMethods";
import { profileNotice } from "./connectionText";
import { ProviderConnectionActions } from "./ProviderConnectionActions";
import { AccountCard, AccountRow, type AccountHandlers, type AccountView } from "./ProviderAccountItem";
import { accountState, nextCooldown, timeLeft, type AccountState } from "./providerHealth";
import { providerSupportsUpstreamQuota, useAccountQuotas } from "./useAccountQuotas";
import {
  buildCredentialAccountNumbers,
  compareCredentialsByPriority,
  formatCredentialAddedAt,
  isOnCooldown,
  moveCredentialBefore,
  type Credential,
  type Provider,
} from "./types";

type Props = {
  provider: Provider;
  credentials: Credential[];
  secret: string;
  presets: NinerouterPreset[];
  connectionProfile: ConnectionProfile;
  onMethod: (method: ConnectionMethod) => void;
  onEdit: (credential: Credential) => void;
  onModels: (credential: Credential) => void;
  onReAuth: (credential: Credential) => void;
  onMutated: () => void;
  onNotice: (message: string) => void;
  onError: (message: string) => void;
};

type StateFilter = "all" | "ready" | "cooldown" | "attention" | "off";
type ViewMode = "grid" | "list";
const VIEW_MODES = ["grid", "list"] as const;

const NEEDS_ATTENTION: AccountState[] = ["attention", "degraded"];

function matchesState(filter: StateFilter, state: AccountState) {
  if (filter === "all") return true;
  if (filter === "attention") return NEEDS_ATTENTION.includes(state);
  return state === filter;
}

function hasErrorSignal(credential: Credential) {
  return Boolean(
    credential.last_error_code?.trim() ||
      credential.last_error?.trim() ||
      credential.status === "auth_required" ||
      credential.status === "cooldown" ||
      isOnCooldown(credential.cooldown_until),
  );
}

function downloadBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  anchor.click();
  URL.revokeObjectURL(url);
}

/** Accounts of one provider: health at a glance, routing order, and bulk operations. */
export function ProviderAccounts({
  provider,
  credentials,
  secret,
  presets,
  connectionProfile,
  onMethod,
  onEdit,
  onModels,
  onReAuth,
  onMutated,
  onNotice,
  onError,
}: Props) {
  const { t } = useTranslation();
  const { masked } = useAccountMask();
  const guide = usePageGuide("provider-detail");
  const [viewMode, setViewMode] = useStoredState<ViewMode>("tproxy.providerAccounts.view", "grid", VIEW_MODES);
  const [query, setQuery] = useState("");
  const [errorCodeFilter, setErrorCodeFilter] = useState("");
  const [stateFilter, setStateFilter] = useState<StateFilter>("all");
  const [refreshOverrides, setRefreshOverrides] = useState<Record<string, { status?: string; last_validated_at?: string }>>({});
  const [credentialOrder, setCredentialOrder] = useState<string[] | null>(null);
  const [draggingId, setDraggingId] = useState<string | null>(null);
  const [dragOverId, setDragOverId] = useState<string | null>(null);
  const [reordering, setReordering] = useState(false);
  const [selectedIds, setSelectedIds] = useState<string[]>([]);
  const [busyById, setBusyById] = useState<Record<string, string>>({});
  const [bulkBusy, setBulkBusy] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<Credential | null>(null);
  const [confirmBulkDelete, setConfirmBulkDelete] = useState(false);
  const [usageById, setUsageById] = useState<Record<string, CredentialProxyUsage>>({});
  const [now, setNow] = useState(() => Date.now());
  const lastTickRef = useRef(now);

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 30000);
    return () => window.clearInterval(timer);
  }, []);

  // Reset local ordering and drop state for accounts that no longer exist.
  useEffect(() => {
    setCredentialOrder(null);
    const available = new Set(credentials.map((credential) => credential.id));
    setRefreshOverrides((current) => {
      const next = Object.fromEntries(Object.entries(current).filter(([id]) => available.has(id)));
      return Object.keys(next).length === Object.keys(current).length ? current : next;
    });
    setSelectedIds((current) => {
      const next = current.filter((id) => available.has(id));
      return next.length === current.length ? current : next;
    });
  }, [credentials]);

  // A cooldown that ends while the page is open reloads the snapshot once,
  // so the account flips back to available without a manual refresh.
  useEffect(() => {
    const previous = lastTickRef.current;
    lastTickRef.current = now;
    const ended = credentials.some((credential) => {
      const until = credential.cooldown_until ? Date.parse(credential.cooldown_until) : NaN;
      return Number.isFinite(until) && until > previous && until <= now;
    });
    if (ended) onMutated();
  }, [now, credentials, onMutated]);

  useEffect(() => {
    let cancelled = false;
    fetchCredentialProxyUsage(secret, "today")
      .then((result) => {
        if (!cancelled) setUsageById(result.by_credential || {});
      })
      .catch(() => {
        if (!cancelled) setUsageById({});
      });
    return () => {
      cancelled = true;
    };
  }, [secret, credentials]);

  const displayed = useMemo(
    () => credentials.map((credential) => (refreshOverrides[credential.id] ? { ...credential, ...refreshOverrides[credential.id] } : credential)),
    [credentials, refreshOverrides],
  );

  const ordered = useMemo(() => {
    const priorityOrder = [...displayed].sort(compareCredentialsByPriority);
    if (!credentialOrder || credentialOrder.length !== priorityOrder.length) return priorityOrder;
    const byId = new Map(priorityOrder.map((credential) => [credential.id, credential]));
    if (credentialOrder.some((id) => !byId.has(id))) return priorityOrder;
    return credentialOrder.map((id) => byId.get(id)!).filter(Boolean);
  }, [displayed, credentialOrder]);

  const accountNumbers = useMemo(() => buildCredentialAccountNumbers(ordered), [ordered]);
  const orderedIds = useMemo(() => ordered.map((credential) => credential.id), [ordered]);
  const idsKey = useMemo(() => credentials.map((credential) => credential.id).slice().sort().join("\u0000"), [credentials]);
  const supportsQuota = useMemo(() => providerSupportsUpstreamQuota(provider.ID, presets), [provider.ID, presets]);
  const quotas = useAccountQuotas({ secret, providerId: provider.ID, supported: supportsQuota, orderedIds, idsKey, masked, onError, onNotice });

  const states = useMemo(() => {
    const result: Record<string, AccountState> = {};
    for (const credential of ordered) result[credential.id] = accountState(credential);
    return result;
    // now re-evaluates cooldowns that expire while the page is open.
  }, [ordered, now]);

  const errorCodeOptions = useMemo(() => {
    const counts = new Map<string, number>();
    for (const credential of ordered) {
      const code = credential.last_error_code?.trim();
      if (code) counts.set(code, (counts.get(code) || 0) + 1);
    }
    return [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([code, count]) => ({ code, count }));
  }, [ordered]);
  const erroredWithoutCode = useMemo(
    () => ordered.filter((credential) => !credential.last_error_code?.trim() && hasErrorSignal(credential)).length,
    [ordered],
  );

  useEffect(() => {
    if (!errorCodeFilter || errorCodeFilter === "__any__") return;
    if (errorCodeFilter === "__none__") {
      if (erroredWithoutCode === 0) setErrorCodeFilter("");
      return;
    }
    if (!errorCodeOptions.some((option) => option.code === errorCodeFilter)) setErrorCodeFilter("");
  }, [errorCodeFilter, errorCodeOptions, erroredWithoutCode]);

  // Search and error-code scope; the stat tiles count within it, so clicking a
  // tile never changes the numbers on the other tiles.
  const scoped = useMemo(() => {
    const text = query.trim().toLowerCase();
    return ordered.filter((credential) => {
      const code = credential.last_error_code?.trim() || "";
      const hasError = Boolean(code) || hasErrorSignal(credential);
      if (errorCodeFilter === "__any__") {
        if (!hasError) return false;
      } else if (errorCodeFilter === "__none__") {
        if (!hasError || code) return false;
      } else if (errorCodeFilter && code !== errorCodeFilter) {
        return false;
      }
      if (!text) return true;
      return [credential.id, credential.label, credential.email, credential.auth_type, credential.status, credential.last_error_code, credential.last_error]
        .filter(Boolean)
        .join(" ")
        .toLowerCase()
        .includes(text);
    });
  }, [ordered, query, errorCodeFilter]);

  const visible = useMemo(() => scoped.filter((credential) => matchesState(stateFilter, states[credential.id])), [scoped, stateFilter, states]);

  const counts = useMemo(() => {
    const result = { all: scoped.length, ready: 0, cooldown: 0, attention: 0, off: 0, on: 0 };
    for (const credential of scoped) {
      const state = states[credential.id];
      if (state === "ready") result.ready += 1;
      if (state === "cooldown") result.cooldown += 1;
      if (NEEDS_ATTENTION.includes(state)) result.attention += 1;
      if (state === "off") result.off += 1;
      if (credential.enabled) result.on += 1;
    }
    return result;
  }, [scoped, states]);

  const usageTotal = useMemo(() => {
    const total = { requests: 0, promptTokens: 0, completionTokens: 0 };
    for (const credential of credentials) {
      const usage = usageById[credential.id];
      if (!usage) continue;
      total.requests += usage.requests || 0;
      total.promptTokens += usage.promptTokens || 0;
      total.completionTokens += usage.completionTokens || 0;
    }
    return total;
  }, [credentials, usageById]);

  const selected = useMemo(() => ordered.filter((credential) => selectedIds.includes(credential.id)), [ordered, selectedIds]);
  const allVisibleSelected = visible.length > 0 && visible.every((credential) => selectedIds.includes(credential.id));
  const someVisibleSelected = !allVisibleSelected && visible.some((credential) => selectedIds.includes(credential.id));

  const setBusy = (id: string, action: string | null) =>
    setBusyById((current) => {
      const next = { ...current };
      if (action) next[id] = action;
      else delete next[id];
      return next;
    });

  const nameOf = (credential: Credential) => maskAccountText(credential.email || credential.label || credential.id, masked);

  const saveEnabled = (credential: Credential, enabled: boolean) =>
    saveCredential(secret, {
      provider_id: provider.ID,
      credential: {
        id: credential.id,
        label: credential.label,
        email: credential.email,
        auth_type: credential.auth_type as "api_key" | "oauth" | "service_account" | "none",
        priority: credential.priority ?? 0,
        weight: credential.weight ?? 1,
        enabled,
        proxy_pools: credential.proxy_pool_ids,
      },
    });

  const setEnabled = async (credential: Credential, enabled: boolean) => {
    setBusy(credential.id, "toggle");
    try {
      await saveEnabled(credential, enabled);
      onNotice(t(enabled ? "providers.credentialEnabled" : "providers.credentialDisabled", { id: nameOf(credential) }));
      onMutated();
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("providers.toggleFailed"));
    } finally {
      setBusy(credential.id, null);
    }
  };

  const runHealth = async (credential: Credential) => {
    setBusy(credential.id, "health");
    try {
      const result = await checkCredentialHealth(secret, credential.id);
      const status = result.status || (result.ok ? "healthy" : "failed");
      onNotice(
        result.ok
          ? t("providers.credentialHealthOk", { id: nameOf(credential), status })
          : `${t("providers.credentialHealthFailed", { id: nameOf(credential), status })}${result.last_error ? t("providers.providerHealthSummarySuffix", { error: result.last_error }) : ""}`,
      );
      onMutated();
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("providers.healthCheckFailed"));
    } finally {
      setBusy(credential.id, null);
    }
  };

  const runRefreshToken = async (credential: Credential) => {
    setBusy(credential.id, "token");
    try {
      const result = await refreshCredential(secret, credential.id);
      setRefreshOverrides((current) => ({
        ...current,
        [credential.id]: { status: result.status?.status || "healthy", last_validated_at: new Date().toISOString() },
      }));
      onNotice(t("providers.tokenRefreshed", { id: nameOf(credential) }));
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("providers.tokenRefreshFailed"));
    } finally {
      setBusy(credential.id, null);
    }
  };

  const runClearCooldown = async (credential: Credential) => {
    setBusy(credential.id, "cooldown");
    try {
      await clearCredentialCooldown(secret, credential.id);
      onNotice(t("providers.cooldownCleared", { id: nameOf(credential) }));
      onMutated();
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("providers.cooldownClearFailed"));
    } finally {
      setBusy(credential.id, null);
    }
  };

  const exportAccounts = async (ids: string[]) => {
    const unique = [...new Set(ids.map((id) => id.trim()).filter(Boolean))];
    if (unique.length === 0) return;
    setBulkBusy("export");
    try {
      const blob = await exportSub2apiAccounts(secret, unique);
      const stamp = new Date().toISOString().replace(/[:.]/g, "-").slice(0, 19);
      downloadBlob(blob, `sub2api-accounts-${stamp}.json`);
      onNotice(t("providers.account.exported", { count: unique.length }));
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("providers.sub2apiExportFailed"));
    } finally {
      setBulkBusy(null);
    }
  };

  const deleteOne = async () => {
    if (!confirmDelete) return;
    const credential = confirmDelete;
    setConfirmDelete(null);
    setBusy(credential.id, "delete");
    try {
      await deleteCredential(secret, credential.id);
      setSelectedIds((current) => current.filter((id) => id !== credential.id));
      onNotice(t("providers.credentialDeleted", { id: nameOf(credential) }));
      onMutated();
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("providers.deleteFailed"));
    } finally {
      setBusy(credential.id, null);
    }
  };

  const bulkDelete = async () => {
    const ids = [...selectedIds];
    if (ids.length === 0) return;
    setConfirmBulkDelete(false);
    setBulkBusy("delete");
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
      setSelectedIds([]);
      if (deleted > 0) onMutated();
      if (failures.length === 0) onNotice(t("providers.account.bulkDeleted", { count: deleted }));
      else onError(t("providers.bulkDeletePartial", { deleted, total: ids.length, error: failures[0] }));
    } finally {
      setBulkBusy(null);
    }
  };

  const bulkSetEnabled = async (enabled: boolean) => {
    const targets = selected.filter((credential) => credential.enabled !== enabled);
    if (targets.length === 0) return;
    setBulkBusy(enabled ? "enable" : "disable");
    let done = 0;
    const failures: string[] = [];
    try {
      for (const credential of targets) {
        try {
          await saveEnabled(credential, enabled);
          done += 1;
        } catch (cause) {
          failures.push(cause instanceof Error ? cause.message : credential.id);
        }
      }
      if (done > 0) onMutated();
      if (failures.length === 0) onNotice(t(enabled ? "providers.account.bulkEnabled" : "providers.account.bulkDisabled", { count: done }));
      else onError(t("providers.account.bulkPartial", { done, total: targets.length, error: failures[0] }));
    } finally {
      setBulkBusy(null);
    }
  };

  const bulkHealth = async () => {
    if (selected.length === 0) return;
    setBulkBusy("health");
    let healthy = 0;
    let failed = 0;
    try {
      for (const credential of selected) {
        try {
          const result = await checkCredentialHealth(secret, credential.id);
          if (result.ok) healthy += 1;
          else failed += 1;
        } catch {
          failed += 1;
        }
      }
      onNotice(t("providers.healthCheckFinished", { ok: healthy, failed }));
      onMutated();
    } finally {
      setBulkBusy(null);
    }
  };

  const toggleSelection = (targets: Credential[], select: boolean) => {
    setSelectedIds((current) => {
      const next = new Set(current);
      for (const credential of targets) {
        if (select) next.add(credential.id);
        else next.delete(credential.id);
      }
      return [...next];
    });
  };

  const handleDrop = async (event: DragEvent<HTMLElement>, target: Credential) => {
    event.preventDefault();
    const sourceId = event.dataTransfer.getData("text/plain") || draggingId;
    setDraggingId(null);
    setDragOverId(null);
    if (!sourceId || sourceId === target.id || reordering) return;
    const next = moveCredentialBefore(ordered, sourceId, target.id);
    if (next === ordered) return;
    const nextIds = next.map((credential) => credential.id);
    setCredentialOrder(nextIds);
    setReordering(true);
    try {
      await reorderProviderCredentials(secret, provider.ID, nextIds);
      onNotice(t("providers.account.orderSaved"));
      onMutated();
    } catch (cause) {
      setCredentialOrder(null);
      onError(cause instanceof Error ? cause.message : t("providers.account.orderFailed"));
    } finally {
      setReordering(false);
    }
  };

  const oauthReady = connectionProfile.methods.some((method) => method.kind === "oauth" && method.available);
  const rankById = new Map(orderedIds.map((id, index) => [id, index + 1]));

  const buildView = (credential: Credential): AccountView => {
    const state = states[credential.id] ?? accountState(credential);
    const primary = credential.email || credential.label || credential.id;
    const secondary = credential.label && credential.email && credential.label !== credential.email ? credential.label : null;
    return {
      credential,
      label: maskAccountText(primary, masked),
      secondary: secondary ? maskAccountText(secondary, masked) : null,
      number: accountNumbers.get(credential.id),
      addedAt: formatCredentialAddedAt(credential.created_at),
      state,
      rank: rankById.get(credential.id) ?? 0,
      canReorder: ordered.length > 1,
      supportsQuota,
      quota: quotas.quotaById[credential.id],
      quotaBusy: Boolean(quotas.busyById[credential.id]),
      usageLabel: formatProxyUsageLabel(usageById[credential.id]),
      busy: busyById[credential.id] ?? (bulkBusy && selectedIds.includes(credential.id) ? bulkBusy : null),
      selected: selectedIds.includes(credential.id),
      dragging: draggingId === credential.id,
      dragOver: dragOverId === credential.id,
      canReAuth: credential.status === "auth_required" && credential.auth_type === "oauth" && oauthReady,
    };
  };

  const handlers: AccountHandlers = {
    edit: onEdit,
    select: (credential, value) => toggleSelection([credential], value),
    setEnabled: (credential, enabled) => void setEnabled(credential, enabled),
    health: (credential) => void runHealth(credential),
    refreshQuota: (credential) => void quotas.refresh(credential.id),
    refreshToken: (credential) => void runRefreshToken(credential),
    clearCooldown: (credential) => void runClearCooldown(credential),
    reAuth: onReAuth,
    models: onModels,
    exportOne: (credential) => void exportAccounts([credential.id]),
    remove: setConfirmDelete,
    dragStart: (credential, event) => {
      event.stopPropagation();
      if (reordering) {
        event.preventDefault();
        return;
      }
      event.dataTransfer.effectAllowed = "move";
      event.dataTransfer.setData("text/plain", credential.id);
      setDraggingId(credential.id);
      setDragOverId(null);
    },
    dragEnd: () => {
      setDraggingId(null);
      setDragOverId(null);
    },
    dragOver: (credential, event) => {
      if (!reordering && draggingId && draggingId !== credential.id) {
        event.preventDefault();
        event.dataTransfer.dropEffect = "move";
        setDragOverId(credential.id);
      }
    },
    drop: (credential, event) => void handleDrop(event, credential),
  };

  const notice = profileNotice(connectionProfile, t);

  if (credentials.length === 0) {
    return (
      <section className="cp-panel pv-panel pv-connect-panel" aria-label={t("providers.connections")}>
        <SectionHead
          leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>vpn_key</span>}
          title={t("providers.account.connectTitle")}
          subtitle={connectionProfile.noAuth ? t("providers.noAuthHint") : t("providers.account.connectHint")}
        />
        <ProviderConnectionActions profile={connectionProfile} onMethod={onMethod} placement="empty" size="md" />
      </section>
    );
  }

  const filterTile = (filter: StateFilter) => ({
    active: stateFilter === filter,
    onClick: () => setStateFilter(stateFilter === filter && filter !== "all" ? "all" : filter),
  });
  const cooldownLeft = timeLeft(nextCooldown(scoped) ?? undefined, now);
  const hasFilters = query.trim() !== "" || errorCodeFilter !== "" || stateFilter !== "all";
  const clearFilters = () => {
    setQuery("");
    setErrorCodeFilter("");
    setStateFilter("all");
  };
  const usageLabel = formatProxyUsageLabel(usageTotal);
  const reorderLocked = reordering;

  return (
    <>
      <PageStats
        label={t("providers.account.statsLabel")}
        items={[
          {
            key: "all",
            icon: "group",
            label: t("providers.account.stats.all"),
            value: counts.all,
            hint: t("providers.account.stats.allHint", { on: counts.on, off: counts.all - counts.on }),
            ...filterTile("all"),
          },
          {
            key: "ready",
            icon: "check_circle",
            tone: "success",
            label: t("providers.account.stats.ready"),
            value: counts.ready,
            hint: t("providers.account.stats.readyHint"),
            muted: counts.ready === 0,
            ...filterTile("ready"),
          },
          {
            key: "cooldown",
            icon: "timer",
            tone: "warning",
            label: t("providers.account.stats.cooldown"),
            value: counts.cooldown,
            hint: cooldownLeft ? t("providers.stats.cooldownNext", { time: cooldownLeft }) : t("providers.account.stats.cooldownHint"),
            muted: counts.cooldown === 0,
            ...filterTile("cooldown"),
          },
          {
            key: "attention",
            icon: "report",
            tone: "danger",
            label: t("providers.account.stats.attention"),
            value: counts.attention,
            hint: t("providers.account.stats.attentionHint"),
            muted: counts.attention === 0,
            ...filterTile("attention"),
          },
          {
            key: "off",
            icon: "power_settings_new",
            tone: "muted",
            label: t("providers.account.stats.off"),
            value: counts.off,
            hint: t("providers.account.stats.offHint"),
            ...filterTile("off"),
          },
          {
            key: "usage",
            icon: "swap_vert",
            label: t("providers.account.stats.usage"),
            // "1.6K req · 4.8M tok" does not fit a tile: requests are the value, tokens the hint.
            value: usageTotal.requests > 0 && usageLabel ? usageLabel.split(" · ")[0] : "—",
            hint: usageTotal.requests > 0 && usageLabel ? t("providers.account.stats.usageHint", { tokens: usageLabel.split(" · ")[1] ?? "" }) : t("providers.usageThroughTproxy"),
            title: t("providers.usageThroughTproxy"),
            mono: true,
            muted: usageTotal.requests === 0,
          },
        ]}
      />

      <Toolbar>
        <ToolbarRow>
          <SearchField value={query} onChange={setQuery} placeholder={t("providers.searchAccounts")} />
          <SelectField
            icon="filter_alt"
            label={t("providers.account.errorFilter")}
            value={errorCodeFilter}
            onChange={setErrorCodeFilter}
            options={[
              { value: "", label: t("providers.allErrorCodes") },
              { value: "__any__", label: t("providers.anyError") },
              ...(erroredWithoutCode > 0 ? [{ value: "__none__", label: t("providers.errorWithoutCode", { count: erroredWithoutCode }) }] : []),
              ...errorCodeOptions.map(({ code, count }) => ({ value: code, label: `${code} (${count})` })),
            ]}
          />
          <ToolbarEnd>
            <Segmented
              label={t("providers.list.viewMode")}
              value={viewMode}
              onChange={setViewMode}
              items={[
                { value: "grid", icon: "grid_view", label: t("providers.list.viewGrid") },
                { value: "list", icon: "view_list", label: t("providers.list.viewList") },
              ]}
            />
            <ActionMenu
              label={t("providers.list.quickActions")}
              text={t("providers.list.quickActions")}
              icon="auto_fix_high"
              items={[
                {
                  key: "select-visible",
                  icon: "select_all",
                  label: t("providers.account.selectVisible", { count: visible.length }),
                  disabled: visible.length === 0 || allVisibleSelected,
                  onSelect: () => toggleSelection(visible, true),
                },
                {
                  key: "select-attention",
                  icon: "report",
                  label: t("providers.account.selectAttention"),
                  hint: t("providers.account.selectAttentionHint", { count: counts.attention }),
                  disabled: counts.attention === 0,
                  onSelect: () => toggleSelection(scoped.filter((credential) => NEEDS_ATTENTION.includes(states[credential.id])), true),
                },
                {
                  key: "export-all",
                  icon: "download",
                  label: t("providers.account.exportAll", { count: ordered.length }),
                  hint: t("providers.account.exportHint"),
                  disabled: bulkBusy !== null,
                  onSelect: () => void exportAccounts(orderedIds),
                },
              ]}
            />
            <GuideToggle open={guide.open} onToggle={guide.toggle} />
            <ProviderConnectionActions profile={connectionProfile} onMethod={onMethod} placement="footer" />
          </ToolbarEnd>
        </ToolbarRow>
        {supportsQuota && !quotas.ready && orderedIds.length > 0 ? (
          <ProgressLine value={quotas.settledCount} total={orderedIds.length}>
            {t("quota.loadingQuotaProgress", { current: quotas.settledCount, total: orderedIds.length })}
          </ProgressLine>
        ) : null}
      </Toolbar>

      <PageGuide
        open={guide.open}
        onDismiss={guide.dismiss}
        title={t("providers.detailGuide.title")}
        steps={[
          { icon: "low_priority", text: t("providers.detailGuide.order") },
          { icon: "monitor_heart", text: t("providers.detailGuide.states") },
          { icon: "checklist", text: t("providers.detailGuide.bulk") },
        ]}
        legend={
          <>
            <LegendItem tone="success">{t("providers.accountState.ready")}</LegendItem>
            <LegendItem tone="warning">{t("providers.accountState.cooldown")}</LegendItem>
            <LegendItem tone="danger">{t("providers.accountState.attention")}</LegendItem>
            <LegendItem tone="muted">{t("providers.accountState.off")}</LegendItem>
          </>
        }
      />

      {notice ? <PageNote>{notice}</PageNote> : null}

      <section className="pv-group" aria-label={t("providers.connections")}>
        <SectionHead
          leading={
            <input
              type="checkbox"
              className="cp-check"
              checked={allVisibleSelected}
              disabled={visible.length === 0}
              ref={(element) => {
                if (element) element.indeterminate = someVisibleSelected;
              }}
              onChange={(event) => toggleSelection(visible, event.target.checked)}
              aria-label={t("providers.selectAllAccounts")}
            />
          }
          title={t("providers.connections")}
          subtitle={
            visible.length === ordered.length
              ? t("providers.account.summary", { count: ordered.length, on: ordered.filter((credential) => credential.enabled).length })
              : t("providers.account.summaryFiltered", { shown: visible.length, count: ordered.length })
          }
          meta={
            <ToneCounts
              label={t("providers.list.groupHealth")}
              items={[
                { tone: "success", count: counts.ready, label: t("providers.accountState.ready") },
                { tone: "warning", count: counts.cooldown, label: t("providers.accountState.cooldown") },
                { tone: "danger", count: counts.attention, label: t("providers.accountState.attention") },
              ]}
            />
          }
          actions={
            ordered.length > 1 ? (
              <span className="pv-order-hint" title={t("providers.account.orderHint")}>
                <span className="material-symbols-outlined" aria-hidden>drag_indicator</span>
                {t("providers.account.orderShort")}
              </span>
            ) : null
          }
        />

        {visible.length === 0 ? (
          <EmptyPanel
            compact
            icon="filter_alt_off"
            title={t("providers.noAccountsMatch")}
            description={t("providers.accountsFilterHint")}
            action={hasFilters ? <Button variant="secondary" icon="filter_alt_off" onClick={clearFilters}>{t("providers.list.clearFilters")}</Button> : null}
          />
        ) : viewMode === "grid" ? (
          <div className="cp-grid" style={{ ["--cp-card-min" as string]: "320px" }}>
            {visible.map((credential) => (
              <AccountCard key={credential.id} view={buildView(credential)} handlers={handlers} reorderLocked={reorderLocked} now={now} />
            ))}
          </div>
        ) : (
          <div className="cp-list pv-account-list">
            <div className="cp-list-head" aria-hidden>
              <span />
              <span>#</span>
              <span>{t("providers.columns.account")}</span>
              <span>{t("providers.columns.status")}</span>
              <span>{t("providers.columns.quota")}</span>
              <span>{t("providers.columns.today")}</span>
              <span>{t("providers.columns.on")}</span>
              <span />
            </div>
            {visible.map((credential) => (
              <AccountRow key={credential.id} view={buildView(credential)} handlers={handlers} reorderLocked={reorderLocked} now={now} />
            ))}
          </div>
        )}
      </section>

      <BulkBar
        count={selected.length}
        busy={bulkBusy !== null}
        onClear={() => setSelectedIds([])}
        selectAll={allVisibleSelected ? null : { label: t("providers.account.selectVisible", { count: visible.length }), onSelect: () => toggleSelection(visible, true) }}
      >
        <BulkButton icon="toggle_on" spinning={bulkBusy === "enable"} disabled={bulkBusy !== null} onClick={() => void bulkSetEnabled(true)}>
          {t("providers.account.bulkEnable")}
        </BulkButton>
        <BulkButton icon="toggle_off" spinning={bulkBusy === "disable"} disabled={bulkBusy !== null} onClick={() => void bulkSetEnabled(false)}>
          {t("providers.account.bulkDisable")}
        </BulkButton>
        <BulkButton icon="monitor_heart" spinning={bulkBusy === "health"} disabled={bulkBusy !== null} onClick={() => void bulkHealth()}>
          {t("providers.healthCheck")}
        </BulkButton>
        <BulkButton icon="download" spinning={bulkBusy === "export"} disabled={bulkBusy !== null} onClick={() => void exportAccounts(selectedIds)}>
          {t("providers.exportSelected")}
        </BulkButton>
        <BulkButton icon="delete" tone="danger" spinning={bulkBusy === "delete"} disabled={bulkBusy !== null} onClick={() => setConfirmBulkDelete(true)}>
          {t("providers.deleteSelected")}
        </BulkButton>
      </BulkBar>

      <ConfirmDialog
        open={confirmDelete !== null}
        title={t("providers.deleteCredentialConfirm", { id: confirmDelete ? nameOf(confirmDelete) : "" })}
        message={t("providers.deleteCredentialMessage")}
        confirmText={t("providers.deleteCredential")}
        cancelText={t("common.cancel")}
        variant="danger"
        onConfirm={() => void deleteOne()}
        onClose={() => setConfirmDelete(null)}
      />
      <ConfirmDialog
        open={confirmBulkDelete}
        title={t("providers.account.bulkDeleteTitle", { count: selected.length })}
        message={t("providers.bulkDeleteAccountsMessage")}
        confirmText={t("providers.deleteCount", { count: selected.length })}
        cancelText={t("common.cancel")}
        variant="danger"
        onConfirm={() => void bulkDelete()}
        onClose={() => setConfirmBulkDelete(false)}
      />
    </>
  );
}
