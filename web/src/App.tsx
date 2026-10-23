import { useCallback, useEffect, useMemo, useRef, useState, type ComponentProps } from "react";
import { useTranslation } from "react-i18next";
import { Navigate, Route, Routes, useLocation, useNavigate, useParams } from "react-router-dom";
import { Sidebar } from "./components/Sidebar";
import { LanguageToggle } from "./components/LanguageToggle";
import { useViewportWidth } from "./hooks/useViewportWidth";
import { Header } from "./components/Header";
import { cn } from "./components/ui";
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
import { QuotaTrackerView } from "./components/quota/QuotaTrackerView";
import { OperationsView } from "./components/operations/OperationsView";
import { DiagnosticsView } from "./components/diagnostics/DiagnosticsView";
import { UsageView } from "./components/usage/UsageView";
import { TokenSaverView } from "./components/token-saver/TokenSaverView";
import { FreeTiersView } from "./components/free-tiers/FreeTiersView";
import { ApisView } from "./components/apis/ApisView";
import { buildExampleModelOptions } from "./components/apis/utils";
import { OverviewView } from "./components/overview/OverviewView";
import { HealthOverviewView } from "./components/upstreams/HealthOverviewView";
import { ProxyPoolsView } from "./components/proxy-pools/ProxyPoolsView";
import { CombosPage } from "./components/combos/CombosPage";
import { MappingView } from "./components/mapping/MappingView";
import { ModelsPage } from "./components/models/ModelsPage";
import { CLIToolDetailView } from "./components/cli-tools/CLIToolDetailView";
import { CLIToolsView } from "./components/cli-tools/CLIToolsView";
import { SkillsView } from "./components/skills/SkillsView";
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
  weight?: number;
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
  api_keys: { id: string; name: string; models: string[]; enabled: boolean; policy?: { limits?: Record<string, number>; endpoints?: string[]; credential_ids?: string[]; team?: string } }[];
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

// Keep the route component stable when notices or snapshots update, so an
// in-progress import retains its selected files and per-file results.
function ProvidersPage(props: Omit<ComponentProps<typeof ProvidersView>, "selectedId" | "onSelect">) {
  const { providerId } = useParams();
  const navigate = useNavigate();
  return <ProvidersView {...props} selectedId={providerId ?? null}
    onSelect={(id) => navigate(id ? `/providers/${encodeURIComponent(id)}` : "/providers")} />;
}

