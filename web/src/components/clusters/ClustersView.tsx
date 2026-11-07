import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";
import { Button, ConfirmDialog, cn } from "../ui";
import {
  AutoRefreshToggle,
  EmptyPanel,
  FilterTabs,
  GuideToggle,
  IconAction,
  LegendItem,
  LinkButton,
  PageGuide,
  PageStats,
  StatusPill,
  Tag,
  Toolbar,
  ToolbarEnd,
  ToolbarRow,
  usePageGuide,
  useStoredState,
  type PageStat,
} from "../page-kit";
import { compactNumber, formatAgo } from "../upstreams/health";
import {
  createCluster,
  createJoinToken,
  dissolveCluster,
  getClusterLedger,
  getClusterStatus,
  joinCluster,
  leaveCluster,
  renameLocalNode,
  revokeClusterNode,
  revokeJoinToken,
  syncCluster,
  updateBorrowing,
  updateClusterNode,
  updateLending,
  updateShared,
} from "./api";
import { SharedCatalogEditor, SyncedCatalog } from "./ClusterCatalog";
import { ClusterLedger, type LedgerDays } from "./ClusterLedger";
import { NodeEditModal, TokenCreatedModal, TokenCreateModal, type NodeEditTarget } from "./ClusterModals";
import { ClusterNodes } from "./ClusterNodes";
import { ClusterPolicies } from "./ClusterPolicies";
import { ClusterSetup } from "./ClusterSetup";
import { ClusterTokens } from "./ClusterTokens";
import type {
  BorrowingPolicy,
  ClusterNode,
  ClusterStatus,
  CreateClusterBody,
  CreatedJoinToken,
  CreateTokenBody,
  JoinClusterBody,
  JoinToken,
  LedgerRow,
  LendingPolicy,
  NodePatch,
  SharedCatalog,
  SnapshotApiKey,
  SnapshotCombo,
  SnapshotModel,
} from "./types";
import {
  LINK_STATES,
  POLL_INTERVAL_MS,
  buildModelOptions,
  clusterErrorMessage,
  fmtDateTime,
  fmtInt,
  fmtUsd,
  isAbortError,
  linkTone,
  nodeState,
  nodeTraffic,
  warningText,
} from "./utils";

type Tab = "nodes" | "tokens" | "catalog" | "policies" | "ledger";
const ALL_TABS: Tab[] = ["nodes", "tokens", "catalog", "policies", "ledger"];
const MEMBER_TABS: Tab[] = ["nodes", "catalog", "policies", "ledger"];
const TAB_ICON: Record<Tab, string> = { nodes: "dns", tokens: "key", catalog: "inventory_2", policies: "tune", ledger: "receipt_long" };
const LEDGER_DAYS: LedgerDays[] = ["1", "7"];
/** Warnings that make the next join or relay fail outright. */
const BLOCKING_WARNINGS = new Set(["master_key_missing", "hub_unreachable"]);

const NO_MODELS: SnapshotModel[] = [];
const NO_COMBOS: SnapshotCombo[] = [];
const NO_KEYS: SnapshotApiKey[] = [];

type ConfirmState = { title: string; message: string; confirmText: string; onConfirm: () => void };
/** null when the mutation failed (the error is already reported). */
type Mutation<T> = { value: T } | null;

type Props = {
  secret: string;
  /** Snapshot slices: the hub picks what to share from these, and they name lendable models. */
  models?: SnapshotModel[];
  combos?: SnapshotCombo[];
  apiKeys?: SnapshotApiKey[];
  onError: (message: string) => void;
  onNotice: (message: string) => void;
  onMutated?: () => void;
};

const knownLinkState = (state: string) => ((LINK_STATES as string[]).includes(state) ? state : "error");

