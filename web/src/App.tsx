import { useCallback, useEffect, useMemo, useState } from "react";
import { Navigate, Route, Routes, useLocation, useNavigate, useParams } from "react-router-dom";
import { Sidebar } from "./components/Sidebar";
import { MatrixRain } from "./components/MatrixRain";
import { useViewportWidth } from "./hooks/useViewportWidth";
import { Header } from "./components/Header";
import {
  Badge,
  Button,
  Card,
  Input,
  cn,
} from "./components/ui";
import { resolveChatApiKey } from "./devDefaults";
import { AuthLoadingView } from "./components/auth/AuthLoadingView";
import { LoginView } from "./components/auth/LoginView";
import {
  clearStoredManagementSecret,
  getStoredManagementSecret,
  setStoredManagementSecret,
  validateManagementSecret,
} from "./lib/auth";
import { ChatView } from "./components/chat/ChatView";
import { useChatModels } from "./components/chat/useChatModels";
import { ProvidersView } from "./components/providers/ProvidersView";
import { ProviderLogo } from "./components/providers/ProviderLogo";
import { QuotaTrackerView } from "./components/quota/QuotaTrackerView";
import { UsageView } from "./components/usage/UsageView";
import { TokenSaverView } from "./components/token-saver/TokenSaverView";
import { FreeTiersView } from "./components/free-tiers/FreeTiersView";
import { ApisView } from "./components/apis/ApisView";
import { buildExampleModelOptions } from "./components/apis/utils";
import { OverviewApiKeysCard } from "./components/overview/OverviewApiKeysCard";
import { ProxyPoolsView } from "./components/proxy-pools/ProxyPoolsView";
import { CombosView } from "./components/combos/CombosView";
import { MappingView } from "./components/mapping/MappingView";
import { ModelsView } from "./components/models/ModelsView";
import { CLIToolDetailView } from "./components/cli-tools/CLIToolDetailView";
import { CLIToolsView } from "./components/cli-tools/CLIToolsView";
import { LogsView } from "./components/logs/LogsView";
import { SettingsView } from "./components/settings/SettingsView";
import { fetchAuditEvents, type AuditEvent } from "./components/logs/api";
import { matchRoute } from "./navigation";
import { useRequestLogStream, type RequestLog } from "./hooks/useRequestLogStream";

/* ============================================================
   Types — unchanged from the original control center
   ============================================================ */
type Provider = {
  ID: string;
  Type: string;
  Name: string;
  BaseURL: string;
  Enabled: boolean;
  Status?: string;
  LastError?: string;
  LastChecked?: string;
  OAuth?: Record<string, unknown> | null;
  ProxyPoolIDs?: string[];
};

type ProxyPool = {
  id: string;
  name: string;
  url: string;
  enabled: boolean;
  status: string;
  last_error?: string;
  usage_count: number;
};

type Model = {
  ID: string;
  DisplayName: string;
  Aliases: string[];
  Enabled: boolean;
  Capabilities: string[];
  RewriteResponseModel: boolean;
};

type Route = {
  ID: string;
  ProviderID: string;
  UpstreamModel: string;
  Priority: number;
  Weight?: number;
  Enabled: boolean;
};

type Credential = {
  id: string;
  label: string;
  email?: string;
  auth_type: string;
  enabled: boolean;
  status?: string;
  priority?: number;
  cooldown_until?: string;
  last_error_code?: string;
  last_error?: string;
  proxy_pool_ids?: string[];
  last_used_at?: string;
  consecutive_use_count?: number;
  created_at?: string;
};

type Snapshot = {
  providers: Provider[] | null;
  proxy_pools: ProxyPool[] | null;
  models: Model[] | null;
  aliases: { alias: string; public_model_id: string; api_key_id?: string; team_id?: string; enabled: boolean }[];
  combos: { id: string; display_name: string; enabled: boolean; capabilities: string[]; items: { public_model_id: string; route_target_id?: string }[] }[];
  routes: Record<string, Route[]>;
  credentials: Record<string, Credential[]>;
  api_keys: { id: string; name: string; models: string[]; enabled: boolean; policy?: { limits?: Record<string, number>; endpoints?: string[]; team?: string } }[];
  usage: {
    requests: number;
    errors: number;
    input_tokens: number;
    output_tokens: number;
    tokens_saved: number;
    estimated_cost_usd: number;
  };
};

