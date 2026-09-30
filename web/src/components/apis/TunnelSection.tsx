import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";
import { Button, ConfirmDialog, Field, Input, Modal, Toggle } from "../ui";
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
};

const STATUS_POLL_MS = 5000;
const PING_INTERVAL_MS = 3000;
const MISS_THRESHOLD = 3;

type EndpointTunnelRowProps = {
  label: string;
  active?: boolean;
  url?: string;
  copyId: string;
  copied: string | null;
  onCopy: (text: string, id: string) => void;
  statusText?: string;
  statusTone?: "warn" | "error" | "muted";
  trailing?: React.ReactNode;
};

function EndpointTunnelRow({
  label,
  active,
  url,
  copyId,
  copied,
  onCopy,
  statusText,
  statusTone = "muted",
  trailing,
}: EndpointTunnelRowProps) {
  return (
    <div className="endpoint-row">
      <span className={active ? "endpoint-row-badge active" : "endpoint-row-badge"}>{label}</span>
      {url ? (
        <>
          <Input value={url} readOnly className="endpoint-row-input" />
          <button
            type="button"
            className="endpoint-row-copy"
            onClick={() => onCopy(url, copyId)}
            aria-label={`Copy ${label}`}
          >
            <span className="material-symbols-outlined">{copied === copyId ? "check" : "content_copy"}</span>
          </button>
        </>
      ) : statusText ? (
        <div className={`endpoint-row-status-box ${statusTone}`}>
          {statusTone === "warn" ? (
            <span className="material-symbols-outlined endpoint-row-status-icon">progress_activity</span>
          ) : null}
          <span>{statusText}</span>
        </div>
      ) : (
        <div className="endpoint-row-placeholder" />
      )}
      {trailing ? <div className="endpoint-row-trailing">{trailing}</div> : null}
    </div>
  );
}

function PowerButton({ title, onClick }: { title: string; onClick: () => void }) {
  return (
    <button type="button" className="endpoint-row-power" onClick={onClick} title={title}>
      <span className="material-symbols-outlined">power_settings_new</span>
    </button>
  );
}

