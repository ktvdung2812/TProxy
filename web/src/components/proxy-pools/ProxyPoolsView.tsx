import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router-dom";
import { Button, ConfirmDialog } from "../ui";
import {
  BulkBar,
  BulkButton,
  EmptyPanel,
  FilterTabs,
  GuideToggle,
  LegendItem,
  PageGuide,
  PageStats,
  ProgressLine,
  SearchField,
  Segmented,
  SelectField,
  Toolbar,
  ToolbarEnd,
  ToolbarRow,
  ToolButton,
  usePageGuide,
  useStoredState,
} from "../page-kit";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";
import { useAccountMask } from "../../hooks/useAccountMask";
import { maskAccountText } from "../../lib/accountMask";
import { credentialLabel, formatAgo, type SnapshotCredential, type SnapshotProvider } from "../upstreams/health";
import {
  createProxyPool,
  deleteProxyPool,
  listProxyPools,
  testProxyPool,
  updateProxyPool,
  type ProxyPoolRow,
  type ProxyPoolTestResult,
} from "./api";
import { ProxyPoolCard, ProxyPoolListRow, type ProxyPoolHandlers, type ProxyPoolView } from "./ProxyPoolItem";
import { ProxyPoolFormModal, ProxyPoolImportModal, normalizeFormData, type ProxyPoolFormData } from "./ProxyPoolModals";
import {
  POOL_HEALTH_ORDER,
  POOL_HEALTH_TONE,
  parsePoolFilter,
  parseProxyLine,
  poolHealth,
  proxyEndpoint,
  suggestPoolId,
  type PoolHealth,
} from "./utils";

type Filter = PoolHealth | "all";
type SortMode = "status" | "name" | "usage" | "tested";
type ViewMode = "grid" | "list";
const SORT_MODES = ["status", "name", "usage", "tested"] as const;
const VIEW_MODES = ["grid", "list"] as const;

const HEALTH_ICON: Record<PoolHealth, string> = {
  healthy: "check_circle",
  failing: "error",
  untested: "help",
  off: "power_settings_new",
};

type Props = {
  secret: string;
  onError: (message: string) => void;
  onNotice: (message: string) => void;
  onMutated?: () => void;
  /** Snapshot data, used only to name what each pool is bound to. */
  providers?: SnapshotProvider[];
  credentials?: Record<string, SnapshotCredential[]>;
};

type ConfirmState = {
  title: string;
  message: string;
  confirmText: string;
  onConfirm: () => void;
};

