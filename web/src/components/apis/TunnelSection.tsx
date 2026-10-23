import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";
import { Button, ConfirmDialog, Field, Input, Modal, Toggle, cn } from "../ui";
import { StatusPill, ToolButton, type Tone } from "../page-kit";
import { SecurityWarning } from "./SecurityWarning";
import {
  checkTailscale,
  disableTailscale,
  disableTunnel,
  enableTailscale,
  enableTunnel,
  fetchTunnelStatus,
  pingAnyHealth,
  pingHealth,
  saveTunnelDashboardAccess,
  type CloudflareTunnelStatus,
  type TailscaleTunnelStatus,
} from "./tunnelApi";
import { normalizeBaseUrl } from "./utils";

type Props = {
  secret: string;
  apiKeyCount: number;
  onError: (message: string) => void;
  onNotice: (message: string) => void;
  /** Reports the live state of both methods so the page can show it outside this panel. */
  onSummary?: (summary: RemoteAccessSummary) => void;
};

const STATUS_POLL_MS = 5000;
const PING_INTERVAL_MS = 3000;
const MISS_THRESHOLD = 3;

export type TunnelState = "checking" | "off" | "busy" | "online" | "unreachable" | "reconnecting" | "error";

export type RemoteAccessSummary = {
  loading: boolean;
  cloudflare: { state: TunnelState; url?: string };
  tailscale: { state: TunnelState; url?: string };
};

export const STATE_TONE: Record<TunnelState, Tone> = {
  checking: "neutral",
  off: "muted",
  busy: "info",
  online: "success",
  unreachable: "warning",
  reconnecting: "warning",
  error: "danger",
};

type TunnelCardProps = {
  icon: string;
  title: string;
  description: string;
  state: TunnelState;
  url?: string;
  message?: string;
  facts?: ReactNode;
  copyId: string;
  copied: string | null;
  onCopy: (text: string, id: string) => void;
  actions?: ReactNode;
};

/** One remote-access method: what it is, its live state and the start/stop controls. */
function TunnelCard({ icon, title, description, state, url, message, facts, copyId, copied, onCopy, actions }: TunnelCardProps) {
  const { t } = useTranslation();
  const tone = STATE_TONE[state];
  return (
    <article className={cn("cp-card ap-tunnel", `cp-tone-${tone}`)} aria-label={title}>
      <header className="cp-card-head">
        <span className="material-symbols-outlined cp-section-icon" aria-hidden>{icon}</span>
        <div className="cp-card-title">
          <strong>{title}</strong>
          <div className="cp-card-tags">
            <StatusPill tone={tone} loading={state === "checking" || state === "busy" || state === "reconnecting"}>
              {t(`apis.tunnel.state.${state}`)}
            </StatusPill>
          </div>
        </div>
      </header>
      <div className="cp-card-body">
        <p className="ap-tunnel-desc">{description}</p>
        {url ? (
          <button type="button" className="ap-url ap-tunnel-url" onClick={() => onCopy(url, copyId)} title={t("apis.copyEndpoint", { label: title })}>
            <span className="ap-url-text">{url}</span>
            <span className="material-symbols-outlined" aria-hidden>{copied === copyId ? "check" : "content_copy"}</span>
          </button>
        ) : null}
        {message ? (
          <p className={cn("ap-tunnel-msg", `cp-tone-${tone}`)} role={state === "error" ? "alert" : "status"}>
            <span className={cn("material-symbols-outlined", (state === "busy" || state === "reconnecting") && "animate-spin")} aria-hidden>
              {state === "error" ? "error" : state === "busy" || state === "reconnecting" ? "progress_activity" : state === "unreachable" ? "warning" : "info"}
            </span>
            <span>{message}</span>
          </p>
        ) : null}
        {facts}
      </div>
      {actions ? (
        <footer className="cp-card-foot">
          <span />
          <div className="ap-tunnel-actions">{actions}</div>
        </footer>
      ) : null}
    </article>
  );
}