const emptySnapshot: Snapshot = {
  providers: [],
  proxy_pools: [],
  models: [],
  aliases: [],
  combos: [],
  routes: {},
  credentials: {},
  api_keys: [],
  usage: { requests: 0, errors: 0, input_tokens: 0, output_tokens: 0, tokens_saved: 0, estimated_cost_usd: 0 },
};

function compactNumber(value: number) {
  return new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 }).format(value || 0);
}

function usd(value: number) {
  return new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", maximumFractionDigits: 4 }).format(value || 0);
}

function statusBadge(status?: string, enabled = true) {
  if (!enabled) return <Badge variant="default">disabled</Badge>;
  if (status === "healthy") return <Badge variant="success" dot>healthy</Badge>;
  if (status && status !== "unknown") return <Badge variant="warning" dot>{status}</Badge>;
  return <Badge variant="default">unknown</Badge>;
}

function App() {
  const location = useLocation();
  const navigate = useNavigate();
  const activeRoute = matchRoute(location.pathname);
  const [logs, setLogs] = useState<RequestLog[]>([]);
  const [audit, setAudit] = useState<AuditEvent[]>([]);
  const [discovered, setDiscovered] = useState<Record<string, { id: string; name?: string }[]>>({});
  const [secret, setSecret] = useState("");
  const [authState, setAuthState] = useState<"checking" | "authenticated" | "unauthenticated">("checking");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [sidebarExpanded, setSidebarExpanded] = useState(true);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const viewportWidth = useViewportWidth();
  const [providerSearch, setProviderSearch] = useState("");
  const [apiKey, setApiKey] = useState(() => resolveChatApiKey());

  const [snapshot, setSnapshot] = useState<Snapshot>(emptySnapshot);
  const [gatewayOnline, setGatewayOnline] = useState(true);
  const [healthCheckAllBusy, setHealthCheckAllBusy] = useState(false);
  const authHeaders = useMemo<Record<string, string>>(() => ({ ...(secret ? { Authorization: `Bearer ${secret}` } : {}) }), [secret]);
  const logsStreamEnabled = location.pathname === "/logs" || location.pathname.startsWith("/logs/");

  useEffect(() => {
    if (!notice) return;
    const timer = window.setTimeout(() => setNotice(""), 5000);
    return () => window.clearTimeout(timer);
  }, [notice]);

  const applyLogUpdate = useCallback((items: RequestLog[]) => {
    setLogs(items);
  }, []);

  useRequestLogStream(secret, logsStreamEnabled && authState === "authenticated", applyLogUpdate);

  const logout = useCallback(() => {
    clearStoredManagementSecret();
    setSecret("");
    setAuthState("unauthenticated");
    setSnapshot(emptySnapshot);
    setError("");
    setNotice("");
  }, []);

  const login = useCallback(async (nextSecret: string) => {
    const result = await validateManagementSecret(nextSecret);
    if (!result.ok) {
      throw new Error(result.message);
    }
    setStoredManagementSecret(nextSecret);
    setSecret(nextSecret);
    setAuthState("authenticated");
    setError("");
  }, []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const stored = getStoredManagementSecret();
      if (!stored) {
        if (!cancelled) setAuthState("unauthenticated");
        return;
      }
      const result = await validateManagementSecret(stored);
      if (cancelled) return;
      if (result.ok) {
        setSecret(stored);
        setAuthState("authenticated");
      } else {
        clearStoredManagementSecret();
        setAuthState("unauthenticated");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const load = useCallback(async () => {
    if (authState !== "authenticated" || !secret) return;
    setLoading(true);
    setError("");
    try {
      const health = await fetch("/healthz");
      setGatewayOnline(health.ok);

      const response = await fetch("/api/admin/snapshot", { headers: authHeaders });
      const data = await response.json();
      if (!response.ok) {
        const code = data?.error?.code;
        if (code === "invalid_management_secret") {
          logout();
          throw new Error("Phiên đăng nhập hết hạn hoặc secret không hợp lệ.");
        }
        throw new Error(data?.error?.message || `HTTP ${response.status}`);
      }
      setSnapshot(data);
      setStoredManagementSecret(secret);
      void refreshAudit();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Unable to load tproxy state");
    } finally {
      setLoading(false);
    }
  }, [authHeaders, authState, logout, secret]);

  useEffect(() => {
    setMobileNavOpen(false);
  }, [location.pathname]);

  useEffect(() => {
    if (authState !== "authenticated") {
      setMobileNavOpen(false);
    }
  }, [authState]);

  useEffect(() => {
    if (authState === "authenticated") {
      void load();
    }
  }, [authState, load]);

  const refreshAudit = useCallback(async () => {
    try {
      const items = await fetchAuditEvents(secret);
      setAudit(items);
    } catch {
      // Silent — full error is surfaced by the snapshot loader.
    }
  }, [secret]);

  const adminRequest = useCallback(async (path: string, method: string, body?: unknown) => {
    setNotice("");
    setError("");
    const response = await fetch(path, { method, headers: { ...(secret ? { Authorization: `Bearer ${secret}` } : {}), ...(body ? { "Content-Type": "application/json" } : {}) }, body: body ? JSON.stringify(body) : undefined });
    const data = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(data?.error?.message || `HTTP ${response.status}`);
    return data;
  }, [secret]);

  const checkProvider = async (id: string) => {
    try { const result = await adminRequest(`/api/admin/providers/${encodeURIComponent(id)}/health`, "POST"); setNotice(result.ok ? `${id} is healthy` : `${id} health check failed`); await load(); } catch (cause) { setError(cause instanceof Error ? cause.message : "Provider health check failed"); }
  };
  const checkAllProviders = async () => {
    const ids = (snapshot.providers || []).map((provider) => provider.ID);
    if (ids.length === 0) {
      setError("No providers configured");
      return;
    }
    setHealthCheckAllBusy(true);
    let ok = 0;
    let failed = 0;
    try {
      for (const id of ids) {
        try {
          const result = await adminRequest(`/api/admin/providers/${encodeURIComponent(id)}/health`, "POST");
          if (result.ok) ok++;
          else failed++;
        } catch {
          failed++;
        }
      }
      setNotice(`Health check finished: ${ok} healthy, ${failed} failed`);
      await load();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Health check failed");
    } finally {
      setHealthCheckAllBusy(false);
    }
  };
  const discoverProvider = async (id: string) => {
    try { const result = await adminRequest(`/api/admin/providers/${encodeURIComponent(id)}/models`, "GET"); setDiscovered((current) => ({ ...current, [id]: result.data || [] })); setNotice(`Discovered ${result.data?.length || 0} models from ${id}`); await load(); } catch (cause) { setError(cause instanceof Error ? cause.message : "Model discovery failed"); }
  };
  const remove = async (path: string, label: string) => {
    if (!window.confirm(`Delete ${label}?`)) return;
    try { await adminRequest(path, "DELETE"); setNotice(`${label} deleted`); await load(); } catch (cause) { setError(cause instanceof Error ? cause.message : "Delete failed"); }
  };

  const activeCredentials = useMemo(
    () => Object.values(snapshot.credentials || {}).flat().filter((item) => item.enabled).length,
    [snapshot.credentials],
  );

  const quotaCredentials = useMemo(() => {
    const providersById = Object.fromEntries((snapshot.providers || []).map((provider) => [provider.ID, provider]));
    return Object.entries(snapshot.credentials || {}).flatMap(([providerId, items]) =>
      (items || []).map((credential) => ({
        id: credential.id,
        providerId,
        providerType: providersById[providerId]?.Type || "",
        label: credential.label,
        email: credential.email,
        enabled: credential.enabled,
        auth_type: credential.auth_type,
        created_at: credential.created_at,
      })),
    );
  }, [snapshot.credentials, snapshot.providers]);

  const online = gatewayOnline;
  const isMobileSidebar = viewportWidth < 1024;
  const isNarrowSidebar =
    isMobileSidebar || (viewportWidth < 1280 ? !sidebarExpanded : sidebarCollapsed);

  const toggleSidebar = () => {
    if (isMobileSidebar) {
      return;
    }
    if (viewportWidth >= 1280) {
      setSidebarCollapsed((current) => !current);
      return;
    }
    if (viewportWidth >= 1024) {
      setSidebarExpanded((current) => !current);
    }
  };

  const sidebarCollapseLabel = isNarrowSidebar ? "Mở rộng menu" : "Thu nhỏ menu";

  if (authState === "checking") {
    return (
      <div className="app-shell app-shell-auth">
        <MatrixRain />
        <AuthLoadingView />
      </div>
    );
  }

  if (authState === "unauthenticated") {
    return (
      <div className="app-shell app-shell-auth">
        <MatrixRain />
        <LoginView onLogin={login} />
      </div>
    );
  }

  return (
    <div className="app-shell">
      <MatrixRain />
      {isMobileSidebar && mobileNavOpen ? (
        <button
          type="button"
          className="sidebar-backdrop"
          aria-label="Đóng menu"
          onClick={() => setMobileNavOpen(false)}
        />
      ) : null}
      <div className={cn("sidebar-desktop", isNarrowSidebar && "is-narrow", isMobileSidebar && "is-mobile", mobileNavOpen && "is-open")}>
        <Sidebar
          online={online}
          collapsed={isNarrowSidebar}
          onToggleCollapse={isMobileSidebar ? undefined : toggleSidebar}
          collapseLabel={sidebarCollapseLabel}
          onClose={() => setMobileNavOpen(false)}
          onLogout={logout}
        />
      </div>

      <main className="main-area">
        <div className="landing-grid" aria-hidden="true" style={{ position: "absolute", inset: 0, pointerEvents: "none", zIndex: -1 }} />
        <Header
          title={activeRoute.title}
          description={activeRoute.description}
          icon={activeRoute.icon}
          onRefresh={load}
          loading={loading}
          onLogout={logout}
          onOpenNav={isMobileSidebar ? () => setMobileNavOpen(true) : undefined}
          extra={
            activeRoute.id === "providers" ? (
              <Input
                icon="search"
                placeholder="Search providers..."
                value={providerSearch}
                onChange={(e) => setProviderSearch(e.target.value)}
                style={{ width: 220 }}
                aria-label="Search providers"
              />
            ) : undefined
          }
        />

        <div className={cn("main-scroll custom-scrollbar", activeRoute.id === "chat" && "is-chat-page")}>
          <div className={cn("main-inner", activeRoute.id === "chat" && "chat-page-inner")}>
            {error && (
              <div className="banner banner-error">
                <span className="material-symbols-outlined">error</span>
                <span>{error}</span>
                <button type="button" className="banner-dismiss" aria-label="Đóng thông báo lỗi" onClick={() => setError("")}>
                  <span className="material-symbols-outlined">close</span>
                </button>
              </div>
            )}
            {notice && (
              <div className="banner banner-notice">
                <span className="material-symbols-outlined">check_circle</span>
                <span>{notice}</span>
                <button type="button" className="banner-dismiss" aria-label="Đóng thông báo" onClick={() => setNotice("")}>
                  <span className="material-symbols-outlined">close</span>
                </button>
              </div>
            )}

            <Routes>
              <Route path="/" element={<OverviewPage />} />
              <Route path="/combos" element={<CombosPage />} />
              <Route path="/mapping" element={<MappingPage />} />
              <Route path="/models" element={<ModelsPage />} />
              <Route path="/upstreams" element={<UpstreamsPage />} />
              <Route path="/providers" element={<ProvidersPage />} />
              <Route path="/providers/:providerId" element={<ProvidersPage />} />
              <Route path="/proxy-pools" element={<ProxyPoolsPage />} />
              <Route path="/quota" element={<QuotaPage />} />
              <Route path="/free-tiers" element={<FreeTiersPage />} />
              <Route path="/usage" element={<UsagePage />} />
              <Route path="/token-saver" element={<TokenSaverPage />} />
              <Route path="/apis" element={<ApisPage />} />
              <Route path="/chat" element={<ChatPage />} />
              <Route path="/cli-tools" element={<CLIToolsPage />} />
              <Route path="/cli-tools/:toolId" element={<CLIToolDetailPage />} />
              <Route path="/logs" element={<LogsPage />} />
              <Route path="/settings" element={<SettingsPage />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </div>
        </div>
      </main>
    </div>
  );

  function QuotaPage() {
    return <QuotaTrackerView secret={secret} credentials={quotaCredentials} onError={setError} onMutated={() => void load()} />;
  }

  function FreeTiersPage() {
    return <FreeTiersView secret={secret} onError={setError} />;
  }

  function UsagePage() {
    return (
      <UsageView
        secret={secret}
        providers={snapshot.providers}
        credentials={snapshot.credentials || {}}
        onError={setError}
      />
    );
  }

  function TokenSaverPage() {
    return <TokenSaverView secret={secret} onError={setError} onNotice={setNotice} />;
  }

  function ApisPage() {
    const modelOptions = useMemo(
      () => buildExampleModelOptions(snapshot.models, snapshot.combos),
      [snapshot.models, snapshot.combos],
    );

    return (
      <ApisView
        secret={secret}
        apiKeys={snapshot.api_keys || []}
        modelOptions={modelOptions}
        onError={setError}
        onNotice={setNotice}
        onMutated={() => void load()}
      />
    );
  }

  function ChatPage() {
    const { models: chatModels, loadingProviderModels, loadingProviderIds, providerError } = useChatModels(
      secret,
      snapshot,
    );
    const providerLabels = useMemo(
      () => Object.fromEntries((snapshot.providers || []).map((provider) => [provider.ID, provider.Name || provider.ID])),
      [snapshot.providers],
    );

    const handleApiKeyChange = (value: string) => {
      setApiKey(value);
      if (value) localStorage.setItem("tproxy-api-key", value);
      else localStorage.removeItem("tproxy-api-key");
    };

    return (
      <ChatView
        models={chatModels}
        loadingProviderModels={loadingProviderModels}
        loadingProviderIds={loadingProviderIds}
        providerLabels={providerLabels}
        providerError={providerError}
        apiKey={apiKey}
        onApiKeyChange={handleApiKeyChange}
        onError={setError}
      />
    );
  }

  function CLIToolsPage() {
    return <CLIToolsView secret={secret} />;
  }

  function CLIToolDetailPage() {
    return <CLIToolDetailView snapshot={snapshot} secret={secret} />;
  }

  function ProvidersPage() {
    const { providerId } = useParams();
    return (
      <ProvidersView
        providers={snapshot.providers || []}
        credentials={snapshot.credentials || {}}
        aliases={snapshot.aliases || []}
        secret={secret}
        searchQuery={providerSearch}
        selectedId={providerId ?? null}
        snapshotLoading={loading}
        onSelect={(id) => navigate(id ? `/providers/${encodeURIComponent(id)}` : "/providers")}
        onMutated={() => void load()}
        onNotice={setNotice}
        onError={setError}
      />
    );
  }

  function OverviewPage() {
    return (
      <section className="section">
        <div className="section-head">
          <div>
            <p className="eyebrow">AI gateway</p>
            <h2>Overview</h2>
            <p>Live snapshot of traffic, savings, credentials, and API keys.</p>
          </div>
        </div>
        <div className="metrics">
          <Metric icon="bolt" label="Requests" value={compactNumber(snapshot.usage?.requests)} hint="all recorded attempts" />
          <Metric icon="error" label="Error attempts" value={compactNumber(snapshot.usage?.errors)} hint="retry and fallback included" variant="warning" />
          <Metric icon="savings" label="Tokens saved" value={compactNumber(snapshot.usage?.tokens_saved || 0)} hint="RTK tool output compression" variant="success" />
          <Metric icon="payments" label="Estimated cost" value={usd(snapshot.usage?.estimated_cost_usd || 0)} hint="models.dev pricing" />
          <Metric icon="key" label="Active credentials" value={String(activeCredentials)} hint="available account records" />
        </div>
        <OverviewApiKeysCard
          secret={secret}
          apiKeys={snapshot.api_keys || []}
          onError={setError}
          onMutated={() => void load()}
        />
      </section>
    );
  }

  function CombosPage() {
    const comboModels = useMemo(
      () =>
        (snapshot.models || []).map((model) => ({
          id: model.ID,
          label: model.DisplayName ? `${model.DisplayName} (${model.ID})` : model.ID,
          enabled: model.Enabled,
        })),
      [snapshot.models],
    );

    const comboRoutes = useMemo(() => {
      const mapped: Record<string, { id: string; provider_id: string; upstream_model: string; enabled: boolean }[]> = {};
      for (const [modelId, routes] of Object.entries(snapshot.routes || {})) {
        mapped[modelId] = (routes || []).map((route) => ({
          id: route.ID,
          provider_id: route.ProviderID,
          upstream_model: route.UpstreamModel,
          enabled: route.Enabled,
        }));
      }
      return mapped;
    }, [snapshot.routes]);

    return (
      <CombosView
        secret={secret}
        combos={snapshot.combos || []}
        models={comboModels}
        routesByModel={comboRoutes}
        onMutated={() => void load()}
        onNotice={setNotice}
        onError={setError}
      />
    );
  }

  function MappingPage() {
    return (
      <MappingView
        secret={secret}
        apiKeys={snapshot.api_keys || []}
        models={snapshot.models || []}
        routesByModel={snapshot.routes || {}}
        onError={setError}
        onNotice={setNotice}
      />
    );
  }

  function ModelsPage() {
    const modelProviders = useMemo(
      () =>
        (snapshot.providers || []).map((provider) => ({
          id: provider.ID,
          label: provider.Name ? `${provider.Name} (${provider.ID})` : provider.ID,
          type: provider.Type,
        })),
      [snapshot.providers],
    );

    const credentialCounts = useMemo(() => {
      const counts: Record<string, number> = {};
      for (const [providerId, items] of Object.entries(snapshot.credentials || {})) {
        counts[providerId] = (items || []).filter((item) => item.enabled).length;
      }
      return counts;
    }, [snapshot.credentials]);

    const modelRoutes = useMemo(() => {
      const mapped: Record<string, { ID: string; ProviderID: string; UpstreamModel: string; Priority: number; Weight?: number; Enabled: boolean }[]> = {};
      for (const [modelId, routes] of Object.entries(snapshot.routes || {})) {
        mapped[modelId] = (routes || []).map((route) => ({
          ID: route.ID,
          ProviderID: route.ProviderID,
          UpstreamModel: route.UpstreamModel,
          Priority: route.Priority,
          Weight: route.Weight,
          Enabled: route.Enabled,
        }));
      }
      return mapped;
    }, [snapshot.routes]);

    return (
      <ModelsView
        secret={secret}
        models={snapshot.models || []}
        routesByModel={modelRoutes}
        providers={modelProviders}
        credentialCounts={credentialCounts}
        onMutated={() => void load()}
        onNotice={setNotice}
        onError={setError}
      />
    );
  }

  function UpstreamsPage() {
    return (
      <section className="section">
              <div className="section-head">
                <div>
                  <p className="eyebrow">Infrastructure</p>
                  <h2>Health overview</h2>
                  <p>Run health checks and model discovery without opening each provider detail page. For credential setup and OAuth, use <button type="button" className="inline-link" onClick={() => navigate("/providers")}>Providers</button>.</p>
                </div>
                <div style={{ display: "flex", alignItems: "center", gap: 12, flexWrap: "wrap" }}>
                  <span className="meta">{snapshot.providers?.length || 0} configured</span>
                  <Button
                    variant="outline"
                    size="sm"
                    icon="monitor_heart"
                    loading={healthCheckAllBusy}
                    disabled={healthCheckAllBusy || !(snapshot.providers?.length)}
                    onClick={() => void checkAllProviders()}
                  >
                    {healthCheckAllBusy ? "Checking…" : "Check all health"}
                  </Button>
                  <Button variant="primary" size="sm" icon="dns" onClick={() => navigate("/providers")}>
                    Manage providers
                  </Button>
                </div>
              </div>
              <Card pad="md">
                <div className="row-table">
                  {(snapshot.providers || []).map((provider) => (
                    <div className="row" key={provider.ID}>
                      <ProviderLogo
                        className="provider-badge"
                        providerId={provider.ID}
                        providerType={provider.Type}
                      />
                      <div className="row-body">
                        <div className="row-primary">
                          <strong>{provider.Name || provider.ID}</strong>
                          <small>{provider.BaseURL || "local transport"}</small>
                        </div>
                        <div className="row-meta">
                          <code>{provider.Type}</code>
                          <span>{snapshot.credentials?.[provider.ID]?.length || 0} accounts</span>
                          {statusBadge(provider.Status, provider.Enabled)}
                        </div>
                        {discovered[provider.ID]?.length ? (
                          <div className="discovery-list">
                            {discovered[provider.ID].slice(0, 8).map((model) => <code key={model.id}>{model.id}</code>)}
                          </div>
                        ) : null}
                      </div>
                      <div className="row-actions">
                        <Button variant="outline" size="sm" icon="monitor_heart" onClick={() => void checkProvider(provider.ID)}>Health</Button>
                        <Button variant="outline" size="sm" icon="search" onClick={() => void discoverProvider(provider.ID)}>Discover</Button>
                        <Button variant="ghost" size="sm" icon="arrow_forward" onClick={() => navigate(`/providers/${encodeURIComponent(provider.ID)}`)}>Open</Button>
                        <Button variant="danger" size="sm" icon="delete" onClick={() => remove(`/api/admin/providers/${encodeURIComponent(provider.ID)}`, `provider ${provider.ID}`)}>Delete</Button>
                      </div>
                    </div>
                  ))}
                  {(snapshot.providers || []).length === 0 && <EmptyState icon="dns" text="No providers configured yet." />}
                </div>
              </Card>
      </section>
    );
  }

  function ProxyPoolsPage() {
    return (
      <ProxyPoolsView
        secret={secret}
        onError={setError}
        onNotice={setNotice}
        onMutated={() => void load()}
      />
    );
  }

  function LogsPage() {
    return (
      <LogsView
        logs={logs}
        audit={audit}
        streaming={logsStreamEnabled}
        onRefreshAudit={refreshAudit}
      />
    );
  }

  function SettingsPage() {
    return (
      <SettingsView
        secret={secret}
        onError={setError}
        onNotice={setNotice}
        onMutated={() => void load()}
        onPasswordChanged={(newPassword) => {
          setStoredManagementSecret(newPassword);
          setSecret(newPassword);
        }}
      />
    );
  }
}

/* ============================================================
   Small presentational helpers
   ============================================================ */
function Metric({ icon, label, value, hint, variant }: { icon: string; label: string; value: string; hint: string; variant?: "success" | "warning" }) {
  const badgeVariant = variant ? `badge ${variant}` : "badge primary";
  return (
    <Card pad="md" className="metric-card" elev>
      <span className="metric-icon" style={variant ? { background: "var(--color-surface-2)", color: "var(--color-text-muted)" } : undefined}>
        <span className="material-symbols-outlined">{icon}</span>
      </span>
      <span className="metric-label">
        {label}
        {variant && <span className={badgeVariant} style={{ marginLeft: 8, verticalAlign: "middle" }}>{variant === "success" ? "saved" : "errors"}</span>}
      </span>
      <strong className="metric-value">{value}</strong>
      <small className="metric-hint">{hint}</small>
    </Card>
  );
}

function EmptyState({ icon, text }: { icon: string; text: string }) {
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 12, padding: "24px 8px", color: "var(--color-text-muted)" }}>
      <span className="material-symbols-outlined" style={{ fontSize: 28, color: "var(--color-text-subtle)" }}>{icon}</span>
      <span style={{ fontSize: 14 }}>{text}</span>
    </div>
  );
}

export default App;
