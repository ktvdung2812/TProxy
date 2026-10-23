import { useCallback, useEffect, useMemo, useState } from "react";
import type { FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";
import { useApiKeySecrets } from "../../hooks/useApiKeySecrets";
import { getApiKeySecretsVersion, getStoredApiKeySecret, storeApiKeySecret } from "../../lib/apiKeySecrets";
import { Button, ConfirmDialog, Modal } from "../ui";
import {
  BulkBar,
  BulkButton,
  EmptyPanel,
  FilterTabs,
  GuideToggle,
  LegendItem,
  PageGuide,
  PageStats,
  SearchField,
  SectionHead,
  SelectField,
  ToneCounts,
  ToolButton,
  Toolbar,
  ToolbarEnd,
  ToolbarRow,
  usePageGuide,
  useStoredState,
} from "../page-kit";
import { fetchAdminSettings } from "../settings/api";
import { createApiKey, deleteApiKey, fetchApiKeyUsage, toggleApiKey, updateApiKey } from "./api";
import { ApiEndpoints } from "./ApiEndpoints";
import { ApiKeyCapacity } from "./ApiKeyCapacity";
import { ApiKeyList, type ApiKeyRowHandlers, type ApiKeyRowView } from "./ApiKeyList";
import { ApiKeyCreateModal, ApiKeyCreatedModal, ApiKeyEditModal } from "./ApiKeyModals";
import { ApiQuickStart } from "./ApiQuickStart";
import { LanPortHelpModal } from "./LanPortHelpModal";
import { TunnelSection, type RemoteAccessSummary } from "./TunnelSection";
import type {
  ApiKeyCombo,
  ApiKeyCredential,
  ApiKeyFormData,
  ApiKeyProvider,
  ApiKeyRecord,
  ApiKeyRoute,
  ApiKeyUsage,
  ApiModelOption,
} from "./types";
import {
  apiKeyHealth,
  apiKeyToForm,
  buildCurlExample,
  buildLocalGatewayBaseUrl,
  emptyApiKeyForm,
  isAttentionHealth,
  resolveExampleApiKeySecret,
  type ApiKeyHealth,
} from "./utils";

type Props = {
  secret: string;
  apiKeys: ApiKeyRecord[];
  modelOptions: ApiModelOption[];
  providers: ApiKeyProvider[];
  credentials: Record<string, ApiKeyCredential[]>;
  routes: Record<string, ApiKeyRoute[]>;
  combos: ApiKeyCombo[];
  onError: (message: string) => void;
  onNotice: (message: string) => void;
  onMutated?: () => void;
};

type KeyFilter = "all" | "active" | "attention" | "paused";
type SortMode = "status" | "name" | "requests" | "spend";
const SORT_MODES: readonly SortMode[] = ["status", "name", "requests", "spend"];

/** Worst first, so keys that cannot serve traffic are at the top. */
const HEALTH_ORDER: ApiKeyHealth[] = ["noModels", "overBudget", "noAccounts", "nearBudget", "active", "paused"];

type ConfirmState = {
  title: string;
  message: string;
  confirmText: string;
  onConfirm: () => void;
};

function bucket(health: ApiKeyHealth): Exclude<KeyFilter, "all"> {
  if (health === "paused") return "paused";
  return isAttentionHealth(health) ? "attention" : "active";
}

function errorText(error: unknown, fallback: string) {
  return error instanceof Error ? error.message : fallback;
}

export function ApisView({ secret, apiKeys, modelOptions, providers, credentials, routes, combos, onError, onNotice, onMutated }: Props) {
  const { t, i18n } = useTranslation();
  useApiKeySecrets();
  // Bumps when a secret is saved in this browser, so masked secrets refresh.
  const secretsVersion = getApiKeySecretsVersion();
  const guide = usePageGuide("apis");
  const { copied, copy } = useCopyToClipboard();

  const [usageById, setUsageById] = useState<Record<string, ApiKeyUsage>>({});
  const [loadingUsage, setLoadingUsage] = useState(true);
  const [filter, setFilter] = useState<KeyFilter>("all");
  const [search, setSearch] = useState("");
  const [sortMode, setSortMode] = useStoredState<SortMode>("tproxy.apis.sort", "status", SORT_MODES);
  const [selectedIds, setSelectedIds] = useState<string[]>([]);
  const [busyIds, setBusyIds] = useState<Set<string>>(new Set());
  const [bulkBusy, setBulkBusy] = useState(false);

  const [showCreateModal, setShowCreateModal] = useState(false);
  const [showAdvancedCreate, setShowAdvancedCreate] = useState(false);
  const [editingKey, setEditingKey] = useState<ApiKeyRecord | null>(null);
  const [capacityKeyId, setCapacityKeyId] = useState<string | null>(null);
  const [formData, setFormData] = useState<ApiKeyFormData>(emptyApiKeyForm());
  const [editSecret, setEditSecret] = useState("");
  const [saving, setSaving] = useState(false);
  const [createdSecret, setCreatedSecret] = useState<string | null>(null);
  const [confirmState, setConfirmState] = useState<ConfirmState | null>(null);

  const [exampleApiKey, setExampleApiKey] = useState("");
  const [exampleApiKeyId, setExampleApiKeyId] = useState("");
  const [exampleModel, setExampleModel] = useState(() => modelOptions[0]?.value ?? "");

  const [allowLanAccess, setAllowLanAccess] = useState(false);
  const [lanIPs, setLanIPs] = useState<string[]>([]);
  const [serverPort, setServerPort] = useState(28120);
  const [lanRestartRequired, setLanRestartRequired] = useState(false);
  const [showLanPortHelp, setShowLanPortHelp] = useState(false);
  const [remote, setRemote] = useState<RemoteAccessSummary | null>(null);

  const isRemoteHost = typeof window !== "undefined" && !["localhost", "127.0.0.1", "::1"].includes(window.location.hostname);
  const baseUrl = useMemo(() => buildLocalGatewayBaseUrl(serverPort), [serverPort]);

  useEffect(() => {
    void fetchAdminSettings(secret)
      .then((settings) => {
        setAllowLanAccess(Boolean(settings.allow_lan_management));
        setLanIPs(settings.lan_ips || []);
        setServerPort(settings.server_port || 28120);
        setLanRestartRequired(Boolean(settings.restart_required));
      })
      .catch(() => {
        // Keep the endpoint list usable when settings cannot be loaded.
      });
  }, [secret]);

  const loadUsage = useCallback(async () => {
    setLoadingUsage(true);
    try {
      const result = await fetchApiKeyUsage(secret);
      const mapped: Record<string, ApiKeyUsage> = {};
      for (const item of result.api_keys || []) mapped[item.id] = item;
      setUsageById(mapped);
    } catch (error) {
      onError(errorText(error, t("apis.failedLoadUsage")));
    } finally {
      setLoadingUsage(false);
    }
  }, [secret, onError, t]);

  // Usage is refreshed every 30s and whenever the key list itself changes.
  useEffect(() => {
    void loadUsage();
    const timer = window.setInterval(() => void loadUsage(), 30000);
    return () => window.clearInterval(timer);
  }, [loadUsage, apiKeys]);

  useEffect(() => {
    if (!exampleModel && modelOptions[0]?.value) setExampleModel(modelOptions[0].value);
  }, [exampleModel, modelOptions]);

  // Drop selections for keys that no longer exist.
  useEffect(() => {
    setSelectedIds((current) => {
      const next = current.filter((id) => apiKeys.some((key) => key.id === id));
      return next.length === current.length ? current : next;
    });
  }, [apiKeys]);

  const curlExample = useMemo(
    () => buildCurlExample(baseUrl, resolveExampleApiKeySecret(exampleApiKeyId, exampleApiKey), exampleModel),
    [baseUrl, exampleApiKeyId, exampleApiKey, exampleModel],
  );

  const setBusy = (ids: string[], busy: boolean) =>
    setBusyIds((current) => {
      const next = new Set(current);
      for (const id of ids) {
        if (busy) next.add(id);
        else next.delete(id);
      }
      return next;
    });

  /* ---------------- Mutations ---------------- */

  const openCreateModal = () => {
    setFormData(emptyApiKeyForm());
    setShowAdvancedCreate(false);
    setShowCreateModal(true);
  };

  const closeCreateModal = () => {
    setShowCreateModal(false);
    setFormData(emptyApiKeyForm());
    setShowAdvancedCreate(false);
  };

  const openEditModal = (key: ApiKeyRecord) => {
    setEditingKey(key);
    setFormData(apiKeyToForm(key));
    setEditSecret(getStoredApiKeySecret(key.id) || "");
  };

  const closeEditModal = () => {
    setEditingKey(null);
    setEditSecret("");
  };

  const handleCreate = async () => {
    if (!formData.name.trim()) return;
    setSaving(true);
    try {
      const result = await createApiKey(secret, formData);
      storeApiKeySecret(result.id, result.key);
      setCreatedSecret(result.key);
      closeCreateModal();
      onNotice(t("apis.keyCreatedNotice"));
      onMutated?.();
      await loadUsage();
    } catch (error) {
      onError(errorText(error, t("apis.failedCreateKey")));
    } finally {
      setSaving(false);
    }
  };

  const handleEdit = async (event: FormEvent) => {
    event.preventDefault();
    if (!editingKey || !formData.name.trim()) return;
    setSaving(true);
    try {
      await updateApiKey(secret, editingKey.id, formData);
      if (editSecret.trim()) storeApiKeySecret(editingKey.id, editSecret.trim());
      onNotice(t("apis.keyUpdatedNotice", { name: formData.name }));
      closeEditModal();
      onMutated?.();
      await loadUsage();
    } catch (error) {
      onError(errorText(error, t("apis.failedUpdateKey")));
    } finally {
      setSaving(false);
    }
  };

  const applyEnabled = async (key: ApiKeyRecord, enabled: boolean) => {
    setBusy([key.id], true);
    try {
      await toggleApiKey(secret, key, enabled);
      const name = key.name || key.id;
      onNotice(enabled ? t("apis.keyEnabledNotice", { name }) : t("apis.keyDisabledNotice", { name }));
      onMutated?.();
      await loadUsage();
    } catch (error) {
      onError(errorText(error, t("apis.failedUpdateKey")));
    } finally {
      setBusy([key.id], false);
    }
  };

  /** Resuming is harmless; pausing cuts clients off, so it asks first. */
  const setEnabled = (key: ApiKeyRecord, enabled: boolean) => {
    if (enabled || !key.enabled) {
      void applyEnabled(key, enabled);
      return;
    }
    setConfirmState({
      title: t("apis.pauseKeyTitle"),
      message: t("apis.pauseKeyConfirm", { name: key.name || key.id }),
      confirmText: t("apis.pauseKeyConfirmText"),
      onConfirm: () => void applyEnabled(key, false),
    });
  };

  const revoke = (key: ApiKeyRecord) => {
    setConfirmState({
      title: t("apis.deleteKeyTitle"),
      message: t("apis.deleteKeyConfirm", { name: key.name || key.id }),
      confirmText: t("apis.deleteKeyConfirmText"),
      onConfirm: () => {
        void (async () => {
          setBusy([key.id], true);
          try {
            await deleteApiKey(secret, key.id);
            onNotice(t("apis.keyDeletedNotice", { name: key.name || key.id }));
            onMutated?.();
            await loadUsage();
          } catch (error) {
            onError(errorText(error, t("apis.failedDeleteKey")));
          } finally {
            setBusy([key.id], false);
          }
        })();
      },
    });
  };

  const selectedKeys = apiKeys.filter((key) => selectedIds.includes(key.id));

  /** Runs one request per key; a failure on one key does not stop the others. */
  const runBulk = async (keys: ApiKeyRecord[], action: (key: ApiKeyRecord) => Promise<unknown>, notice: (done: number) => string) => {
    if (keys.length === 0) return;
    setBulkBusy(true);
    setBusy(keys.map((key) => key.id), true);
    let done = 0;
    let lastError = "";
    for (const key of keys) {
      try {
        await action(key);
        done += 1;
      } catch (error) {
        lastError = errorText(error, t("apis.failedUpdateKey"));
      }
    }
    setBusy(keys.map((key) => key.id), false);
    setBulkBusy(false);
    if (lastError) onError(t("apis.bulk.partialFailure", { failed: keys.length - done, error: lastError }));
    if (done > 0) onNotice(notice(done));
    setSelectedIds([]);
    onMutated?.();
    await loadUsage();
  };

  const bulkSetEnabled = (enabled: boolean) => {
    const targets = selectedKeys.filter((key) => key.enabled !== enabled);
    if (targets.length === 0) return;
    const run = () =>
      void runBulk(targets, (key) => toggleApiKey(secret, key, enabled), (count) =>
        enabled ? t("apis.bulk.resumed", { count }) : t("apis.bulk.paused", { count }),
      );
    if (enabled) {
      run();
      return;
    }
    setConfirmState({
      title: t("apis.bulk.pauseTitle", { count: targets.length }),
      message: t("apis.bulk.pauseConfirm", { count: targets.length }),
      confirmText: t("apis.pauseKeyConfirmText"),
      onConfirm: run,
    });
  };

  const bulkRevoke = () => {
    const targets = selectedKeys;
    if (targets.length === 0) return;
    setConfirmState({
      title: t("apis.bulk.revokeTitle", { count: targets.length }),
      message: t("apis.bulk.revokeConfirm", { count: targets.length, names: targets.map((key) => key.name || key.id).join(", ") }),
      confirmText: t("apis.deleteKeyConfirmText"),
      onConfirm: () => void runBulk(targets, (key) => deleteApiKey(secret, key.id), (count) => t("apis.bulk.revoked", { count })),
    });
  };

  /* ---------------- Derived list ---------------- */

  const views = useMemo<ApiKeyRowView[]>(
    () =>
      apiKeys.map((key) => {
        const usage = usageById[key.id];
        return {
          key,
          usage,
          health: apiKeyHealth(key, usage),
          secret: getStoredApiKeySecret(key.id),
          selected: selectedIds.includes(key.id),
          busy: bulkBusy || busyIds.has(key.id),
        };
      }),
    // secretsVersion stands in for the browser-stored secrets read above.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [apiKeys, usageById, selectedIds, bulkBusy, busyIds, secretsVersion],
  );

  const searched = useMemo(() => {
    const query = search.trim().toLowerCase();
    if (!query) return views;
    return views.filter(({ key }) =>
      [key.name, key.id, key.policy?.team, ...(key.models || []), ...(key.policy?.endpoints || [])].some((value) => value?.toLowerCase().includes(query)),
    );
  }, [views, search]);

  const counts = useMemo(() => {
    const result = { all: searched.length, active: 0, attention: 0, paused: 0, nearBudget: 0 };
    for (const view of searched) {
      result[bucket(view.health)] += 1;
      if (view.health === "nearBudget") result.nearBudget += 1;
    }
    return result;
  }, [searched]);

  const totals = useMemo(() => {
    let requests = 0;
    let spend = 0;
    for (const view of views) {
      requests += view.usage?.requests_today || 0;
      spend += view.usage?.cost_usd_today || 0;
    }
    return { requests, spend, withSecret: views.filter((view) => view.secret).length };
  }, [views]);

  const visible = useMemo(() => {
    const list = filter === "all" ? [...searched] : searched.filter((view) => bucket(view.health) === filter);
    const byName = (a: ApiKeyRowView, b: ApiKeyRowView) => (a.key.name || a.key.id).localeCompare(b.key.name || b.key.id);
    if (sortMode === "name") return list.sort(byName);
    if (sortMode === "requests") return list.sort((a, b) => (b.usage?.requests_today || 0) - (a.usage?.requests_today || 0) || byName(a, b));
    if (sortMode === "spend") return list.sort((a, b) => (b.usage?.cost_usd_today || 0) - (a.usage?.cost_usd_today || 0) || byName(a, b));
    return list.sort((a, b) => HEALTH_ORDER.indexOf(a.health) - HEALTH_ORDER.indexOf(b.health) || byName(a, b));
  }, [searched, filter, sortMode]);

  const allVisibleSelected = visible.length > 0 && visible.every((view) => view.selected);
  const someVisibleSelected = visible.some((view) => view.selected);

  const toggleSelection = (ids: string[], select: boolean) =>
    setSelectedIds((current) => (select ? [...new Set([...current, ...ids])] : current.filter((id) => !ids.includes(id))));

  const toggleFilter = (next: KeyFilter) => setFilter((current) => (current === next ? "all" : next));
  const hasFilters = filter !== "all" || search.trim() !== "";
  const clearFilters = () => {
    setFilter("all");
    setSearch("");
  };

  const scrollTo = (id: string) => document.getElementById(id)?.scrollIntoView({ behavior: "smooth", block: "start" });

  const handlers: ApiKeyRowHandlers = {
    open: openEditModal,
    select: (key, select) => toggleSelection([key.id], select),
    setEnabled,
    copy: (text, id) => {
      copy(text, id);
      onNotice(id.startsWith("id_") ? t("apis.idCopied", { id: text }) : t("apis.secretCopied"));
    },
    showCapacity: (key) => setCapacityKeyId(key.id),
    useInExample: (key) => {
      const stored = getStoredApiKeySecret(key.id);
      if (stored) setExampleApiKey(stored);
      scrollTo("ap-quick-start");
    },
    revoke,
  };

  /* ---------------- Render ---------------- */

  const money = (value: number) =>
    new Intl.NumberFormat(i18n.language, { style: "currency", currency: "USD", maximumFractionDigits: 2 }).format(value || 0);
  const number = (value: number) => new Intl.NumberFormat(i18n.language).format(value || 0);
  const pending = loadingUsage && Object.keys(usageById).length === 0;

  const remoteOnline = remote
    ? (["cloudflare", "tailscale"] as const).filter((method) => remote[method].state === "online")
    : [];
  const remoteLabel = (method: "cloudflare" | "tailscale") => t(method === "cloudflare" ? "apis.tunnel.tunnel" : "apis.tunnel.tailscale");

  const capacityKey = capacityKeyId ? apiKeys.find((key) => key.id === capacityKeyId) : undefined;

  const keysSection = (
    <section className="ap-section" id="api-keys" aria-labelledby="ap-keys-title">
      <SectionHead
        leading={<span className="material-symbols-outlined cp-section-icon" aria-hidden>vpn_key</span>}
        title={<span id="ap-keys-title">{t("apis.apiKeys")}</span>}
        subtitle={hasFilters ? t("apis.keysShown", { shown: visible.length, total: apiKeys.length }) : t("apis.keysCount", { count: apiKeys.length })}
        meta={
          <ToneCounts
            label={t("apis.columns.status")}
            items={[
              { tone: "success", count: counts.active, label: t("apis.filter.active") },
              { tone: "danger", count: counts.attention, label: t("apis.filter.attention") },
              { tone: "neutral", count: counts.paused, label: t("apis.filter.paused") },
            ]}
          />
        }
      />
      {apiKeys.length === 0 ? (
        <EmptyPanel
          icon="vpn_key"
          title={t("apis.noApiKeys")}
          description={isRemoteHost ? t("apis.noKeysRemoteWarning") : t("apis.emptyHint")}
          action={<Button variant="primary" icon="add" onClick={openCreateModal}>{t("apis.createKey")}</Button>}
        />
      ) : visible.length === 0 ? (
        <EmptyPanel
          icon="filter_alt_off"
          title={t("apis.noMatch")}
          description={t("apis.noMatchHint")}
          action={hasFilters ? <Button variant="secondary" icon="filter_alt_off" onClick={clearFilters}>{t("apis.clearFilters")}</Button> : null}
        />
      ) : (
        <ApiKeyList
          rows={visible}
          handlers={handlers}
          allSelected={allVisibleSelected}
          someSelected={someVisibleSelected}
          onSelectAll={(select) => toggleSelection(visible.map((view) => view.key.id), select)}
        />
      )}
    </section>
  );

  return (
    <section className="cp-page ap-page">
      <PageStats
        label={t("apis.statsLabel")}
        items={[
          {
            key: "all",
            icon: "vpn_key",
            label: t("apis.stats.keys"),
            value: apiKeys.length,
            hint: t("apis.stats.keysHint", { count: totals.withSecret }),
            active: filter === "all",
            onClick: () => setFilter("all"),
          },
          {
            key: "active",
            icon: "check_circle",
            tone: "success",
            label: t("apis.filter.active"),
            value: counts.active,
            hint: counts.nearBudget ? t("apis.stats.nearBudgetHint", { count: counts.nearBudget }) : t("apis.stats.activeHint"),
            muted: counts.active === 0,
            active: filter === "active",
            onClick: () => toggleFilter("active"),
          },
          {
            key: "attention",
            icon: "error",
            tone: "danger",
            label: t("apis.filter.attention"),
            value: pending ? "—" : counts.attention,
            hint: t("apis.stats.attentionHint"),
            muted: counts.attention === 0,
            active: filter === "attention",
            onClick: () => toggleFilter("attention"),
          },
          {
            key: "paused",
            icon: "pause_circle",
            tone: "neutral",
            label: t("apis.filter.paused"),
            value: counts.paused,
            hint: t("apis.stats.pausedHint"),
            muted: counts.paused === 0,
            active: filter === "paused",
            onClick: () => toggleFilter("paused"),
          },
          {
            key: "today",
            icon: "monitoring",
            label: t("apis.stats.today"),
            value: pending ? "—" : number(totals.requests),
            hint: t("apis.stats.todayHint", { spend: money(totals.spend) }),
            mono: true,
            muted: totals.requests === 0,
          },
          {
            key: "remote",
            icon: "public",
            tone: remoteOnline.length ? "success" : "neutral",
            label: t("apis.stats.remote"),
            value: !remote || remote.loading ? "—" : remoteOnline.length ? t("apis.stats.remoteOn") : t("apis.stats.remoteOff"),
            hint: remoteOnline.length ? remoteOnline.map(remoteLabel).join(" · ") : t("apis.stats.remoteOffHint"),
            muted: remoteOnline.length === 0,
          },
        ]}
      />

      <Toolbar>
        <ToolbarRow>
          <FilterTabs
            label={t("apis.filterLabel")}
            value={filter}
            onChange={toggleFilter}
            items={[
              { value: "all", icon: "apps", label: t("apis.filter.all"), count: counts.all },
              { value: "active", icon: "check_circle", tone: "success", label: t("apis.filter.active"), count: counts.active },
              { value: "attention", icon: "error", tone: "danger", label: t("apis.filter.attention"), count: counts.attention },
              { value: "paused", icon: "pause_circle", tone: "neutral", label: t("apis.filter.paused"), count: counts.paused },
            ]}
          />
          <ToolbarEnd>
            <ToolButton icon="refresh" spinning={loadingUsage} disabled={loadingUsage} onClick={() => void loadUsage()} title={t("apis.refreshUsageHint")}>
              {t("apis.refreshUsage")}
            </ToolButton>
            <Button variant="primary" size="sm" icon="add" onClick={openCreateModal}>{t("apis.createKey")}</Button>
          </ToolbarEnd>
        </ToolbarRow>
        <ToolbarRow>
          <SearchField value={search} onChange={setSearch} placeholder={t("apis.searchPlaceholder")} />
          <ToolbarEnd>
            <SelectField
              label={t("apis.sortLabel")}
              value={sortMode}
              onChange={setSortMode}
              options={[
                { value: "status", label: t("apis.sort.status") },
                { value: "name", label: t("apis.sort.name") },
                { value: "requests", label: t("apis.sort.requests") },
                { value: "spend", label: t("apis.sort.spend") },
              ]}
            />
            <ToolButton icon="terminal" onClick={() => scrollTo("ap-quick-start")}>{t("apis.quickStart.jump")}</ToolButton>
            <GuideToggle open={guide.open} onToggle={guide.toggle} />
          </ToolbarEnd>
        </ToolbarRow>
      </Toolbar>

      <PageGuide
        open={guide.open}
        onDismiss={guide.dismiss}
        title={t("apis.guide.title")}
        steps={[
          { icon: "api", text: t("apis.guide.endpoint") },
          { icon: "vpn_key", text: t("apis.guide.keys") },
          { icon: "public", text: t("apis.guide.remote") },
        ]}
        legend={
          <>
            <LegendItem tone="success">{t("apis.health.active")}</LegendItem>
            <LegendItem tone="warning">{t("apis.health.nearBudget")}</LegendItem>
            <LegendItem tone="danger">{t("apis.filter.attention")}</LegendItem>
            <LegendItem tone="neutral">{t("apis.health.paused")}</LegendItem>
          </>
        }
      />

      {keysSection}

      <ApiEndpoints
        baseUrl={baseUrl}
        serverPort={serverPort}
        lan={{ allowed: allowLanAccess, ips: lanIPs, restartRequired: lanRestartRequired }}
        remote={remote}
        exposedWithoutKeys={isRemoteHost && apiKeys.length === 0}
        copied={copied}
        onCopy={copy}
        onLanHelp={() => setShowLanPortHelp(true)}
        onRemoteSetup={() => scrollTo("ap-remote")}
      />

      <section className="ap-section" id="ap-remote" aria-labelledby="ap-remote-title">
        <SectionHead
          leading={<span className="material-symbols-outlined cp-section-icon" aria-hidden>public</span>}
          title={<span id="ap-remote-title">{t("apis.remote.title")}</span>}
          subtitle={t("apis.remote.subtitle")}
        />
        <TunnelSection secret={secret} apiKeyCount={apiKeys.length} onError={onError} onNotice={onNotice} onSummary={setRemote} />
      </section>

      <ApiQuickStart
        apiKeys={apiKeys}
        modelOptions={modelOptions}
        baseUrl={baseUrl}
        curlExample={curlExample}
        exampleApiKey={exampleApiKey}
        onExampleApiKeyChange={setExampleApiKey}
        onExampleApiKeyIdChange={setExampleApiKeyId}
        exampleModel={exampleModel}
        onExampleModelChange={setExampleModel}
        copied={copied}
        onCopy={copy}
      />

      <BulkBar
        count={selectedIds.length}
        busy={bulkBusy}
        onClear={() => setSelectedIds([])}
        selectAll={
          allVisibleSelected
            ? null
            : { label: t("apis.selectAllCount", { count: visible.length }), onSelect: () => toggleSelection(visible.map((view) => view.key.id), true) }
        }
      >
        <BulkButton icon="play_circle" disabled={bulkBusy || !selectedKeys.some((key) => !key.enabled)} onClick={() => bulkSetEnabled(true)}>
          {t("apis.bulk.resume")}
        </BulkButton>
        <BulkButton icon="pause_circle" disabled={bulkBusy || !selectedKeys.some((key) => key.enabled)} onClick={() => bulkSetEnabled(false)}>
          {t("apis.bulk.pause")}
        </BulkButton>
        <BulkButton icon="delete" tone="danger" disabled={bulkBusy} onClick={bulkRevoke}>
          {t("apis.revoke")}
        </BulkButton>
      </BulkBar>

      <ApiKeyCreateModal
        open={showCreateModal}
        saving={saving}
        advanced={showAdvancedCreate}
        formData={formData}
        setFormData={setFormData}
        modelOptions={modelOptions}
        onToggleAdvanced={() => setShowAdvancedCreate((current) => !current)}
        onClose={closeCreateModal}
        onCreate={() => void handleCreate()}
      />

      <ApiKeyCreatedModal secret={createdSecret} copied={copied} onCopy={copy} onClose={() => setCreatedSecret(null)} />

      <ApiKeyEditModal
        open={editingKey !== null}
        saving={saving}
        keyId={editingKey?.id || ""}
        formData={formData}
        setFormData={setFormData}
        editSecret={editSecret}
        setEditSecret={setEditSecret}
        modelOptions={modelOptions}
        providers={providers}
        credentials={credentials}
        routes={routes}
        combos={combos}
        onClose={closeEditModal}
        onSubmit={(event) => void handleEdit(event)}
      />

      <Modal
        open={Boolean(capacityKey)}
        onClose={() => setCapacityKeyId(null)}
        title={t("apis.capacityTitle", { name: capacityKey?.name || capacityKey?.id || "" })}
        subtitle={t("apis.capacitySubtitle")}
        icon="speed"
        size="lg"
        footer={<Button variant="secondary" onClick={() => setCapacityKeyId(null)}>{t("common.close")}</Button>}
      >
        {capacityKey ? <ApiKeyCapacity usage={usageById[capacityKey.id]} /> : null}
      </Modal>

      <ConfirmDialog
        open={confirmState !== null}
        title={confirmState?.title || ""}
        message={confirmState?.message || ""}
        variant="danger"
        confirmText={confirmState?.confirmText || t("common.confirm")}
        cancelText={t("common.cancel")}
        onClose={() => setConfirmState(null)}
        onConfirm={() => {
          confirmState?.onConfirm();
          setConfirmState(null);
        }}
      />

      <LanPortHelpModal open={showLanPortHelp} port={serverPort} onClose={() => setShowLanPortHelp(false)} />
    </section>
  );
}