export function TunnelSection({ secret, apiKeyCount, onError, onNotice }: Props) {
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

  const renderTunnelRow = () => {
    if (loading) {
      return (
        <EndpointTunnelRow
          label={t("apis.tunnel.tunnel")}
          copyId="tunnel_url"
          copied={copied}
          onCopy={copy}
          statusText={t("apis.tunnel.checking")}
        />
      );
    }
    if (tunnelLoading) {
      return (
        <EndpointTunnelRow
          label={t("apis.tunnel.tunnel")}
          active
          copyId="tunnel_url"
          copied={copied}
          onCopy={copy}
          statusText={tunnelProgress || t("apis.tunnel.creating")}
          statusTone="muted"
        />
      );
    }
    if (tunnelStatusMessage) {
      return (
        <EndpointTunnelRow
          label={t("apis.tunnel.tunnel")}
          copyId="tunnel_url"
          copied={copied}
          onCopy={copy}
          statusText={tunnelStatusMessage}
          statusTone="error"
          trailing={
            <>
              {tunnelEnabled ? (
                <Button size="sm" variant="outline" onClick={openCloudflare}>{t("apis.tunnel.configure")}</Button>
              ) : (
                <Button size="sm" onClick={openCloudflare}>{t("apis.tunnel.enable")}</Button>
              )}
              {tunnelEnabled ? (
                <PowerButton title={t("apis.tunnel.disableTunnel")} onClick={() => setShowDisableModal(true)} />
              ) : null}
            </>
          }
        />
      );
    }
    if (!tunnelEnabled) {
      return (
        <EndpointTunnelRow
          label={t("apis.tunnel.tunnel")}
          copyId="tunnel_url"
          copied={copied}
          onCopy={copy}
          trailing={
            <Button
              size="sm"
              onClick={() => {
                if (apiKeyCount === 0) {
                  setTunnelStatusMessage(t("apis.tunnel.createKeyFirst"));
                  return;
                }
                openCloudflare();
              }}
            >
              {t("apis.tunnel.enable")}
            </Button>
          }
        />
      );
    }
    if (tunnelHealthy) {
      return (
        <EndpointTunnelRow
          label={t("apis.tunnel.tunnel")}
          active
          url={normalizeBaseUrl(tunnelPublicUrl)}
          copyId="tunnel_url"
          copied={copied}
          onCopy={copy}
          trailing={
            <>
              <Button size="sm" variant="outline" onClick={openCloudflare}>{t("apis.tunnel.configure")}</Button>
              <PowerButton title={t("apis.tunnel.disableTunnel")} onClick={() => setShowDisableModal(true)} />
            </>
          }
        />
      );
    }
    if (tunnel?.connected) {
      return (
        <EndpointTunnelRow
          label={t("apis.tunnel.tunnel")}
          active
          copyId="tunnel_url"
          copied={copied}
          onCopy={copy}
          statusText={t("apis.tunnel.hostnameUnreachable")}
          statusTone="warn"
          trailing={
            <>
              <Button size="sm" variant="outline" onClick={openCloudflare}>{t("apis.tunnel.configure")}</Button>
              <PowerButton title={t("apis.tunnel.disableTunnel")} onClick={() => setShowDisableModal(true)} />
            </>
          }
        />
      );
    }
    return (
      <EndpointTunnelRow
        label={t("apis.tunnel.tunnel")}
        active
        copyId="tunnel_url"
        copied={copied}
        onCopy={copy}
        statusText={tunnelEverReachableRef.current === tunnelPublicUrl ? t("apis.tunnel.tunnelReconnecting") : t("apis.tunnel.tunnelChecking")}
        statusTone="warn"
        trailing={
          <>
            <Button size="sm" variant="outline" onClick={openCloudflare}>{t("apis.tunnel.reconnect")}</Button>
            <PowerButton title={t("apis.tunnel.disableTunnel")} onClick={() => setShowDisableModal(true)} />
          </>
        }
      />
    );
  };

  const renderTailscaleRow = () => {
    if (loading) {
      return (
        <EndpointTunnelRow
          label={t("apis.tunnel.tailscale")}
          copyId="ts_url"
          copied={copied}
          onCopy={copy}
          statusText={t("apis.tunnel.checking")}
        />
      );
    }
    if (tsLoading) {
      return (
        <EndpointTunnelRow
          label={t("apis.tunnel.tailscale")}
          active
          copyId="ts_url"
          copied={copied}
          onCopy={copy}
          statusText={tsProgress || t("apis.tunnel.connecting")}
          statusTone="muted"
          trailing={
            tsAuthUrl ? (
              <Button size="sm" onClick={() => window.open(tsAuthUrl, "tailscale_auth", "width=600,height=700,noopener,noreferrer")}>
                Open login
              </Button>
            ) : undefined
          }
        />
      );
    }
    if (tsStatusMessage) {
      return (
        <EndpointTunnelRow
          label={t("apis.tunnel.tailscale")}
          copyId="ts_url"
          copied={copied}
          onCopy={copy}
          statusText={tsStatusMessage}
          statusTone="error"
          trailing={<Button size="sm" onClick={() => void openTailscale()}>Enable</Button>}
        />
      );
    }
    if (!tsEnabled) {
      return (
        <EndpointTunnelRow
          label={t("apis.tunnel.tailscale")}
          copyId="ts_url"
          copied={copied}
          onCopy={copy}
          trailing={<Button size="sm" onClick={() => void openTailscale()}>Enable</Button>}
        />
      );
    }
    if (tsHealthy) {
      return (
        <EndpointTunnelRow
          label={t("apis.tunnel.tailscale")}
          active
          url={normalizeBaseUrl(tsUrl)}
          copyId="ts_url"
          copied={copied}
          onCopy={copy}
          trailing={<PowerButton title={t("apis.tunnel.disableTailscale")} onClick={() => setShowDisableTsModal(true)} />}
        />
      );
    }
    return (
      <EndpointTunnelRow
        label={t("apis.tunnel.tailscale")}
        active
        copyId="ts_url"
        copied={copied}
        onCopy={copy}
        statusText={t("apis.tunnel.tailscaleReconnecting")}
        statusTone="warn"
        trailing={<PowerButton title={t("apis.tunnel.disableTailscale")} onClick={() => setShowDisableTsModal(true)} />}
      />
    );
  };

  return (
    <div className="tunnel-section">
      {renderTunnelRow()}
      {renderTailscaleRow()}

      {(tunnelEnabled || tsEnabled) && apiKeyCount === 0 ? (
        <div className="apis-card-warning">
          <SecurityWarning message={t("apis.tunnel.activeNoKeys")} />
        </div>
      ) : null}

      {tunnelEnabled || tsEnabled ? (
        <div className="endpoint-row tunnel-dashboard-row">
          <span className={tunnelDashboardAccess ? "endpoint-row-badge active" : "endpoint-row-badge"}>{t("apis.tunnel.dashboard")}</span>
          <label className="tunnel-dashboard-toggle">
            <Toggle checked={tunnelDashboardAccess} onChange={() => void handleDashboardAccess(!tunnelDashboardAccess)} />
            <span>{t("apis.tunnel.allowDashboard")}</span>
          </label>
        </div>
      ) : null}

      <Modal open={showEnableModal} title={t("apis.tunnel.enableCloudflareTitle")} onClose={closeCloudflare}>
        <form onSubmit={(event) => { event.preventDefault(); void handleEnableTunnel(); }}>
          <p className="modal-copy">{t("apis.tunnel.cloudflareDesc")}</p>
          <div style={{ display: "grid", gap: 16 }}>
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
          </div>
          <p className="modal-copy">{t("apis.tunnel.routeHint")} <code>{tunnel?.serviceUrl || "http://127.0.0.1:28120"}</code></p>
          <p className="modal-copy">
            <a href="https://one.dash.cloudflare.com/" target="_blank" rel="noopener noreferrer">{t("apis.tunnel.openCloudflare")}</a>
          </p>
          {tunnelStatusMessage ? <p className="modal-copy" role="alert">{tunnelStatusMessage}</p> : null}
          {tunnelLoading ? <p className="modal-copy" role="status">{tunnelProgress}</p> : null}
          <div className="modal-actions">
            <Button type="submit" loading={tunnelLoading} disabled={!tunnelHostname.trim() || (!tunnelToken.trim() && !tunnel?.tokenConfigured)}>{t("apis.tunnel.startTunnel")}</Button>
            <Button type="button" variant="ghost" disabled={tunnelLoading} onClick={closeCloudflare}>{t("common.cancel")}</Button>
          </div>
        </form>
      </Modal>

      <ConfirmDialog
        open={showDisableModal}
        title={t("apis.tunnel.disableTunnel")}
        message={t("apis.tunnel.disableTunnelConfirm")}
        confirmText={t("common.disable")}
        variant="danger"
        onConfirm={() => void handleDisableTunnel()}
        onClose={() => setShowDisableModal(false)}
      />

      <Modal open={showTsModal} title={t("apis.tunnel.tailscaleFunnelTitle")} onClose={() => setShowTsModal(false)}>
        <p className="modal-copy">
          {tsInstalled === false
            ? t("apis.tunnel.installTailscale")
            : t("apis.tunnel.connectTailscale")}
        </p>
        <div className="modal-actions">
          <Button onClick={() => void (tsInstalled === false ? openTailscale() : handleConnectTailscale())}>
            {tsInstalled === false ? t("apis.tunnel.retryCheck") : "Connect"}
          </Button>
          <Button variant="ghost" onClick={() => setShowTsModal(false)}>Cancel</Button>
        </div>
      </Modal>

      <ConfirmDialog
        open={showDisableTsModal}
        title={t("apis.tunnel.disableTailscale")}
        message={t("apis.tunnel.disableTailscaleConfirm")}
        confirmText={t("common.disable")}
        variant="danger"
        onConfirm={() => void handleDisableTailscale()}
        onClose={() => setShowDisableTsModal(false)}
      />
    </div>
  );
}
