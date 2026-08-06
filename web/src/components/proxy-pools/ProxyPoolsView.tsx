import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Badge,
  Button,
  Card,
  ConfirmDialog,
  EmptyState,
  Field,
  Input,
  Modal,
  Textarea,
  Toggle,
  cn,
} from "../ui";
import {
  createProxyPool,
  deleteProxyPool,
  listProxyPools,
  testProxyPool,
  updateProxyPool,
  type ProxyPoolRow,
} from "./api";
import { formatDateTime, parseProxyLine, statusVariant, suggestPoolId } from "./utils";

type FormData = {
  id: string;
  name: string;
  url: string;
  enabled: boolean;
};

type Props = {
  secret: string;
  onError: (message: string) => void;
  onNotice: (message: string) => void;
  onMutated?: () => void;
};

function normalizeFormData(pool?: ProxyPoolRow): FormData {
  return {
    id: pool?.id || "",
    name: pool?.name || "",
    url: "",
    enabled: pool?.enabled !== false,
  };
}

export function ProxyPoolsView({ secret, onError, onNotice, onMutated }: Props) {
  const { t } = useTranslation();
  const [pools, setPools] = useState<ProxyPoolRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [showFormModal, setShowFormModal] = useState(false);
  const [showBatchImportModal, setShowBatchImportModal] = useState(false);
  const [editingPool, setEditingPool] = useState<ProxyPoolRow | null>(null);
  const [formData, setFormData] = useState<FormData>(normalizeFormData());
  const [batchImportText, setBatchImportText] = useState("");
  const [saving, setSaving] = useState(false);
  const [importing, setImporting] = useState(false);
  const [testingId, setTestingId] = useState<string | null>(null);
  const [selectedIds, setSelectedIds] = useState<string[]>([]);
  const [healthChecking, setHealthChecking] = useState(false);
  const [healthProgress, setHealthProgress] = useState({ current: 0, total: 0 });
  const [bulkBusy, setBulkBusy] = useState(false);
  const [confirmState, setConfirmState] = useState<{
    title: string;
    message: string;
    onConfirm: () => void;
  } | null>(null);

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
    setSelectedIds((prev) => prev.filter((id) => pools.some((pool) => pool.id === id)));
  }, [pools]);

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

  const handleTest = async (poolId: string) => {
    setTestingId(poolId);
    try {
      const result = await testProxyPool(secret, poolId);
      await load();
      onNotice(result.ok ? t("proxyPools.noticeTestPassed") : result.error || t("proxyPools.noticeTestFailed"));
    } catch (error) {
      onError(error instanceof Error ? error.message : t("proxyPools.errorTestFailed"));
    } finally {
      setTestingId(null);
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

  const allSelected = pools.length > 0 && selectedIds.length === pools.length;
  const toggleSelect = (id: string) =>
    setSelectedIds((prev) => (prev.includes(id) ? prev.filter((item) => item !== id) : [...prev, id]));
  const toggleSelectAll = () => setSelectedIds(allSelected ? [] : pools.map((pool) => pool.id));
  const clearSelection = () => setSelectedIds([]);

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
    setConfirmState({
      title: t("proxyPools.confirmBulkDeleteTitle"),
      message: t("proxyPools.confirmBulkDeleteMessage", { count: selectedIds.length }),
      onConfirm: async () => {
        setConfirmState(null);
        setBulkBusy(true);
        let ok = 0;
        let blocked = 0;
        let failed = 0;
        try {
          for (const id of selectedIds) {
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

  const handleHealthCheck = async () => {
    const targets = selectedIds.length > 0 ? pools.filter((pool) => selectedIds.includes(pool.id)) : pools;
    if (targets.length === 0) return;

    setHealthChecking(true);
    setHealthProgress({ current: 0, total: targets.length });
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
          if (result.ok) alive += 1;
          else deadIds.push(pool.id);
        } catch {
          deadIds.push(pool.id);
        } finally {
          done += 1;
          setHealthProgress({ current: done, total: targets.length });
        }
      }
    };

    await Promise.all(Array.from({ length: Math.min(10, targets.length) }, worker));
    await load();
    setHealthChecking(false);
    setHealthProgress({ current: 0, total: 0 });

    if (deadIds.length > 0) {
      setConfirmState({
        title: t("proxyPools.confirmDisableDeadTitle"),
        message: t("proxyPools.confirmDisableDeadMessage", { alive, dead: deadIds.length }),
        onConfirm: async () => {
          setConfirmState(null);
          setBulkBusy(true);
          try {
            for (const id of deadIds) {
              try {
                await updateProxyPool(secret, id, { enabled: false });
              } catch {
                // ignore per-pool failures
              }
            }
            await load();
            onMutated?.();
            onNotice(t("proxyPools.noticeDisabled", { count: deadIds.length }));
          } finally {
            setBulkBusy(false);
          }
        },
      });
    } else {
      onNotice(t("proxyPools.noticeHealthDone", { alive }));
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

  const activeCount = useMemo(() => pools.filter((pool) => pool.enabled).length, [pools]);

  if (loading) {
    return (
      <section className="section proxy-pools-page">
        <div className="proxy-pools-loading">
          <span className="material-symbols-outlined animate-spin">progress_activity</span>
          {t("proxyPools.loading")}
        </div>
      </section>
    );
  }

  return (
    <section className="section proxy-pools-page">
      <div className="proxy-pools-toolbar">
        <div>
          <p className="eyebrow">{t("proxyPools.subtitle")}</p>
          <h2>{t("proxyPools.title")}</h2>
          <p>{t("proxyPools.description")}</p>
        </div>
        <div className="proxy-pools-toolbar-actions">
          <Button variant="secondary" size="sm" icon="upload" onClick={() => setShowBatchImportModal(true)}>
            {t("proxyPools.batchImport")}
          </Button>
          <Button size="sm" icon="add" onClick={openCreateModal}>
            {t("proxyPools.addProxyPool")}
          </Button>
        </div>
      </div>

      <Card pad="md">
        <div className="proxy-pools-summary">
          {pools.length > 0 ? (
            <label className="proxy-pools-select-all">
              <input type="checkbox" checked={allSelected} onChange={toggleSelectAll} />
              {allSelected ? t("proxyPools.unselectAll") : t("proxyPools.selectAll")}
            </label>
          ) : null}
          <Badge variant="default">{t("proxyPools.total", { count: pools.length })}</Badge>
          <Badge variant="success">{t("proxyPools.active", { count: activeCount })}</Badge>
        </div>

        {(selectedIds.length > 0 || healthChecking) && (
          <div className="proxy-pools-bulk-bar">
            <span className="material-symbols-outlined">checklist</span>
            <span>{selectedIds.length > 0 ? t("proxyPools.selected", { count: selectedIds.length }) : t("proxyPools.allPools")}</span>
            <div className="proxy-pools-bulk-actions">
              <Button
                size="sm"
                icon={healthChecking ? "progress_activity" : "health_and_safety"}
                onClick={() => void handleHealthCheck()}
                disabled={healthChecking || bulkBusy || pools.length === 0}
                loading={healthChecking}
              >
                {healthChecking ? t("proxyPools.checking", { current: healthProgress.current, total: healthProgress.total }) : t("proxyPools.healthCheck")}
              </Button>
              {selectedIds.length > 0 ? (
                <>
                  <Button size="sm" variant="secondary" icon="toggle_on" onClick={() => void bulkSetEnabled(true)} disabled={bulkBusy || healthChecking}>
                    {t("proxyPools.activate")}
                  </Button>
                  <Button size="sm" variant="secondary" icon="toggle_off" onClick={() => void bulkSetEnabled(false)} disabled={bulkBusy || healthChecking}>
                    {t("proxyPools.deactivate")}
                  </Button>
                  <Button size="sm" variant="secondary" icon="delete" onClick={bulkDelete} disabled={bulkBusy || healthChecking}>
                    {t("proxyPools.delete")}
                  </Button>
                  <Button size="sm" variant="ghost" onClick={clearSelection} disabled={bulkBusy || healthChecking}>
                    {t("proxyPools.clear")}
                  </Button>
                </>
              ) : null}
            </div>
          </div>
        )}

        {pools.length === 0 ? (
          <EmptyState icon="lan" text={t("proxyPools.emptyTitle")} hint={t("proxyPools.emptyHint")} />
        ) : (
          <div className="proxy-pools-list">
            {pools.map((pool) => (
              <div key={pool.id} className="proxy-pools-row">
                <div className="proxy-pools-row-main">
                  <input
                    type="checkbox"
                    checked={selectedIds.includes(pool.id)}
                    onChange={() => toggleSelect(pool.id)}
                  />
                  <div className="proxy-pools-row-body">
                    <div className="proxy-pools-row-title">
                      <strong>{pool.name || pool.id}</strong>
                      <Badge variant={statusVariant(pool.status)} size="sm">{pool.status || t("proxyPools.statusUnknown")}</Badge>
                      <Badge variant={pool.enabled ? "success" : "default"} size="sm">
                        {pool.enabled ? t("proxyPools.statusActive") : t("proxyPools.statusInactive")}
                      </Badge>
                      <Badge variant="default" size="sm">{t("proxyPools.bound", { count: pool.usage_count || 0 })}</Badge>
                    </div>
                    <p className="proxy-pools-row-url">{pool.url}</p>
                    <p className="proxy-pools-row-meta">
                      <code>{pool.id}</code>
                      <span>{t("proxyPools.lastTested")} {formatDateTime(pool.last_tested_at)}</span>
                      {pool.last_error ? <span>{pool.last_error}</span> : null}
                    </p>
                  </div>
                </div>
                <div className="proxy-pools-row-actions">
                  <Toggle checked={pool.enabled} onChange={() => void handleToggleEnabled(pool)} />
                  <button
                    type="button"
                    className="proxy-pools-icon-btn"
                    onClick={() => void handleTest(pool.id)}
                    disabled={testingId === pool.id}
                    title={t("proxyPools.testProxy")}
                    aria-label={t("proxyPools.testProxy")}
                  >
                    <span className={cn("material-symbols-outlined", testingId === pool.id && "animate-spin")}>
                      {testingId === pool.id ? "progress_activity" : "science"}
                    </span>
                  </button>
                  <button type="button" className="proxy-pools-icon-btn" onClick={() => openEditModal(pool)} title={t("proxyPools.edit")} aria-label={t("proxyPools.edit")}>
                    <span className="material-symbols-outlined">edit</span>
                  </button>
                  <button
                    type="button"
                    className="proxy-pools-icon-btn danger"
                    onClick={() => handleDelete(pool)}
                    title={t("proxyPools.delete")}
                    aria-label={t("proxyPools.delete")}
                  >
                    <span className="material-symbols-outlined">delete</span>
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>

      <Modal
        open={showFormModal}
        onClose={closeFormModal}
        title={editingPool ? t("proxyPools.editProxyPool") : t("proxyPools.addProxyPoolTitle")}
        size="md"
        footer={
          <>
            <Button variant="secondary" onClick={closeFormModal} disabled={saving}>{t("proxyPools.cancel")}</Button>
            <Button
              onClick={() => void handleSave()}
              loading={saving}
              disabled={!formData.name.trim() || (!editingPool && (!formData.url.trim() || !formData.id.trim()))}
            >
              {t("proxyPools.save")}
            </Button>
          </>
        }
      >
        <div className="proxy-pools-form">
          {!editingPool ? (
            <Field label={t("proxyPools.poolId")} required hint={t("proxyPools.poolIdHint")}>
              <Input
                value={formData.id}
                onChange={(event) => setFormData((prev) => ({ ...prev, id: event.target.value }))}
                placeholder={t("proxyPools.poolIdPlaceholder")}
                required
              />
            </Field>
          ) : (
            <Field label={t("proxyPools.poolId")}>
              <Input value={formData.id} disabled />
            </Field>
          )}
          <Field label={t("proxyPools.name")} required>
            <Input
              value={formData.name}
              onChange={(event) => {
                const name = event.target.value;
                setFormData((prev) => ({
                  ...prev,
                  name,
                  id: !editingPool && !prev.id ? suggestPoolId(name) : prev.id,
                }));
              }}
              placeholder={t("proxyPools.namePlaceholder")}
              required
            />
          </Field>
          <Field
            label={t("proxyPools.proxyUrl")}
            required={!editingPool}
            hint={editingPool ? t("proxyPools.proxyUrlHintEdit") : t("proxyPools.proxyUrlHintCreate")}
          >
            <Input
              type="password"
              value={formData.url}
              onChange={(event) => setFormData((prev) => ({ ...prev, url: event.target.value }))}
              placeholder={editingPool ? t("proxyPools.proxyUrlPlaceholderEdit") : t("proxyPools.proxyUrlPlaceholderCreate")}
            />
          </Field>
          <div className="proxy-pools-toggle-row">
            <div>
              <strong>{t("proxyPools.activeField")}</strong>
              <p>{t("proxyPools.activeFieldHint")}</p>
            </div>
            <Toggle
              checked={formData.enabled}
              onChange={(event) => setFormData((prev) => ({ ...prev, enabled: event.target.checked }))}
              disabled={saving}
            />
          </div>
        </div>
      </Modal>

      <Modal
        open={showBatchImportModal}
        onClose={() => {
          if (!importing) setShowBatchImportModal(false);
        }}
        title={t("proxyPools.batchImportTitle")}
        size="md"
        footer={
          <>
            <Button variant="secondary" onClick={() => setShowBatchImportModal(false)} disabled={importing}>{t("proxyPools.cancel")}</Button>
            <Button onClick={() => void handleBatchImport()} loading={importing} disabled={!batchImportText.trim()}>
              {t("proxyPools.import")}
            </Button>
          </>
        }
      >
        <Field
          label={t("proxyPools.pasteProxyList")}
          hint={t("proxyPools.pasteProxyListHint")}
        >
          <Textarea
            value={batchImportText}
            onChange={(event) => setBatchImportText(event.target.value)}
            placeholder={t("proxyPools.pasteProxyPlaceholder")}
            rows={8}
          />
        </Field>
      </Modal>

      <ConfirmDialog
        open={!!confirmState}
        onClose={() => setConfirmState(null)}
        onConfirm={() => confirmState?.onConfirm()}
        title={confirmState?.title || "Confirm"}
        message={confirmState?.message}
        confirmText={t("proxyPools.confirm")}
        variant="danger"
      />
    </section>
  );
}