export function TunnelSection({ secret, apiKeyCount, onError, onNotice, onSummary }: Props) {
  const { t } = useTranslation();
  const { copied, copy } = useCopyToClipboard();
  const [tunnel, setTunnel] = useState<CloudflareTunnelStatus | null>(null);
  const [tailscale, setTailscale] = useState<TailscaleTunnelStatus | null>(null);
  // Fail closed while the server setting is unavailable. The API also defaults
  // to false, so a transient dashboard request must never advertise access as
  // enabled or encourage an unsafe toggle.
  const [tunnelDashboardAccess, setTunnelDashboardAccess] = useState(false);
  const [loading, setLoading] = useState(true);
  const [tunnelLoading, setTunnelLoading] = useState(false);
  const [tsLoading, setTsLoading] = useState(false);
  const [tunnelProgress, setTunnelProgress] = useState("");
  const [tsProgress, setTsProgress] = useState("");
  const [clientTunnelHealth, setClientTunnelHealth] = useState({ url: "", reachable: false });
  const [clientTsReachable, setClientTsReachable] = useState(false);
  const [tunnelStatusMessage, setTunnelStatusMessage] = useState<string | null>(null);
  const [tsStatusMessage, setTsStatusMessage] = useState<string | null>(null);
  const [tsAuthUrl, setTsAuthUrl] = useState("");
  const [tsInstalled, setTsInstalled] = useState<boolean | null>(null);
  const [showEnableModal, setShowEnableModal] = useState(false);
  const [tunnelToken, setTunnelToken] = useState("");
  const [tunnelHostname, setTunnelHostname] = useState("");
  const [showDisableModal, setShowDisableModal] = useState(false);
  const [showDisableTsModal, setShowDisableTsModal] = useState(false);
  const [showTsModal, setShowTsModal] = useState(false);
  const tunnelMissRef = useRef(0);
  const tsMissRef = useRef(0);
  const tunnelEverReachableRef = useRef("");

  const syncStatus = useCallback(async () => {
    try {
      const data = await fetchTunnelStatus(secret);
      setTunnel(data.tunnel);
      setTailscale(data.tailscale);
      if (data.tunnel.reachable) {
        tunnelEverReachableRef.current = data.tunnel.publicUrl || data.tunnel.tunnelUrl || "";
        tunnelMissRef.current = 0;
      }
      if (data.tailscale.reachable) {
        setClientTsReachable(true);
        tsMissRef.current = 0;
      }
    } catch (error) {
      onError(error instanceof Error ? error.message : t("apis.tunnel.failedLoadStatus"));
    } finally {
      setLoading(false);
    }
  }, [secret, onError]);

  useEffect(() => {
    void syncStatus();
  }, [syncStatus]);

  useEffect(() => {
    void (async () => {
      try {
        const response = await fetch("/api/admin/tunnel/dashboard-access", {
          headers: secret ? { Authorization: `Bearer ${secret}` } : {},
        });
        if (response.ok) {
          const data = await response.json();
          setTunnelDashboardAccess(data.tunnel_dashboard_access !== false);
        } else {
          setTunnelDashboardAccess(false);
        }
      } catch {
        setTunnelDashboardAccess(false);
      }
    })();
  }, [secret]);

  const tunnelEnabled = tunnel?.settingsEnabled ?? false;
  const tsEnabled = tailscale?.settingsEnabled ?? false;
  const tunnelPublicUrl = tunnel?.publicUrl || tunnel?.tunnelUrl || "";
  const tsUrl = tailscale?.tunnelUrl || "";

  const tunnelHealthy = Boolean(
    tunnel?.reachable ||
    (clientTunnelHealth.reachable && clientTunnelHealth.url === tunnelPublicUrl),
  );
  const tsHealthy = Boolean(tailscale?.running || tailscale?.reachable || clientTsReachable);

  useEffect(() => {
    if (!tunnelEnabled && !tsEnabled) return;
    const timer = setInterval(() => void syncStatus(), STATUS_POLL_MS);
    return () => clearInterval(timer);
  }, [tunnelEnabled, tsEnabled, syncStatus]);

  useEffect(() => {
    if (!tunnelEnabled && !tsEnabled) return;
    let active = true;
    const probe = async () => {
      if (tunnelEnabled && tunnelPublicUrl) {
        const ok = await pingAnyHealth(tunnel?.publicUrl, tunnel?.tunnelUrl);
        if (!active) return;
        if (ok) {
          tunnelMissRef.current = 0;
          tunnelEverReachableRef.current = tunnelPublicUrl;
          setClientTunnelHealth({ url: tunnelPublicUrl, reachable: true });
        } else {
          tunnelMissRef.current += 1;
          if (tunnelMissRef.current >= MISS_THRESHOLD) {
            setClientTunnelHealth({ url: tunnelPublicUrl, reachable: false });
          }
        }
      }
      if (tsEnabled && tsUrl) {
        const ok = await pingHealth(tsUrl);
        if (!active) return;
        if (ok) {
          tsMissRef.current = 0;
          setClientTsReachable(true);
        } else {
          tsMissRef.current += 1;
          if (tsMissRef.current >= MISS_THRESHOLD) setClientTsReachable(false);
        }
      }
    };
    void probe();
    const timer = setInterval(() => void probe(), PING_INTERVAL_MS);
    return () => {
      active = false;
      clearInterval(timer);
    };
  }, [tunnelEnabled, tsEnabled, tunnelPublicUrl, tsUrl, tunnel?.publicUrl, tunnel?.tunnelUrl]);

  const openCloudflare = () => {
    setTunnelHostname(tunnel?.hostname || "");
    setTunnelToken("");
    setTunnelStatusMessage(null);
    setShowEnableModal(true);
  };

  const closeCloudflare = () => {
    if (tunnelLoading) return;
    setTunnelToken("");
    setShowEnableModal(false);
  };

  const handleEnableTunnel = async () => {
    if (apiKeyCount === 0) {
      setShowEnableModal(false);
      setTunnelStatusMessage(t("apis.tunnel.createKeyFirst"));
      return;
    }
    if (!tunnelHostname.trim() || (!tunnelToken.trim() && !tunnel?.tokenConfigured)) return;
    setTunnelLoading(true);
    setTunnelStatusMessage(null);
    setTunnelProgress(t("apis.tunnel.creating"));
    let polling = true;
    void (async () => {
      while (polling) {
        try {
          const status = await fetchTunnelStatus(secret);
          if (status.download.downloading) {
            setTunnelProgress(t("apis.tunnel.downloading"));
          }
        } catch {
          // ignore
        }
        await new Promise((resolve) => setTimeout(resolve, 1000));
      }
    })();
    try {
      const result = await enableTunnel(secret, tunnelHostname.trim(), tunnelToken.trim());
      if (!result.success) {
        setTunnelStatusMessage(result.error || t("apis.tunnel.failedEnable"));
        return;
      }
      setTunnelToken("");
      setShowEnableModal(false);
      setClientTunnelHealth({ url: "", reachable: false });
      tunnelEverReachableRef.current = "";
      onNotice(t("apis.tunnel.enabled"));
    } catch (error) {
      setTunnelStatusMessage(error instanceof Error ? error.message : t("apis.tunnel.failedEnable"));
    } finally {
      polling = false;
      setTunnelLoading(false);
      setTunnelProgress("");
      await syncStatus();
    }
  };

  const handleDisableTunnel = async () => {
    setTunnelLoading(true);
    try {
      await disableTunnel(secret);
      setShowDisableModal(false);
      setClientTunnelHealth({ url: "", reachable: false });
      setTunnelStatusMessage(null);
      tunnelEverReachableRef.current = "";
      onNotice(t("apis.tunnel.disabled"));
      await syncStatus();
    } catch (error) {
      onError(error instanceof Error ? error.message : t("apis.tunnel.failedDisable"));
    } finally {
      setTunnelLoading(false);
    }
  };

  const handleConnectTailscale = async () => {
    setShowTsModal(false);
    setTsLoading(true);
    setTsProgress(t("apis.tunnel.connecting"));
    setTsAuthUrl("");
    try {
      const result = await enableTailscale(secret);
      if (result.needsLogin && result.authUrl) {
        setTsAuthUrl(result.authUrl);
        setTsProgress(t("apis.tunnel.loginRequired"));
        for (let i = 0; i < 40; i++) {
          await new Promise((resolve) => setTimeout(resolve, 3000));
          const check = await checkTailscale(secret);
          if (check.logged_in) {
            setTsAuthUrl("");
            const retry = await enableTailscale(secret);
            if (retry.success && retry.tunnelUrl) {
              await pingHealth(retry.tunnelUrl);
              onNotice(t("apis.tunnel.tailscaleEnabled"));
              await syncStatus();
              return;
            }
          }
        }
        setTsStatusMessage(t("apis.tunnel.loginTimedOut"));
        return;
      }
      if (result.funnelNotEnabled && result.enableUrl) {
        setTsAuthUrl(result.enableUrl);
        setTsProgress(t("apis.tunnel.enableFunnelAdmin"));
        setTsStatusMessage(t("apis.tunnel.funnelNotEnabled"));
        return;
      }
      if (!result.success) {
        setTsStatusMessage(result.error || t("apis.tunnel.failedConnectTailscale"));
        return;
      }
      if (result.tunnelUrl) {
        await pingHealth(result.tunnelUrl);
      }
      onNotice(t("apis.tunnel.tailscaleEnabled"));
      await syncStatus();
    } catch (error) {
      setTsStatusMessage(error instanceof Error ? error.message : t("apis.tunnel.failedConnectTailscale"));
    } finally {
      setTsLoading(false);
      setTsProgress("");
    }
  };

  const openTailscale = async () => {
    setTsStatusMessage(null);
    try {
      const check = await checkTailscale(secret);
      setTsInstalled(check.installed);
      if (!check.installed) {
        setShowTsModal(true);
        return;
      }
      await handleConnectTailscale();
    } catch (error) {
      onError(error instanceof Error ? error.message : t("apis.tunnel.tailscaleCheckFailed"));
    }
  };

  const handleDisableTailscale = async () => {
    setTsLoading(true);
    try {
      await disableTailscale(secret);
      setShowDisableTsModal(false);
      setClientTsReachable(false);
      onNotice(t("apis.tunnel.tailscaleDisabled"));
      await syncStatus();
    } catch (error) {
      onError(error instanceof Error ? error.message : t("apis.tunnel.failedDisableTailscale"));
    } finally {
      setTsLoading(false);
    }
  };

  const handleDashboardAccess = async (value: boolean) => {
    try {
      const result = await saveTunnelDashboardAccess(secret, value);
      setTunnelDashboardAccess(result.tunnel_dashboard_access);
    } catch (error) {
      onError(error instanceof Error ? error.message : t("apis.tunnel.failedDashboardAccess"));
    }
  };

  const startCloudflare = () => {
    if (apiKeyCount === 0) {
      setTunnelStatusMessage(t("apis.tunnel.createKeyFirst"));
      return;
    }
    openCloudflare();
  };

  const stopCloudflareButton = (
    <ToolButton icon="power_settings_new" tone="danger" onClick={() => setShowDisableModal(true)} title={t("apis.tunnel.disableTunnel")}>
      {t("apis.tunnel.stop")}
    </ToolButton>
  );
  const configureCloudflareButton = (label = t("apis.tunnel.configure"), icon = "tune") => (
    <ToolButton icon={icon} onClick={openCloudflare}>{label}</ToolButton>
  );

  // Same state machine as before, expressed as one view per state.
  const cloudflareView = ((): { state: TunnelState; url?: string; message?: string; actions?: ReactNode } => {
    if (loading) return { state: "checking", message: t("apis.tunnel.checking") };
    if (tunnelLoading) return { state: "busy", message: tunnelProgress || t("apis.tunnel.creating") };
    if (tunnelStatusMessage) {
      return {
        state: "error",
        message: tunnelStatusMessage,
        actions: tunnelEnabled ? (
          <>
            {configureCloudflareButton()}
            {stopCloudflareButton}
          </>
        ) : (
          <ToolButton icon="play_arrow" onClick={openCloudflare}>{t("apis.tunnel.start")}</ToolButton>
        ),
      };
    }
    if (!tunnelEnabled) {
      return { state: "off", actions: <ToolButton icon="play_arrow" onClick={startCloudflare}>{t("apis.tunnel.start")}</ToolButton> };
    }
    if (tunnelHealthy) {
      return {
        state: "online",
        url: normalizeBaseUrl(tunnelPublicUrl),
        actions: (
          <>
            {configureCloudflareButton()}
            {stopCloudflareButton}
          </>
        ),
      };
    }
    if (tunnel?.connected) {
      return {
        state: "unreachable",
        message: t("apis.tunnel.hostnameUnreachable"),
        actions: (
          <>
            {configureCloudflareButton()}
            {stopCloudflareButton}
          </>
        ),
      };
    }
    return {
      state: "reconnecting",
      message: tunnelEverReachableRef.current === tunnelPublicUrl ? t("apis.tunnel.tunnelReconnecting") : t("apis.tunnel.tunnelChecking"),
      actions: (
        <>
          {configureCloudflareButton(t("apis.tunnel.reconnect"), "sync")}
          {stopCloudflareButton}
        </>
      ),
    };
  })();

  const stopTailscaleButton = (
    <ToolButton icon="power_settings_new" tone="danger" onClick={() => setShowDisableTsModal(true)} title={t("apis.tunnel.disableTailscale")}>
      {t("apis.tunnel.stop")}
    </ToolButton>
  );

  const tailscaleView = ((): { state: TunnelState; url?: string; message?: string; actions?: ReactNode } => {
    if (loading) return { state: "checking", message: t("apis.tunnel.checking") };
    if (tsLoading) {
      return {
        state: "busy",
        message: tsProgress || t("apis.tunnel.connecting"),
        actions: tsAuthUrl ? (
          <ToolButton icon="open_in_new" onClick={() => window.open(tsAuthUrl, "tailscale_auth", "width=600,height=700,noopener,noreferrer")}>
            {t("apis.tunnel.openLogin")}
          </ToolButton>
        ) : undefined,
      };
    }
    if (tsStatusMessage) {
      return { state: "error", message: tsStatusMessage, actions: <ToolButton icon="play_arrow" onClick={() => void openTailscale()}>{t("apis.tunnel.start")}</ToolButton> };
    }
    if (!tsEnabled) {
      return { state: "off", actions: <ToolButton icon="play_arrow" onClick={() => void openTailscale()}>{t("apis.tunnel.start")}</ToolButton> };
    }
    if (tsHealthy) return { state: "online", url: normalizeBaseUrl(tsUrl), actions: stopTailscaleButton };
    return { state: "reconnecting", message: t("apis.tunnel.tailscaleReconnecting"), actions: stopTailscaleButton };
  })();

  const cloudflareUrl = cloudflareView.url;
  const tailscaleUrl = tailscaleView.url;
  useEffect(() => {
    onSummary?.({
      loading,
      cloudflare: { state: cloudflareView.state, url: cloudflareUrl },
      tailscale: { state: tailscaleView.state, url: tailscaleUrl },
    });
  }, [onSummary, loading, cloudflareView.state, cloudflareUrl, tailscaleView.state, tailscaleUrl]);

  const anyEnabled = tunnelEnabled || tsEnabled;

  return (
    <div className="ap-remote">
      {anyEnabled && apiKeyCount === 0 ? <SecurityWarning tone="danger" message={t("apis.tunnel.activeNoKeys")} /> : null}

      <div className="cp-grid" style={{ ["--cp-card-min" as string]: "340px" }}>
        <TunnelCard
          icon="cloud"
          title={t("apis.tunnel.cloudflareTitle")}
          description={t("apis.tunnel.cloudflareCardDesc")}
          copyId="tunnel_url"
          copied={copied}
          onCopy={copy}
          {...cloudflareView}
          facts={
            tunnel?.hostname || tunnel?.serviceUrl ? (
              <dl className="cp-facts ap-tunnel-facts">
                {tunnel?.hostname ? <div><dt>{t("apis.tunnel.hostname")}</dt><dd title={tunnel.hostname}>{tunnel.hostname}</dd></div> : null}
                {tunnel?.serviceUrl ? <div><dt>{t("apis.tunnel.service")}</dt><dd title={tunnel.serviceUrl}>{tunnel.serviceUrl}</dd></div> : null}
              </dl>
            ) : null
          }
        />
        <TunnelCard
          icon="hub"
          title={t("apis.tunnel.tailscaleTitle")}
          description={t("apis.tunnel.tailscaleCardDesc")}
          copyId="ts_url"
          copied={copied}
          onCopy={copy}
          {...tailscaleView}
        />
      </div>

      {anyEnabled ? (
        <section className={cn("cp-panel ap-dashboard-access", tunnelDashboardAccess && "is-on")} aria-label={t("apis.tunnel.dashboard")}>
          <span className="material-symbols-outlined cp-section-icon" aria-hidden>admin_panel_settings</span>
          <div className="ap-dashboard-access-copy">
            <strong>
              {t("apis.tunnel.allowDashboard")}
              {tunnelDashboardAccess ? <StatusPill tone="warning">{t("apis.tunnel.dashboardExposed")}</StatusPill> : null}
            </strong>
            <p>{t(tunnelDashboardAccess ? "apis.tunnel.dashboardOnHint" : "apis.tunnel.dashboardOffHint")}</p>
          </div>
          <Toggle
            checked={tunnelDashboardAccess}
            onChange={() => void handleDashboardAccess(!tunnelDashboardAccess)}
            aria-label={t("apis.tunnel.allowDashboard")}
          />
        </section>
      ) : null}

      <Modal
        open={showEnableModal}
        title={t("apis.tunnel.enableCloudflareTitle")}
        subtitle={t("apis.tunnel.cloudflareDesc")}
        icon="cloud"
        onClose={closeCloudflare}
        footer={
          <>
            <Button type="button" variant="secondary" disabled={tunnelLoading} onClick={closeCloudflare}>{t("common.cancel")}</Button>
            <Button
              type="submit"
              form="ap-cloudflare-form"
              icon="play_arrow"
              loading={tunnelLoading}
              disabled={!tunnelHostname.trim() || (!tunnelToken.trim() && !tunnel?.tokenConfigured)}
            >
              {t("apis.tunnel.startTunnel")}
            </Button>
          </>
        }
      >
        <form id="ap-cloudflare-form" className="ap-modal-form" onSubmit={(event) => { event.preventDefault(); void handleEnableTunnel(); }}>
          <Field label={t("apis.tunnel.hostname")} hint={t("apis.tunnel.hostnameHint")} required>
            <Input
              aria-label={t("apis.tunnel.hostname")}
              value={tunnelHostname}
              onChange={(event) => setTunnelHostname(event.target.value)}
              placeholder="api.example.com"
              autoCapitalize="none"
              spellCheck={false}
              disabled={tunnelLoading}
              required
            />
          </Field>
          <Field label={t("apis.tunnel.token")} hint={t(tunnel?.tokenConfigured ? "apis.tunnel.savedTokenHint" : "apis.tunnel.tokenHint")} required={!tunnel?.tokenConfigured}>
            <Input
              aria-label={t("apis.tunnel.token")}
              type="password"
              value={tunnelToken}
              onChange={(event) => setTunnelToken(event.target.value)}
              placeholder={t(tunnel?.tokenConfigured ? "apis.tunnel.savedTokenPlaceholder" : "apis.tunnel.tokenPlaceholder")}
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
              disabled={tunnelLoading}
              required={!tunnel?.tokenConfigured}
            />
          </Field>
          <div className="ap-modal-steps">
            <p>{t("apis.tunnel.routeHint")}</p>
            <code>{tunnel?.serviceUrl || "http://127.0.0.1:28120"}</code>
            <a href="https://one.dash.cloudflare.com/" target="_blank" rel="noopener noreferrer" className="cp-link-btn">
              {t("apis.tunnel.openCloudflare")}
            </a>
          </div>
          {tunnelStatusMessage ? <p className="ap-tunnel-msg cp-tone-danger" role="alert"><span className="material-symbols-outlined" aria-hidden>error</span><span>{tunnelStatusMessage}</span></p> : null}
          {tunnelLoading ? <p className="ap-tunnel-msg cp-tone-info" role="status"><span className="material-symbols-outlined animate-spin" aria-hidden>progress_activity</span><span>{tunnelProgress}</span></p> : null}
        </form>
      </Modal>

      <ConfirmDialog
        open={showDisableModal}
        title={t("apis.tunnel.disableTunnel")}
        message={t("apis.tunnel.disableTunnelConfirm")}
        confirmText={t("common.disable")}
        cancelText={t("common.cancel")}
        variant="danger"
        onConfirm={() => void handleDisableTunnel()}
        onClose={() => setShowDisableModal(false)}
      />

      <Modal
        open={showTsModal}
        title={t("apis.tunnel.tailscaleFunnelTitle")}
        icon="hub"
        onClose={() => setShowTsModal(false)}
        footer={
          <>
            <Button variant="secondary" onClick={() => setShowTsModal(false)}>{t("common.cancel")}</Button>
            <Button onClick={() => void (tsInstalled === false ? openTailscale() : handleConnectTailscale())}>
              {tsInstalled === false ? t("apis.tunnel.retryCheck") : t("apis.tunnel.connect")}
            </Button>
          </>
        }
      >
        <p className="modal-copy">
          {tsInstalled === false
            ? t("apis.tunnel.installTailscale")
            : t("apis.tunnel.connectTailscale")}
        </p>
      </Modal>

      <ConfirmDialog
        open={showDisableTsModal}
        title={t("apis.tunnel.disableTailscale")}
        message={t("apis.tunnel.disableTailscaleConfirm")}
        confirmText={t("common.disable")}
        cancelText={t("common.cancel")}
        variant="danger"
        onConfirm={() => void handleDisableTailscale()}
        onClose={() => setShowDisableTsModal(false)}
      />
    </div>
  );
}