function App() {
  const { t } = useTranslation();
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
  const [apiKey, setApiKey] = useState("");

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

  useEffect(() => {
    setApiKey(resolveChatApiKey(snapshot.api_keys || [], secret));
  }, [snapshot.api_keys, secret]);

  const loadTokenRef = useRef(0);

  const load = useCallback(async () => {
    if (authState !== "authenticated" || !secret) return;
    const token = ++loadTokenRef.current;
    setLoading(true);
    setError("");
    try {
      const health = await fetch("/healthz");
      if (loadTokenRef.current !== token) return;
      setGatewayOnline(health.ok);

      const response = await fetch("/api/admin/snapshot", { headers: authHeaders });
      const data = await response.json();
      if (loadTokenRef.current !== token) return;
      if (!response.ok) {
        const code = data?.error?.code;
        if (code === "invalid_management_secret") {
          logout();
          throw new Error(t("auth.sessionExpired"));
        }
        throw new Error(data?.error?.message || `HTTP ${response.status}`);
      }
      setSnapshot(data);
      setStoredManagementSecret(secret);
      void refreshAudit();
    } catch (cause) {
      if (loadTokenRef.current !== token) return;
      setError(cause instanceof Error ? cause.message : t("common.loadStateFailed"));
    } finally {
      if (loadTokenRef.current === token) setLoading(false);
    }
  }, [authHeaders, authState, logout, secret]);

  const refreshSnapshot = useCallback(() => {
    void load();
  }, [load]);

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
    try { const result = await adminRequest(`/api/admin/providers/${encodeURIComponent(id)}/health`, "POST"); setNotice(result.ok ? t("providers.healthOk", { id }) : t("providers.healthFailed", { id })); await load(); } catch (cause) { setError(cause instanceof Error ? cause.message : t("providers.healthCheckFailed")); }
  };
  const checkAllProviders = async (only?: string[]) => {
    const ids = only ?? (snapshot.providers || []).map((provider) => provider.ID);
    if (ids.length === 0) {
      setError(t("providers.noProvidersConfigured"));
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
      setNotice(t("providers.healthCheckFinished", { ok, failed }));
      await load();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t("providers.healthCheckFailed"));
    } finally {
      setHealthCheckAllBusy(false);
    }
  };
  const discoverProvider = async (id: string) => {
    try { const result = await adminRequest(`/api/admin/providers/${encodeURIComponent(id)}/models`, "GET"); setDiscovered((current) => ({ ...current, [id]: result.data || [] })); setNotice(t("providers.modelsDiscovered", { count: result.data?.length || 0, id })); await load(); } catch (cause) { setError(cause instanceof Error ? cause.message : t("providers.modelDiscoveryFailed")); }
  };
  // Callers confirm with a ConfirmDialog before calling this.
  const remove = async (path: string, label: string) => {
    try { await adminRequest(path, "DELETE"); setNotice(t("common.deleted", { label })); await load(); } catch (cause) { setError(cause instanceof Error ? cause.message : t("common.deleteFailed")); }
  };

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
        priority: credential.priority,
        weight: credential.weight,
        proxy_pool_ids: credential.proxy_pool_ids,
        created_at: credential.created_at,
        cooldown_until: credential.cooldown_until,
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

  const sidebarCollapseLabel = isNarrowSidebar ? t("nav.expandMenu") : t("nav.collapseMenu");

  if (authState === "checking") {
    return (
      <div className="app-shell app-shell-auth">
        <div className="auth-language-toggle">
          <LanguageToggle />
        </div>
        <AuthLoadingView />
      </div>
    );
  }

  if (authState === "unauthenticated") {
    return (
      <div className="app-shell app-shell-auth">
        <div className="auth-language-toggle">
          <LanguageToggle />
        </div>
        <LoginView onLogin={login} />
      </div>
    );
  }

  const providersPage = (
    <ProvidersPage
      providers={snapshot.providers || []}
      credentials={snapshot.credentials || {}}
      aliases={snapshot.aliases || []}
      secret={secret}
      snapshotLoading={loading}
      onMutated={() => void load()}
      onNotice={setNotice}
      onError={setError}
    />
  );

  return (
    <div className="app-shell">
      {isMobileSidebar && mobileNavOpen ? (
        <button
          type="button"
          className="sidebar-backdrop"
          aria-label={t("nav.collapseMenu")}
          onClick={() => setMobileNavOpen(false)}
        />
      ) : null}
      <div className={cn("sidebar-desktop", isNarrowSidebar && "is-narrow", isMobileSidebar && "is-mobile", mobileNavOpen && "is-open")}>
        <Sidebar
          online={online}
          collapsed={isMobileSidebar ? false : isNarrowSidebar}
          onToggleCollapse={isMobileSidebar ? undefined : toggleSidebar}
          collapseLabel={sidebarCollapseLabel}
          onClose={() => setMobileNavOpen(false)}
          onLogout={logout}
          secret={secret}
        />
      </div>

      <main className="main-area">
        <Header
          title={t(activeRoute.i18nKey + "_page_title")}
          description={t(activeRoute.i18nKey + "_page_desc")}
          icon={activeRoute.icon}
          onRefresh={load}
          loading={loading}
          onLogout={logout}
          onOpenNav={isMobileSidebar ? () => setMobileNavOpen(true) : undefined}
        />

        <div className={cn("main-scroll custom-scrollbar", activeRoute.id === "chat" && "is-chat-page")}>
          <div className={cn("main-inner", activeRoute.id === "chat" && "chat-page-inner")}>
            {error && activeRoute.id !== "chat" && (
              <div className="banner banner-error">
                <span className="material-symbols-outlined">error</span>
                <span>{error}</span>
                <button type="button" className="banner-dismiss" aria-label={t("common.dismissError")} onClick={() => setError("")}>
                  <span className="material-symbols-outlined">close</span>
                </button>
              </div>
            )}
            {notice && (
              <div className="banner banner-notice">
                <span className="material-symbols-outlined">check_circle</span>
                <span>{notice}</span>
                <button type="button" className="banner-dismiss" aria-label={t("common.dismiss")} onClick={() => setNotice("")}>
                  <span className="material-symbols-outlined">close</span>
                </button>
              </div>
            )}

            <Routes>
              <Route
                path="/"
                element={
                  <OverviewView
                    secret={secret}
                    providers={snapshot.providers || []}
                    credentials={snapshot.credentials || {}}
                    proxyPools={snapshot.proxy_pools || []}
                    models={snapshot.models || []}
                    routes={snapshot.routes || {}}
                    combos={snapshot.combos || []}
                    apiKeys={snapshot.api_keys || []}
                    usage={snapshot.usage}
                    loading={loading}
                    onError={setError}
                    onMutated={refreshSnapshot}
                  />
                }
              />
              <Route
                path="/combos"
                element={<CombosPage secret={secret} snapshot={snapshot} loading={loading} onMutated={refreshSnapshot} onNotice={setNotice} onError={setError} />}
              />
              <Route path="/mapping" element={MappingPage()} />
              <Route
                path="/models"
                element={<ModelsPage secret={secret} snapshot={snapshot} loading={loading} onMutated={refreshSnapshot} onNotice={setNotice} onError={setError} />}
              />
              <Route
                path="/upstreams"
                element={
                  <HealthOverviewView
                    providers={snapshot.providers || []}
                    credentials={snapshot.credentials || {}}
                    routes={snapshot.routes || {}}
                    discovered={discovered}
                    loading={loading}
                    checkingAll={healthCheckAllBusy}
                    onCheck={checkProvider}
                    onCheckMany={checkAllProviders}
                    onDiscover={discoverProvider}
                    onDelete={(provider) => remove(`/api/admin/providers/${encodeURIComponent(provider.ID)}`, provider.Name || provider.ID)}
                  />
                }
              />
              <Route path="/providers" element={providersPage} />
              <Route path="/providers/:providerId" element={providersPage} />
              <Route
                path="/proxy-pools"
                element={
                  <ProxyPoolsView
                    secret={secret}
                    providers={snapshot.providers || []}
                    credentials={snapshot.credentials || {}}
                    onError={setError}
                    onNotice={setNotice}
                    onMutated={refreshSnapshot}
                  />
                }
              />
              <Route
                path="/quota"
                element={
                  <QuotaTrackerView
                    secret={secret}
                    credentials={quotaCredentials}
                    accountsLoading={loading}
                    onError={setError}
                    onNotice={setNotice}
                    onMutated={refreshSnapshot}
                  />
                }
              />
              <Route path="/free-tiers" element={<FreeTiersPage />} />
              <Route path="/usage" element={<UsagePage />} />
              <Route path="/operations" element={<OperationsView secret={secret} providers={snapshot.providers} credentials={snapshot.credentials || {}} />} />
              <Route path="/diagnostics" element={<DiagnosticsView secret={secret}/>} />
              <Route path="/token-saver" element={<TokenSaverPage />} />
              <Route path="/apis" element={ApisPage()} />
              <Route path="/chat" element={<ChatPage />} />
              {/* Called, not rendered as <Page />: a wrapper declared inside App is a new
                  component type on every App render, which would remount the page and drop
                  its filters, paused log stream and unsaved settings. */}
              <Route path="/cli-tools" element={CLIToolsPage()} />
              <Route path="/cli-tools/:toolId" element={CLIToolDetailPage()} />
              <Route path="/skills" element={<SkillsView />} />
              <Route path="/logs" element={LogsPage()} />
              <Route path="/settings" element={SettingsPage()} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </div>
        </div>
      </main>
    </div>
  );

  function FreeTiersPage() {
    return <FreeTiersView secret={secret} onError={setError} />;
  }

  function UsagePage() {
    return (
      <UsageView
        secret={secret}
        providers={snapshot.providers}
        credentials={snapshot.credentials || {}}
        apiKeys={snapshot.api_keys || []}
        onError={setError}
      />
    );
  }

  function TokenSaverPage() {
    return <TokenSaverView secret={secret} onError={setError} onNotice={setNotice} />;
  }

  function ApisPage() {
    // No hooks here: this wrapper is called from the route element, not rendered.
    const modelOptions = buildExampleModelOptions(snapshot.models, snapshot.combos);

    return (
      <ApisView
        secret={secret}
        apiKeys={snapshot.api_keys || []}
        modelOptions={modelOptions}
        providers={snapshot.providers || []}
        credentials={snapshot.credentials || {}}
        routes={snapshot.routes || {}}
        combos={snapshot.combos || []}
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

    const handleInvalidClientApiKey = () => {
      // A user may have rotated or deleted the key since this browser last
      // used it. Drop only the generic chat cache, then prefer a secret saved
      // for an enabled API-key record. Never fall back to a management secret.
      localStorage.removeItem("tproxy-api-key");
      setApiKey(resolveChatApiKey(snapshot.api_keys || [], secret));
    };

    return (
      <ChatView
        models={chatModels}
        loadingProviderModels={loadingProviderModels}
        loadingProviderIds={loadingProviderIds}
        providerLabels={providerLabels}
        providerError={providerError}
        apiKey={apiKey}
        apiKeys={snapshot.api_keys || []}
        onApiKeyChange={handleApiKeyChange}
        onInvalidClientApiKey={handleInvalidClientApiKey}
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

  function MappingPage() {
    return (
      <MappingView
        secret={secret}
        apiKeys={snapshot.api_keys || []}
        models={snapshot.models || []}
        combos={snapshot.combos || []}
        routesByModel={snapshot.routes || {}}
        providers={snapshot.providers || []}
        credentials={snapshot.credentials || {}}
        onError={setError}
        onNotice={setNotice}
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
        credentials={snapshot.credentials || {}}
        apiKeys={snapshot.api_keys || []}
      />
    );
  }

  function SettingsPage() {
    return (
      <SettingsView
        secret={secret}
        providers={snapshot.providers || []}
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

export default App;