function Warnings({ codes }: { codes: string[] }) {
  const { t } = useTranslation();
  if (codes.length === 0) return null;
  return (
    <ul className="cl-warnings" aria-label={t("clusters.warnings.label")}>
      {codes.map((code) => {
        const blocking = BLOCKING_WARNINGS.has(code);
        return (
          <li key={code} className={cn("cl-notice", blocking ? "cp-tone-danger" : "cp-tone-warning")}>
            <span className="material-symbols-outlined" aria-hidden>{blocking ? "error" : "warning"}</span>
            <p>{warningText(code, t)}</p>
          </li>
        );
      })}
    </ul>
  );
}

function LinkPanel({
  status,
  busy,
  now,
  onSync,
  onCreateToken,
}: {
  status: ClusterStatus;
  busy: string | null;
  now: number;
  onSync: () => void;
  onCreateToken: () => void;
}) {
  const { t, i18n } = useTranslation();
  const { copied, copy } = useCopyToClipboard();
  const hubUrl = status.cluster?.hub_url || "";
  const member = status.role === "member";
  const link = status.link;
  const state = knownLinkState(link?.state || "offline");
  const connectedAgo = link?.connected_at ? formatAgo(link.connected_at, i18n.language, now) : null;
  return (
    <section className="cp-panel cl-link" aria-label={t("clusters.link.label")}>
      <div className="cl-link-row">
        <span className="cl-link-label">{t("clusters.link.hubUrl")}</span>
        <code className="cl-link-url" title={hubUrl}>{hubUrl || "—"}</code>
        <IconAction
          icon={copied === "hub-url" ? "check" : "content_copy"}
          label={copied === "hub-url" ? t("common.copied") : t("clusters.link.copyUrl")}
          disabled={!hubUrl}
          onClick={() => copy(hubUrl, "hub-url")}
        />
        {status.cluster?.allow_insecure ? <Tag tone="warning" title={t("clusters.link.insecureHint")}>{t("clusters.link.insecure")}</Tag> : null}
        <span className="cl-link-spacer" />
        {member ? (
          <>
            <StatusPill tone={linkTone(state)} loading={state === "connecting"} title={t(`clusters.link.stateHint.${state}`)}>
              {t(`clusters.link.state.${state}`)}
            </StatusPill>
            {link && link.rtt_ms > 0 ? <span className="cl-link-meta">{t("clusters.link.rtt", { value: fmtInt(link.rtt_ms, i18n.language) })}</span> : null}
            {state === "connected" && connectedAgo ? (
              <span className="cl-link-meta" title={fmtDateTime(link?.connected_at || "", i18n.language)}>{t("clusters.link.connectedAgo", { time: connectedAgo })}</span>
            ) : null}
            <Button size="sm" variant="secondary" icon="sync" loading={busy === "sync"} disabled={busy !== null} onClick={onSync}>
              {t("clusters.syncNow")}
            </Button>
          </>
        ) : (
          <Button size="sm" icon="key" disabled={busy !== null} onClick={onCreateToken}>{t("clusters.tokens.create")}</Button>
        )}
      </div>
      <p className="cl-muted">{member ? t("clusters.link.memberHint") : t("clusters.link.hubHint")}</p>
      {member && link?.last_error ? (
        <p className="cl-inline-error" role="status">
          <span className="material-symbols-outlined" aria-hidden>error</span>
          <span>{t("clusters.link.lastError", { message: link.last_error })}</span>
        </p>
      ) : null}
    </section>
  );
}