export function ProxyPoolsView({ secret, onError, onNotice, onMutated, providers = [], credentials = {} }: Props) {
  const { t, i18n } = useTranslation();
  const { masked } = useAccountMask();
  const { copy } = useCopyToClipboard();
  const [searchParams, setSearchParams] = useSearchParams();
  const filter = parsePoolFilter(searchParams.get("status"));
  const [pools, setPools] = useState<ProxyPoolRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [sortMode, setSortMode] = useStoredState<SortMode>("tproxy.proxyPools.sort", "status", SORT_MODES);
  const [viewMode, setViewMode] = useStoredState<ViewMode>("tproxy.proxyPools.view", "grid", VIEW_MODES);
  const guide = usePageGuide("proxy-pools");
  const [showFormModal, setShowFormModal] = useState(false);
  const [showBatchImportModal, setShowBatchImportModal] = useState(false);
  const [editingPool, setEditingPool] = useState<ProxyPoolRow | null>(null);
  const [formData, setFormData] = useState<ProxyPoolFormData>(normalizeFormData());
  const [batchImportText, setBatchImportText] = useState("");
  const [saving, setSaving] = useState(false);
  const [importing, setImporting] = useState(false);
  const [testingIds, setTestingIds] = useState<Set<string>>(() => new Set());
  const [results, setResults] = useState<Record<string, ProxyPoolTestResult>>({});
  const [selectedIds, setSelectedIds] = useState<string[]>([]);
  const [healthChecking, setHealthChecking] = useState(false);
  const [healthProgress, setHealthProgress] = useState({ current: 0, total: 0 });
  const [bulkBusy, setBulkBusy] = useState(false);
  const [confirmState, setConfirmState] = useState<ConfirmState | null>(null);
  const [clock, setClock] = useState(() => Date.now());

  useEffect(() => {
    const timer = window.setInterval(() => setClock(Date.now()), 30000);
    return () => window.clearInterval(timer);
  }, []);

  const load = useCallback(async () => {
    try {
      const result = await listProxyPools(secret);
      setPools(result.proxy_pools || []);
    } catch (error) {
      onError(error instanceof Error ? error.message : t("proxyPools.errorLoadFailed"));
    } finally {
      setLoading(false);
    }
  }, [secret, onError]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    setSelectedIds((prev) => {
      const next = prev.filter((id) => pools.some((pool) => pool.id === id));
      return next.length === prev.length ? prev : next;
    });
  }, [pools]);

  const setFilter = (next: Filter) => {
    setSearchParams((current) => {
      const params = new URLSearchParams(current);
      if (next === "all") params.delete("status");
      else params.set("status", next);
      return params;
    }, { replace: true });
  };
  const toggleFilter = (next: Filter) => setFilter(filter === next ? "all" : next);

  const resetForm = () => {
    setEditingPool(null);
    setFormData(normalizeFormData());
  };

  const openCreateModal = () => {
    resetForm();
    setShowFormModal(true);
  };

  const openEditModal = (pool: ProxyPoolRow) => {
    setEditingPool(pool);
    setFormData(normalizeFormData(pool));
    setShowFormModal(true);
  };

  const closeFormModal = () => {
    setShowFormModal(false);
    resetForm();
  };

  const handleSave = async () => {
    const name = formData.name.trim();
    const url = formData.url.trim();
    if (!name) return;
    if (!editingPool && !url) return;
    if (!editingPool && !formData.id.trim()) return;

    setSaving(true);
    try {
      if (editingPool) {
        await updateProxyPool(secret, editingPool.id, {
          name,
          ...(url ? { url } : {}),
          enabled: formData.enabled,
        });
        onNotice(t("proxyPools.noticeUpdated"));
      } else {
        await createProxyPool(secret, {
          id: formData.id.trim(),
          name,
          url,
          enabled: formData.enabled,
        });
        onNotice(t("proxyPools.noticeCreated"));
      }
      await load();
      onMutated?.();
      closeFormModal();
    } catch (error) {
      onError(error instanceof Error ? error.message : t("proxyPools.errorSaveFailed"));
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = (pool: ProxyPoolRow) => {
    setConfirmState({
      title: t("proxyPools.confirmDeleteTitle"),
      message:
        pool.usage_count > 0
          ? t("proxyPools.confirmDeleteMessage", { name: pool.name, count: pool.usage_count })
          : t("proxyPools.confirmDeleteMessageSimple", { name: pool.name }),
      confirmText: t("proxyPools.deletePool"),
      onConfirm: async () => {
        setConfirmState(null);
        try {
          await deleteProxyPool(secret, pool.id);
          setPools((prev) => prev.filter((item) => item.id !== pool.id));
          onNotice(t("proxyPools.noticeDeleted"));
          onMutated?.();
        } catch (error) {
          const message = error instanceof Error ? error.message : t("proxyPools.errorDeleteFailed");
          if ((error as Error & { status?: number }).status === 409) {
            onError(t("proxyPools.errorDeleteConflict", { message }));
          } else {
            onError(message);
          }
        }
      },
    });
  };

  const markTesting = (id: string, testing: boolean) => {
    setTestingIds((current) => {
      const next = new Set(current);
      if (testing) next.add(id);
      else next.delete(id);
      return next;
    });
  };

  const handleTest = async (pool: ProxyPoolRow) => {
    markTesting(pool.id, true);
    try {
      const result = await testProxyPool(secret, pool.id);
      setResults((current) => ({ ...current, [pool.id]: result }));
      await load();
      onNotice(result.ok ? t("proxyPools.noticeTestPassedNamed", { name: pool.name, ms: result.elapsed_ms ?? 0 }) : result.error || t("proxyPools.noticeTestFailed"));
    } catch (error) {
      onError(error instanceof Error ? error.message : t("proxyPools.errorTestFailed"));
    } finally {
      markTesting(pool.id, false);
    }
  };

  const handleToggleEnabled = async (pool: ProxyPoolRow) => {
    const next = !pool.enabled;
    setPools((prev) => prev.map((item) => (item.id === pool.id ? { ...item, enabled: next } : item)));
    try {
      await updateProxyPool(secret, pool.id, { enabled: next });
      onMutated?.();
    } catch (error) {
      setPools((prev) => prev.map((item) => (item.id === pool.id ? { ...item, enabled: pool.enabled } : item)));
      onError(error instanceof Error ? error.message : t("proxyPools.errorUpdateFailed"));
    }
  };

  const clearSelection = () => setSelectedIds([]);
  const toggleSelection = (ids: string[], select: boolean) =>
    setSelectedIds((prev) => (select ? [...new Set([...prev, ...ids])] : prev.filter((id) => !ids.includes(id))));

  const bulkSetEnabled = async (enabled: boolean) => {
    const targets = selectedIds.length > 0 ? selectedIds : pools.map((pool) => pool.id);
    if (targets.length === 0) return;
    setBulkBusy(true);
    let ok = 0;
    let failed = 0;
    try {
      for (const id of targets) {
        try {
          await updateProxyPool(secret, id, { enabled });
          ok += 1;
        } catch {
          failed += 1;
        }
      }
      await load();
      onMutated?.();
      if (enabled) {
        onNotice(failed ? t("proxyPools.noticeActivatedWithFailed", { ok, failed }) : t("proxyPools.noticeActivated", { ok }));
      } else {
        onNotice(failed ? t("proxyPools.noticeDeactivatedWithFailed", { ok, failed }) : t("proxyPools.noticeDeactivated", { ok }));
      }
    } finally {
      setBulkBusy(false);
    }
  };

  const bulkDelete = () => {
    if (selectedIds.length === 0) return;
    const ids = [...selectedIds];
    setConfirmState({
      title: t("proxyPools.confirmBulkDeleteTitle"),
      message: t("proxyPools.confirmBulkDeleteMessage", { count: ids.length }),
      confirmText: t("proxyPools.delete"),
      onConfirm: async () => {
        setConfirmState(null);
        setBulkBusy(true);
        let ok = 0;
        let blocked = 0;
        let failed = 0;
        try {
          for (const id of ids) {
            try {
              await deleteProxyPool(secret, id);
              ok += 1;
            } catch (error) {
              if ((error as Error & { status?: number }).status === 409) blocked += 1;
              else failed += 1;
            }
          }
          await load();
          clearSelection();
          onMutated?.();
          if (blocked && failed) {
            onNotice(t("proxyPools.noticeBulkDeletedFull", { ok, blocked, failed }));
          } else if (blocked) {
            onNotice(t("proxyPools.noticeBulkDeletedBlocked", { ok, blocked }));
          } else if (failed) {
            onNotice(t("proxyPools.noticeBulkDeletedFailed", { ok, failed }));
          } else {
            onNotice(t("proxyPools.noticeBulkDeleted", { ok }));
          }
        } finally {
          setBulkBusy(false);
        }
      },
    });
  };

  /** Tests the selection (or every pool), then offers to turn the dead ones off. */
  const handleHealthCheck = async () => {
    const targets = selectedIds.length > 0 ? pools.filter((pool) => selectedIds.includes(pool.id)) : pools;
    if (targets.length === 0 || healthChecking) return;

    setHealthChecking(true);
    setHealthProgress({ current: 0, total: targets.length });
    setTestingIds(new Set(targets.map((pool) => pool.id)));
    let alive = 0;
    const deadIds: string[] = [];
    let done = 0;
    const queue = [...targets];

    const worker = async () => {
      while (queue.length > 0) {
        const pool = queue.shift();
        if (!pool) break;
        try {
          const result = await testProxyPool(secret, pool.id);
          setResults((current) => ({ ...current, [pool.id]: result }));
          if (result.ok) alive += 1;
          else deadIds.push(pool.id);
        } catch {
          deadIds.push(pool.id);
        } finally {
          done += 1;
          markTesting(pool.id, false);
          setHealthProgress({ current: done, total: targets.length });
        }
      }
    };

    await Promise.all(Array.from({ length: Math.min(10, targets.length) }, worker));
    await load();
    setHealthChecking(false);
    setHealthProgress({ current: 0, total: 0 });

    // Only pools that are still on can be turned off.
    const enabledDead = deadIds.filter((id) => targets.find((pool) => pool.id === id)?.enabled);
    if (enabledDead.length > 0) {
      setConfirmState({
        title: t("proxyPools.confirmDisableDeadTitle"),
        message: t("proxyPools.confirmDisableDeadMessage", { alive, dead: enabledDead.length, count: enabledDead.length }),
        confirmText: t("proxyPools.turnOffDead", { count: enabledDead.length }),
        onConfirm: async () => {
          setConfirmState(null);
          setBulkBusy(true);
          try {
            for (const id of enabledDead) {
              try {
                await updateProxyPool(secret, id, { enabled: false });
              } catch {
                // A pool that fails to update keeps its state; the list reload shows it.
              }
            }
            await load();
            onMutated?.();
            onNotice(t("proxyPools.noticeDisabled", { count: enabledDead.length }));
          } finally {
            setBulkBusy(false);
          }
        },
      });
    } else {
      onNotice(deadIds.length ? t("proxyPools.noticeHealthDoneDead", { alive, dead: deadIds.length }) : t("proxyPools.noticeHealthDone", { alive }));
    }
  };

  const handleBatchImport = async () => {
    const lines = batchImportText
      .split(/\r?\n/)
      .map((line) => line.trim())
      .filter(Boolean);

    if (lines.length === 0) {
      onError(t("proxyPools.errorPasteOneLine"));
      return;
    }

    const parsedEntries: Array<{ proxyUrl: string; name: string; lineNumber: number }> = [];
    const invalidLines: string[] = [];

    lines.forEach((line, index) => {
      try {
        const parsed = parseProxyLine(line);
        if (parsed) parsedEntries.push({ ...parsed, lineNumber: index + 1 });
      } catch (error) {
        invalidLines.push(t("proxyPools.errorInvalidFormatLine", { line: index + 1, error: error instanceof Error ? error.message : t("proxyPools.errorInvalidFormatGeneric") }));
      }
    });

    if (invalidLines.length > 0) {
      onError(t("proxyPools.errorInvalidFormat", { lines: invalidLines.join("\n") }));
      return;
    }

    setImporting(true);
    try {
      const existingKeys = new Set(pools.map((pool) => `${pool.url.trim()}|||${pool.id}`));
      let created = 0;
      let skipped = 0;
      let failed = 0;

      for (const entry of parsedEntries) {
        const dedupeKey = `${entry.proxyUrl}|||`;
        if (existingKeys.has(dedupeKey)) {
          skipped += 1;
          continue;
        }
        const id = `${suggestPoolId(entry.name)}-${created + 1}`;
        try {
          await createProxyPool(secret, {
            id,
            name: entry.name,
            url: entry.proxyUrl,
            enabled: true,
          });
          created += 1;
          existingKeys.add(dedupeKey);
        } catch {
          failed += 1;
        }
      }

      await load();
      onMutated?.();
      setShowBatchImportModal(false);
      setBatchImportText("");
      onNotice(t("proxyPools.noticeBatchImportCompleted", { created, skipped, failed }));
    } catch (error) {
      onError(error instanceof Error ? error.message : t("proxyPools.errorBatchImportFailed"));
    } finally {
      setImporting(false);
    }
  };

  // Which providers and accounts route through each pool, by name.
  const usedByPool = useMemo(() => {
    const names: Record<string, string[]> = {};
    const add = (poolId: string, name: string) => {
      (names[poolId] ||= []).push(name);
    };
    for (const provider of providers) {
      for (const poolId of provider.ProxyPoolIDs || []) add(poolId, provider.Name || provider.ID);
    }
    for (const [providerId, list] of Object.entries(credentials)) {
      for (const credential of list || []) {
        for (const poolId of credential.proxy_pool_ids || []) add(poolId, `${maskAccountText(credentialLabel(credential), masked)} (${providerId})`);
      }
    }
    return names;
  }, [providers, credentials, masked]);

  const views = useMemo<ProxyPoolView[]>(
    () =>
      pools.map((pool) => {
        const endpoint = proxyEndpoint(pool.url);
        return {
          pool,
          name: pool.name || pool.id,
          health: poolHealth(pool),
          scheme: endpoint.scheme,
          host: endpoint.host,
          usedBy: usedByPool[pool.id] || [],
          lastTested: formatAgo(pool.last_tested_at, i18n.language, clock),
          lastResult: results[pool.id],
          testing: testingIds.has(pool.id),
          busy: bulkBusy || testingIds.has(pool.id),
          selected: selectedIds.includes(pool.id),
        };
      }),
    [pools, usedByPool, results, testingIds, bulkBusy, selectedIds, i18n.language, clock],
  );

  const searched = useMemo(() => {
    const query = search.trim().toLowerCase();
    if (!query) return views;
    return views.filter((view) =>
      [view.name, view.pool.id, view.host, view.pool.last_error, ...view.usedBy].some((value) => value?.toLowerCase().includes(query)),
    );
  }, [views, search]);

  const counts = useMemo(() => {
    const result: Record<PoolHealth, number> = { healthy: 0, failing: 0, untested: 0, off: 0 };
    for (const view of searched) result[view.health] += 1;
    return result;
  }, [searched]);

  const usage = useMemo(() => {
    const inUse = searched.filter((view) => (view.pool.usage_count || 0) > 0);
    return { pools: inUse.length, bindings: inUse.reduce((sum, view) => sum + view.pool.usage_count, 0) };
  }, [searched]);

  const visible = useMemo(() => {
    const list = filter === "all" ? [...searched] : searched.filter((view) => view.health === filter);
    const byName = (a: ProxyPoolView, b: ProxyPoolView) => a.name.localeCompare(b.name);
    if (sortMode === "name") return list.sort(byName);
    if (sortMode === "usage") return list.sort((a, b) => (b.pool.usage_count || 0) - (a.pool.usage_count || 0) || byName(a, b));
    if (sortMode === "tested") {
      const time = (view: ProxyPoolView) => Date.parse(view.pool.last_tested_at || "") || 0;
      return list.sort((a, b) => time(a) - time(b) || byName(a, b));
    }
    return list.sort((a, b) => POOL_HEALTH_ORDER.indexOf(a.health) - POOL_HEALTH_ORDER.indexOf(b.health) || byName(a, b));
  }, [searched, filter, sortMode]);

  const allVisibleSelected = visible.length > 0 && visible.every((view) => selectedIds.includes(view.pool.id));
  const someVisibleSelected = !allVisibleSelected && visible.some((view) => selectedIds.includes(view.pool.id));

  const handlers: ProxyPoolHandlers = {
    edit: openEditModal,
    select: (pool, select) => toggleSelection([pool.id], select),
    setEnabled: (pool) => void handleToggleEnabled(pool),
    test: (pool) => void handleTest(pool),
    copyId: (pool) => {
      copy(pool.id, `pool_${pool.id}`);
      onNotice(t("proxyPools.idCopied", { id: pool.id }));
    },
    remove: handleDelete,
  };

  const hasFilters = filter !== "all" || search.trim() !== "";
  const clearFilters = () => {
    setSearch("");
    setFilter("all");
  };

  const modals = (
    <>
      <ProxyPoolFormModal
        open={showFormModal}
        editingPool={editingPool}
        formData={formData}
        setFormData={setFormData}
        saving={saving}
        onSave={() => void handleSave()}
        onClose={closeFormModal}
      />
      <ProxyPoolImportModal
        open={showBatchImportModal}
        text={batchImportText}
        setText={setBatchImportText}
        importing={importing}
        onImport={() => void handleBatchImport()}
        onClose={() => setShowBatchImportModal(false)}
      />
      <ConfirmDialog
        open={!!confirmState}
        onClose={() => setConfirmState(null)}
        onConfirm={() => confirmState?.onConfirm()}
        title={confirmState?.title || t("proxyPools.confirm")}
        message={confirmState?.message}
        confirmText={confirmState?.confirmText || t("proxyPools.confirm")}
        cancelText={t("proxyPools.cancel")}
        variant="danger"
      />
    </>
  );

  if (!loading && pools.length === 0) {
    return (
      <section className="cp-page">
        <EmptyPanel
          icon="lan"
          title={t("proxyPools.emptyTitle")}
          description={t("proxyPools.emptyHint")}
          action={
            <div className="pp-empty-actions">
              <Button variant="primary" icon="add" onClick={openCreateModal}>{t("proxyPools.addProxyPool")}</Button>
              <Button variant="secondary" icon="upload" onClick={() => setShowBatchImportModal(true)}>{t("proxyPools.batchImport")}</Button>
            </div>
          }
        />
        {modals}
      </section>
    );
  }

  const tile = (health: PoolHealth) => ({
    key: health,
    icon: HEALTH_ICON[health],
    tone: POOL_HEALTH_TONE[health],
    label: t(`proxyPools.health.${health}`),
    value: loading ? "—" : counts[health],
    hint: t(`proxyPools.tileHint.${health}`),
    muted: counts[health] === 0,
    active: filter === health,
    onClick: () => toggleFilter(health),
    title: t(`proxyPools.healthHint.${health}`),
  });

  const testTargets = selectedIds.length || pools.length;

  return (
    <section className="cp-page">
      <PageStats
        label={t("proxyPools.statsLabel")}
        items={[
          {
            key: "all",
            icon: "lan",
            label: t("proxyPools.stats.pools"),
            value: loading ? "—" : searched.length,
            hint: t("proxyPools.stats.poolsHint", { count: searched.length - counts.off }),
            active: filter === "all",
            onClick: () => setFilter("all"),
          },
          tile("healthy"),
          tile("failing"),
          tile("untested"),
          tile("off"),
          {
            key: "in-use",
            icon: "link",
            label: t("proxyPools.stats.inUse"),
            value: loading ? "—" : usage.pools,
            hint: t("proxyPools.stats.inUseHint", { count: usage.bindings, unused: searched.length - usage.pools }),
            muted: usage.pools === 0,
          },
        ]}
      />

      <Toolbar>
        <ToolbarRow>
          <FilterTabs
            label={t("proxyPools.filterLabel")}
            value={filter}
            onChange={toggleFilter}
            items={[
              { value: "all", icon: "apps", label: t("proxyPools.filterAll"), count: searched.length },
              ...POOL_HEALTH_ORDER.map((health) => ({
                value: health,
                icon: HEALTH_ICON[health],
                tone: POOL_HEALTH_TONE[health],
                label: t(`proxyPools.health.${health}`),
                count: counts[health],
              })),
            ]}
          />
          <ToolbarEnd>
            <ToolButton icon="upload" onClick={() => setShowBatchImportModal(true)}>{t("proxyPools.batchImport")}</ToolButton>
            <Button variant="primary" size="sm" icon="add" onClick={openCreateModal}>{t("proxyPools.addProxyPool")}</Button>
          </ToolbarEnd>
        </ToolbarRow>
        <ToolbarRow>
          <SearchField value={search} onChange={setSearch} placeholder={t("proxyPools.searchPlaceholder")} />
          <ToolbarEnd>
            <SelectField
              label={t("proxyPools.sortLabel")}
              value={sortMode}
              onChange={setSortMode}
              options={[
                { value: "status", label: t("proxyPools.sort.status") },
                { value: "name", label: t("proxyPools.sort.name") },
                { value: "usage", label: t("proxyPools.sort.usage") },
                { value: "tested", label: t("proxyPools.sort.tested") },
              ]}
            />
            <Segmented
              label={t("proxyPools.viewMode")}
              value={viewMode}
              onChange={setViewMode}
              items={[
                { value: "grid", icon: "grid_view", label: t("proxyPools.viewGrid") },
                { value: "list", icon: "view_list", label: t("proxyPools.viewList") },
              ]}
            />
            <ToolButton
              icon="network_check"
              spinning={healthChecking}
              disabled={healthChecking || bulkBusy || pools.length === 0}
              onClick={() => void handleHealthCheck()}
              title={t("proxyPools.testAllHint")}
            >
              {selectedIds.length ? t("proxyPools.testSelected", { count: testTargets }) : t("proxyPools.testAll")}
            </ToolButton>
            <GuideToggle open={guide.open} onToggle={guide.toggle} />
          </ToolbarEnd>
        </ToolbarRow>
        {healthChecking ? (
          <ProgressLine value={healthProgress.current} total={healthProgress.total}>
            {t("proxyPools.checking", { current: healthProgress.current, total: healthProgress.total })}
          </ProgressLine>
        ) : null}
      </Toolbar>

      <PageGuide
        open={guide.open}
        onDismiss={guide.dismiss}
        title={t("proxyPools.guide.title")}
        steps={[
          { icon: "lan", text: t("proxyPools.guide.what") },
          { icon: "network_check", text: t("proxyPools.guide.test") },
          { icon: "link", text: t("proxyPools.guide.bind") },
        ]}
        legend={
          <>
            <LegendItem tone="success">{t("proxyPools.health.healthy")}</LegendItem>
            <LegendItem tone="danger">{t("proxyPools.health.failing")}</LegendItem>
            <LegendItem tone="neutral">{t("proxyPools.health.untested")}</LegendItem>
            <LegendItem tone="muted">{t("proxyPools.health.off")}</LegendItem>
          </>
        }
      />

      {loading ? (
        <div className="cp-grid" style={{ ["--cp-card-min" as string]: "300px" }} aria-label={t("proxyPools.loading")}>
          {[0, 1, 2].map((index) => (
            <div key={index} className="cp-card pp-skeleton-card">
              <div className="cp-skeleton"><span /><span /><span /></div>
            </div>
          ))}
        </div>
      ) : visible.length === 0 ? (
        <EmptyPanel
          icon="filter_alt_off"
          title={t("proxyPools.noMatch")}
          description={t("proxyPools.noMatchHint")}
          action={hasFilters ? <Button variant="secondary" icon="filter_alt_off" onClick={clearFilters}>{t("proxyPools.clearFilters")}</Button> : null}
        />
      ) : viewMode === "grid" ? (
        <div className="cp-grid" style={{ ["--cp-card-min" as string]: "300px" }}>
          {visible.map((view) => <ProxyPoolCard key={view.pool.id} view={view} handlers={handlers} />)}
        </div>
      ) : (
        <div className="cp-list pp-list">
          <div className="cp-list-head" aria-hidden>
            <span>
              <input
                type="checkbox"
                className="cp-check"
                tabIndex={-1}
                checked={allVisibleSelected}
                ref={(element) => {
                  if (element) element.indeterminate = someVisibleSelected;
                }}
                onChange={(event) => toggleSelection(visible.map((view) => view.pool.id), event.target.checked)}
                aria-label={t("proxyPools.selectAll")}
              />
            </span>
            <span>{t("proxyPools.columns.pool")}</span>
            <span>{t("proxyPools.columns.status")}</span>
            <span>{t("proxyPools.columns.endpoint")}</span>
            <span>{t("proxyPools.columns.usedBy")}</span>
            <span>{t("proxyPools.columns.lastTest")}</span>
            <span>{t("proxyPools.columns.on")}</span>
            <span />
          </div>
          {visible.map((view) => <ProxyPoolListRow key={view.pool.id} view={view} handlers={handlers} />)}
        </div>
      )}

      <BulkBar
        count={selectedIds.length}
        busy={bulkBusy || healthChecking}
        onClear={clearSelection}
        selectAll={
          allVisibleSelected
            ? null
            : { label: t("proxyPools.selectAllVisible", { count: visible.length }), onSelect: () => toggleSelection(visible.map((view) => view.pool.id), true) }
        }
      >
        <BulkButton icon="network_check" spinning={healthChecking} disabled={bulkBusy || healthChecking} onClick={() => void handleHealthCheck()}>
          {t("proxyPools.bulkTest")}
        </BulkButton>
        <BulkButton icon="toggle_on" disabled={bulkBusy || healthChecking} onClick={() => void bulkSetEnabled(true)}>
          {t("proxyPools.activate")}
        </BulkButton>
        <BulkButton icon="toggle_off" disabled={bulkBusy || healthChecking} onClick={() => void bulkSetEnabled(false)}>
          {t("proxyPools.deactivate")}
        </BulkButton>
        <BulkButton icon="delete" tone="danger" disabled={bulkBusy || healthChecking} onClick={bulkDelete}>
          {t("proxyPools.delete")}
        </BulkButton>
      </BulkBar>

      {modals}
    </section>
  );
}