/** Clusters page: set up, run and inspect a cluster of tproxy nodes. */
export function ClustersView({ secret, models = NO_MODELS, combos = NO_COMBOS, apiKeys = NO_KEYS, onError, onNotice, onMutated }: Props) {
  const { t, i18n } = useTranslation();
  const language = i18n.language;
  const guide = usePageGuide("clusters");

  const [status, setStatus] = useState<ClusterStatus>();
  const [loadError, setLoadError] = useState("");
  const [refreshing, setRefreshing] = useState(false);
  const [reloadToken, setReloadToken] = useState(0);
  const [loadedAt, setLoadedAt] = useState(0);
  const [autoRefresh, setAutoRefresh] = useStoredState<"on" | "off">("tproxy.clusters.autoRefresh", "on", ["on", "off"]);
  const [storedTab, setTab] = useStoredState<Tab>("tproxy.clusters.tab", "nodes", ALL_TABS);
  const [ledgerDays, setLedgerDays] = useStoredState<LedgerDays>("tproxy.clusters.ledgerDays", "1", LEDGER_DAYS);
  const [todayRows, setTodayRows] = useState<LedgerRow[]>([]);
  const [ledgerRows, setLedgerRows] = useState<LedgerRow[]>([]);
  const [ledgerError, setLedgerError] = useState("");
  const [ledgerLoaded, setLedgerLoaded] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [confirmState, setConfirmState] = useState<ConfirmState | null>(null);
  const [nodeEdit, setNodeEdit] = useState<NodeEditTarget | null>(null);
  const [tokenModalOpen, setTokenModalOpen] = useState(false);
  const [createdToken, setCreatedToken] = useState<CreatedJoinToken | null>(null);
  const [now, setNow] = useState(() => Date.now());

  // Relative times ("2 min ago") stay current even with auto-refresh off.
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 30_000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    const { signal } = controller;
    setRefreshing(true);
    void (async () => {
      try {
        const next = await getClusterStatus(secret, signal);
        if (signal.aborted) return;
        setStatus(next);
        setLoadError("");
        if (next.role !== "hub" && (next.role !== "member" || next.link?.state === "revoked")) {
          setTodayRows([]);
          setLedgerRows([]);
          setLedgerError("");
          setLedgerLoaded(false);
          return;
        }
        try {
          const today = await getClusterLedger(secret, 1, signal);
          const rows = ledgerDays === "7" ? await getClusterLedger(secret, 7, signal) : today;
          if (signal.aborted) return;
          setTodayRows(today);
          setLedgerRows(rows);
          setLedgerError("");
        } catch (cause) {
          if (signal.aborted || isAbortError(cause)) return;
          setLedgerError(clusterErrorMessage(cause, t, t("clusters.ledger.loadFailed")));
        } finally {
          if (!signal.aborted) setLedgerLoaded(true);
        }
      } catch (cause) {
        if (signal.aborted || isAbortError(cause)) return;
        setLoadError(clusterErrorMessage(cause, t, t("clusters.loadFailed")));
      } finally {
        if (!signal.aborted) {
          setRefreshing(false);
          setLoadedAt(Date.now());
          setNow(Date.now());
        }
      }
    })();
    return () => controller.abort();
  }, [secret, reloadToken, ledgerDays, t]);

  // Poll 10 s after each settled load, success or not.
  useEffect(() => {
    if (autoRefresh !== "on" || !loadedAt) return;
    const timer = window.setTimeout(() => setReloadToken((value) => value + 1), Math.max(0, loadedAt + POLL_INTERVAL_MS - Date.now()));
    return () => window.clearTimeout(timer);
  }, [autoRefresh, loadedAt]);

  const reload = useCallback(() => setReloadToken((value) => value + 1), []);

  /** Runs one admin call: success → notice, re-fetch and snapshot refresh; failure → friendly error. */
  const mutate = useCallback(
    async <T,>(key: string, action: () => Promise<T>, success: string, fallback: string): Promise<Mutation<T>> => {
      setBusy(key);
      try {
        const value = await action();
        onNotice(success);
        reload();
        onMutated?.();
        return { value };
      } catch (cause) {
        onError(clusterErrorMessage(cause, t, fallback));
        return null;
      } finally {
        setBusy(null);
      }
    },
    [onError, onMutated, onNotice, reload, t],
  );

  const modelOptions = useMemo(() => buildModelOptions(status, models), [status, models]);

  /* ---------------- Actions ---------------- */

  const handleCreate = async (body: CreateClusterBody) =>
    (await mutate("create", () => createCluster(secret, body), t("clusters.notice.created", { name: body.name }), t("clusters.errors.createFailed"))) !== null;

  const handleJoin = async (body: JoinClusterBody) =>
    (await mutate("join", () => joinCluster(secret, body), t("clusters.notice.joined"), t("clusters.errors.joinFailed"))) !== null;

  const handleSync = () => void mutate("sync", () => syncCluster(secret), t("clusters.notice.synced"), t("clusters.errors.syncFailed"));

  const saveLending = async (policy: LendingPolicy) =>
    (await mutate("lending", () => updateLending(secret, policy), t("clusters.notice.lendingSaved"), t("clusters.errors.saveFailed"))) !== null;

  const saveBorrowing = async (policy: BorrowingPolicy) =>
    (await mutate("borrowing", () => updateBorrowing(secret, policy), t("clusters.notice.borrowingSaved"), t("clusters.errors.saveFailed"))) !== null;

  const saveShared = async (shared: SharedCatalog) =>
    (await mutate("shared", () => updateShared(secret, shared), t("clusters.notice.sharedSaved"), t("clusters.errors.saveFailed"))) !== null;

  const createToken = async (body: CreateTokenBody) => {
    const result = await mutate("token:create", () => createJoinToken(secret, body), t("clusters.notice.tokenCreated"), t("clusters.errors.tokenFailed"));
    if (!result) return;
    setTokenModalOpen(false);
    setCreatedToken(result.value);
    setTab("tokens");
  };

  const confirmRevokeToken = (token: JoinToken) => {
    const name = token.label || token.id;
    setConfirmState({
      title: t("clusters.tokens.revokeTitle"),
      message: t("clusters.tokens.revokeConfirm", { name }),
      confirmText: t("clusters.tokens.revoke"),
      onConfirm: () =>
        void mutate(`token:${token.id}`, () => revokeJoinToken(secret, token.id), t("clusters.notice.tokenRevoked", { name }), t("clusters.errors.revokeFailed")),
    });
  };

  const saveNode = async (patch: NodePatch) => {
    if (!nodeEdit) return;
    const name = patch.name || "";
    const result =
      nodeEdit.mode === "peer"
        ? await mutate(`node:${nodeEdit.node.id}`, () => updateClusterNode(secret, nodeEdit.node.id, patch), t("clusters.notice.nodeUpdated", { name }), t("clusters.errors.saveFailed"))
        : await mutate("rename", () => renameLocalNode(secret, name), t("clusters.notice.renamed", { name }), t("clusters.errors.saveFailed"));
    if (result) setNodeEdit(null);
  };

  const confirmRevokeNode = (node: ClusterNode) => {
    const name = node.name || node.id;
    setConfirmState({
      title: t("clusters.nodes.revokeTitle"),
      message: t("clusters.nodes.revokeConfirm", { name }),
      confirmText: t("clusters.nodes.revoke"),
      onConfirm: () =>
        void mutate(`node:${node.id}`, () => revokeClusterNode(secret, node.id), t("clusters.notice.nodeRevoked", { name }), t("clusters.errors.revokeFailed")),
    });
  };

  const confirmLeave = () =>
    setConfirmState({
      title: t("clusters.danger.leaveTitle"),
      message: t("clusters.danger.leaveConfirm", { name: status?.cluster?.name || "" }),
      confirmText: t("clusters.danger.leave"),
      onConfirm: () => void mutate("leave", () => leaveCluster(secret), t("clusters.notice.left"), t("clusters.errors.leaveFailed")),
    });

  const confirmDissolve = () =>
    setConfirmState({
      title: t("clusters.danger.dissolveTitle"),
      message: t("clusters.danger.dissolveConfirm", { name: status?.cluster?.name || "" }),
      confirmText: t("clusters.danger.dissolve"),
      onConfirm: () => void mutate("dissolve", () => dissolveCluster(secret), t("clusters.notice.dissolved"), t("clusters.errors.dissolveFailed")),
    });

  /* ---------------- Shared chrome ---------------- */

  const modals = (
    <>
      <NodeEditModal
        target={nodeEdit}
        saving={busy === "rename" || (nodeEdit?.mode === "peer" && busy === `node:${nodeEdit.node.id}`)}
        onSave={(patch) => void saveNode(patch)}
        onClose={() => setNodeEdit(null)}
      />
      <TokenCreateModal open={tokenModalOpen} saving={busy === "token:create"} onCreate={(body) => void createToken(body)} onClose={() => setTokenModalOpen(false)} />
      <TokenCreatedModal created={createdToken} onClose={() => setCreatedToken(null)} />
      <ConfirmDialog
        open={Boolean(confirmState)}
        onClose={() => setConfirmState(null)}
        onConfirm={() => {
          const action = confirmState?.onConfirm;
          setConfirmState(null);
          action?.();
        }}
        title={confirmState?.title || ""}
        message={confirmState?.message || ""}
        confirmText={confirmState?.confirmText}
        cancelText={t("common.cancel")}
        variant="danger"
      />
    </>
  );

  // Compact on purpose: it shares a row with five tabs.
  const toolbarEnd = (
    <ToolbarEnd>
      {loadError && status ? (
        <span className="cl-stale" role="status" title={loadError}>
          <span className="material-symbols-outlined" aria-hidden>sync_problem</span>
          {t("clusters.refreshFailed")}
        </span>
      ) : null}
      <AutoRefreshToggle
        enabled={autoRefresh === "on"}
        nextAt={loadedAt + POLL_INTERVAL_MS}
        title={loadedAt ? `${t("clusters.autoRefreshHint")} · ${t("clusters.updatedAt", { time: new Date(loadedAt).toLocaleTimeString(language) })}` : t("clusters.autoRefreshHint")}
        onToggle={() => setAutoRefresh((value) => (value === "on" ? "off" : "on"))}
      />
      <IconAction icon="refresh" label={t("common.refresh")} bordered spinning={refreshing} onClick={reload} />
      <GuideToggle open={guide.open} onToggle={guide.toggle} />
    </ToolbarEnd>
  );

  const pageGuide = (
    <PageGuide
      open={guide.open}
      onDismiss={guide.dismiss}
      title={t("clusters.guide.title")}
      steps={[
        { icon: "hub", text: t("clusters.guide.hub") },
        { icon: "group_add", text: t("clusters.guide.join") },
        { icon: "volunteer_activism", text: t("clusters.guide.share") },
      ]}
      legend={
        <>
          <LegendItem tone="success">{t("clusters.nodes.state.online")}</LegendItem>
          <LegendItem tone="warning">{t("clusters.nodes.state.offline")}</LegendItem>
          <LegendItem tone="danger">{t("clusters.nodes.state.revoked")}</LegendItem>
        </>
      }
    />
  );

  /* ---------------- First load ---------------- */

  if (!status) {
    return (
      <section className="cp-page cl-page">
        {loadError ? (
          <EmptyPanel
            icon="cloud_off"
            title={t("clusters.loadFailed")}
            description={<span role="alert">{loadError}</span>}
            action={<Button variant="secondary" icon="refresh" loading={refreshing} onClick={reload}>{t("clusters.retry")}</Button>}
          />
        ) : (
          <EmptyPanel loading title={t("clusters.loading")} />
        )}
      </section>
    );
  }

  /* ---------------- Standalone ---------------- */

  if (status.role === "standalone") {
    return (
      <section className="cp-page cl-page">
        <Toolbar>
          <ToolbarRow>
            <div className="cl-self">
              <span className="material-symbols-outlined" aria-hidden>dns</span>
              <span className="cl-self-copy">
                <small>{t("clusters.self.label")}</small>
                <strong title={status.node.id}>{status.node.name || status.node.id || "—"}</strong>
              </span>
              <Tag tone="neutral">{t("clusters.role.standalone")}</Tag>
              {status.node.version ? <span className="cl-muted">{t("clusters.self.version", { version: status.node.version })}</span> : null}
              <LinkButton disabled={busy !== null} onClick={() => setNodeEdit({ mode: "self", name: status.node.name })}>
                {t("clusters.self.rename")}
              </LinkButton>
            </div>
            {toolbarEnd}
          </ToolbarRow>
        </Toolbar>
        {pageGuide}
        <Warnings codes={status.warnings} />
        <ClusterSetup status={status} modelOptions={modelOptions} busy={busy} onCreate={handleCreate} onJoin={handleJoin} />
        {modals}
      </section>
    );
  }

  /* ---------------- Hub / member ---------------- */

  const hub = status.role === "hub";
  const clusterName = status.cluster?.name || status.cluster?.id || t("clusters.unnamed");
  const linkState = knownLinkState(status.link?.state || "offline");
  const revoked = !hub && linkState === "revoked";
  const activeNodes = status.nodes.filter((node) => node.status !== "revoked" && node.status !== "left");
  const onlineNodes = activeNodes.filter((node) => nodeState(node) === "online").length;
  const goneNodes = status.nodes.length - activeNodes.length;
  const lendingNodes = activeNodes.filter((node) => node.lending_enabled).length;
  const catalog = status.catalog;
  const conflicts = catalog?.conflicts.length || 0;
  const traffic = nodeTraffic(todayRows, status.node.id);
  const tabs = hub ? ALL_TABS : MEMBER_TABS;
  const tab: Tab = tabs.includes(storedTab) ? storedTab : "nodes";
  const sharedCount = hub ? status.shared?.models.length || 0 : catalog?.models.length || 0;

  const trafficTile = (key: "lent" | "borrowed", icon: string): PageStat => {
    const value = traffic[key];
    return {
      key,
      icon,
      label: t(`clusters.stats.${key}`),
      value: ledgerLoaded && !revoked ? fmtInt(value.requests, language) : "—",
      hint: revoked ? t("clusters.ledger.unavailable") : t("clusters.stats.trafficHint", { tokens: compactNumber(value.input_tokens + value.output_tokens), cost: fmtUsd(value.cost_usd, language) }),
      muted: value.requests === 0,
      onClick: () => setTab("ledger"),
      title: t("clusters.stats.openLedger"),
    };
  };

  // Members also get a tile for their link to the hub.
  const linkTile: PageStat | null = hub
    ? null
    : {
        key: "link",
        icon: "link",
        tone: linkTone(linkState),
        label: t("clusters.stats.link"),
        value: <span className="cl-stat-name">{t(`clusters.link.state.${linkState}`)}</span>,
        hint: status.link?.rtt_ms ? t("clusters.link.rtt", { value: fmtInt(status.link.rtt_ms, language) }) : t("clusters.stats.linkNoRtt"),
      };

  const stats: PageStat[] = [
    {
      key: "cluster",
      icon: "hub",
      label: t("clusters.stats.cluster"),
      value: <span className="cl-stat-name" title={clusterName}>{clusterName}</span>,
      hint: hub
        ? t("clusters.stats.clusterHintHub")
        : t("clusters.stats.clusterHintMember", { time: formatAgo(status.cluster?.joined_at, language, now) || "—" }),
      title: status.cluster?.id,
    },
    ...(linkTile ? [linkTile] : []),
    {
      key: "nodes",
      icon: "dns",
      tone: activeNodes.length && onlineNodes < activeNodes.length ? "warning" : "success",
      label: t("clusters.stats.nodes"),
      value: `${onlineNodes}/${activeNodes.length}`,
      hint: goneNodes
        ? t("clusters.stats.nodesHintGone", { lending: lendingNodes, gone: goneNodes })
        : t("clusters.stats.nodesHint", { count: lendingNodes }),
      onClick: () => setTab("nodes"),
      title: t("clusters.stats.openNodes"),
    },
    {
      key: "catalog",
      icon: hub ? "share" : "sync",
      tone: conflicts ? "warning" : undefined,
      label: hub ? t("clusters.stats.shared") : t("clusters.stats.synced"),
      value: sharedCount,
      hint: hub
        ? t("clusters.stats.sharedHint", { combos: status.shared?.combos.length || 0, keys: status.shared?.api_keys.length || 0 })
        : conflicts
          ? t("clusters.stats.conflicts", { count: conflicts })
          : t("clusters.stats.noConflicts"),
      muted: sharedCount === 0,
      onClick: () => setTab("catalog"),
      title: t("clusters.stats.openCatalog"),
    },
    trafficTile("lent", "north_east"),
    trafficTile("borrowed", "south_west"),
  ];

  const tabCount = (value: Tab) => {
    if (value === "nodes") return status.nodes.length;
    if (value === "catalog") return hub ? (status.shared?.models.length || 0) + (status.shared?.combos.length || 0) + (status.shared?.api_keys.length || 0) : conflicts || undefined;
    return undefined;
  };

  return (
    <section className="cp-page cl-page">
      {revoked ? (
        <div className="cl-banner cp-tone-danger" role="alert">
          <span className="material-symbols-outlined" aria-hidden>block</span>
          <div>
            <strong>{t("clusters.revoked.title")}</strong>
            <p>{t("clusters.revoked.desc", { name: clusterName })}</p>
          </div>
          <Button variant="danger" icon="logout" loading={busy === "leave"} disabled={busy !== null} onClick={confirmLeave}>
            {t("clusters.revoked.leave")}
          </Button>
        </div>
      ) : null}

      <PageStats label={t("clusters.statsLabel")} items={stats} />

      <LinkPanel status={status} busy={busy} now={now} onSync={handleSync} onCreateToken={() => setTokenModalOpen(true)} />

      <Warnings codes={status.warnings} />

      <Toolbar>
        <ToolbarRow>
          <FilterTabs
            label={t("clusters.tabs.label")}
            value={tab}
            onChange={setTab}
            items={tabs.map((value) => ({
              value,
              icon: TAB_ICON[value],
              label: value === "catalog" && !hub ? t("clusters.tabs.synced") : t(`clusters.tabs.${value}`),
              count: tabCount(value),
              tone: value === "catalog" && !hub && conflicts ? "warning" : undefined,
            }))}
          />
          {toolbarEnd}
        </ToolbarRow>
      </Toolbar>

      {pageGuide}

      {tab === "nodes" ? (
        <ClusterNodes
          nodes={status.nodes}
          role={status.role}
          now={now}
          busy={busy}
          onEdit={(node) => setNodeEdit({ mode: "peer", node })}
          onRevoke={confirmRevokeNode}
          onRenameSelf={() => setNodeEdit({ mode: "self", name: status.node.name })}
        />
      ) : tab === "tokens" ? (
        <ClusterTokens
          secret={secret}
          refreshKey={loadedAt}
          now={now}
          busy={busy}
          onCreate={() => setTokenModalOpen(true)}
          onRevoke={confirmRevokeToken}
        />
      ) : tab === "catalog" ? (
        hub ? (
          <SharedCatalogEditor shared={status.shared} models={models} combos={combos} apiKeys={apiKeys} busy={busy} onSave={saveShared} />
        ) : (
          <SyncedCatalog catalog={catalog} busy={busy} onSync={handleSync} />
        )
      ) : tab === "policies" ? (
        <ClusterPolicies
          lending={status.lending}
          borrowing={status.borrowing}
          modelOptions={modelOptions}
          busy={busy}
          onSaveLending={saveLending}
          onSaveBorrowing={saveBorrowing}
        />
      ) : (
        <ClusterLedger
          available={!revoked}
          rows={ledgerRows}
          days={ledgerDays}
          onDaysChange={setLedgerDays}
          selfId={status.node.id}
          loaded={ledgerLoaded}
          error={ledgerError}
        />
      )}

      <section className="cp-panel cl-danger" aria-labelledby="cl-danger-title">
        <div>
          <h3 id="cl-danger-title">{t("clusters.danger.title")}</h3>
          <p>{hub ? t("clusters.danger.dissolveDesc") : t("clusters.danger.leaveDesc")}</p>
        </div>
        <Button
          variant="danger"
          icon={hub ? "delete_forever" : "logout"}
          loading={busy === (hub ? "dissolve" : "leave")}
          disabled={busy !== null}
          onClick={hub ? confirmDissolve : confirmLeave}
        >
          {hub ? t("clusters.danger.dissolve") : t("clusters.danger.leave")}
        </Button>
      </section>

      {modals}
    </section>
  );
}
