import { useCallback, useEffect, useRef, useState } from "react";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";
import { Button, ConfirmDialog, Modal, Toggle } from "../ui";
import { EndpointRow } from "./EndpointRow";
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
const PING_INTERVAL_MS = 2000;
const PING_MAX_MS = 120000;
const MISS_THRESHOLD = 2;

function TunnelRow({
  label,
  active,
  url,
  copyId,
  copied,
  onCopy,
  actions,
}: {
  label: string;
  active?: boolean;
  url?: string;
  copyId: string;
  copied: string | null;
  onCopy: (text: string, id: string) => void;
  actions: React.ReactNode;
}) {
  if (url) {
    return (
      <EndpointRow label={label} url={url} copyId={copyId} copied={copied} onCopy={onCopy} actions={actions} />
    );
  }
  return (
    <div className="endpoint-row">
      <span className={active ? "endpoint-row-badge active" : "endpoint-row-badge"}>{label}</span>
      <div className="endpoint-row-placeholder" />
      <div className="endpoint-row-actions">{actions}</div>
    </div>
  );
}

export function TunnelSection({ secret, apiKeyCount, onError, onNotice }: Props) {
  const { copied, copy } = useCopyToClipboard();
  const [tunnel, setTunnel] = useState<CloudflareTunnelStatus | null>(null);
  const [tailscale, setTailscale] = useState<TailscaleTunnelStatus | null>(null);
  const [tunnelDashboardAccess, setTunnelDashboardAccess] = useState(true);
  const [loading, setLoading] = useState(true);
  const [tunnelLoading, setTunnelLoading] = useState(false);
  const [tsLoading, setTsLoading] = useState(false);
  const [tunnelProgress, setTunnelProgress] = useState("");
  const [tsProgress, setTsProgress] = useState("");
  const [tunnelReachable, setTunnelReachable] = useState(false);
  const [tsReachable, setTsReachable] = useState(false);
  const [tunnelStatusMessage, setTunnelStatusMessage] = useState<string | null>(null);
  const [tsStatusMessage, setTsStatusMessage] = useState<string | null>(null);
  const [tsAuthUrl, setTsAuthUrl] = useState("");
  const [tsInstalled, setTsInstalled] = useState<boolean | null>(null);
  const [showEnableModal, setShowEnableModal] = useState(false);
  const [showDisableModal, setShowDisableModal] = useState(false);
  const [showDisableTsModal, setShowDisableTsModal] = useState(false);
  const [showTsModal, setShowTsModal] = useState(false);
  const tunnelMissRef = useRef(0);
  const tsMissRef = useRef(0);

  const syncStatus = useCallback(async () => {
    try {
      const data = await fetchTunnelStatus(secret);
      setTunnel(data.tunnel);
      setTailscale(data.tailscale);
    } catch (error) {
      onError(error instanceof Error ? error.message : "Failed to load tunnel status");
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
        }
      } catch {
        // ignore
      }
    })();
  }, [secret]);

  const tunnelEnabled = tunnel?.settingsEnabled ?? false;
  const tsEnabled = tailscale?.settingsEnabled ?? false;
  const tunnelPublicUrl = tunnel?.publicUrl || tunnel?.tunnelUrl || "";
  const tsUrl = tailscale?.tunnelUrl || "";

  useEffect(() => {
    if (!tunnelEnabled && !tsEnabled) return;
    const timer = setInterval(() => void syncStatus(), STATUS_POLL_MS);
    return () => clearInterval(timer);
  }, [tunnelEnabled, tsEnabled, syncStatus]);

  useEffect(() => {
    if (!tunnelEnabled && !tsEnabled) return;
    const probe = async () => {
      if (tunnelEnabled && tunnelPublicUrl) {
        const ok = await pingAnyHealth(tunnel?.publicUrl, tunnel?.tunnelUrl);
        if (ok) {
          tunnelMissRef.current = 0;
          setTunnelReachable(true);
        } else {
          tunnelMissRef.current += 1;
          if (tunnelMissRef.current >= MISS_THRESHOLD) setTunnelReachable(false);
        }
      }
      if (tsEnabled && tsUrl) {
        const ok = await pingHealth(tsUrl);
        if (ok) {
          tsMissRef.current = 0;
          setTsReachable(true);
        } else {
          tsMissRef.current += 1;
          if (tsMissRef.current >= MISS_THRESHOLD) setTsReachable(false);
        }
      }
    };
    void probe();
    const timer = setInterval(() => void probe(), PING_INTERVAL_MS);
    return () => clearInterval(timer);
  }, [tunnelEnabled, tsEnabled, tunnelPublicUrl, tsUrl, tunnel?.publicUrl, tunnel?.tunnelUrl]);

  const waitForTunnel = async (publicUrl?: string, directUrl?: string) => {
    const start = Date.now();
    while (Date.now() - start < PING_MAX_MS) {
      await new Promise((resolve) => setTimeout(resolve, PING_INTERVAL_MS));
      if (await pingAnyHealth(publicUrl, directUrl)) {
        setTunnelReachable(true);
        return true;
      }
    }
    return false;
  };

  const handleEnableTunnel = async () => {
    setShowEnableModal(false);
    setTunnelLoading(true);
    setTunnelStatusMessage(null);
    setTunnelProgress("Creating tunnel...");
    let polling = true;
    void (async () => {
      while (polling) {
        try {
          const status = await fetchTunnelStatus(secret);
          if (status.download.downloading) {
            setTunnelProgress(`Downloading cloudflared... ${status.download.progress}%`);
          }
        } catch {
          // ignore
        }
        await new Promise((resolve) => setTimeout(resolve, 1000));
      }
    })();
    try {
      const result = await enableTunnel(secret);
      if (!result.success) {
        setTunnelStatusMessage(result.error || "Failed to enable tunnel");
        return;
      }
      await waitForTunnel(result.publicUrl, result.tunnelUrl);
      onNotice("Tunnel enabled");
      await syncStatus();
    } catch (error) {
      setTunnelStatusMessage(error instanceof Error ? error.message : "Failed to enable tunnel");
    } finally {
      polling = false;
      setTunnelLoading(false);
      setTunnelProgress("");
    }
  };

  const handleDisableTunnel = async () => {
    setTunnelLoading(true);
    try {
      await disableTunnel(secret);
      setShowDisableModal(false);
      setTunnelReachable(false);
      onNotice("Tunnel disabled");
      await syncStatus();
    } catch (error) {
      onError(error instanceof Error ? error.message : "Failed to disable tunnel");
    } finally {
      setTunnelLoading(false);
    }
  };

  const handleConnectTailscale = async () => {
    setShowTsModal(false);
    setTsLoading(true);
    setTsProgress("Connecting...");
    setTsAuthUrl("");
    try {
      const result = await enableTailscale(secret);
      if (result.needsLogin && result.authUrl) {
        setTsAuthUrl(result.authUrl);
        setTsProgress("Login required — open the Tailscale login page");
        for (let i = 0; i < 40; i++) {
          await new Promise((resolve) => setTimeout(resolve, 3000));
          const check = await checkTailscale(secret);
          if (check.logged_in) {
            setTsAuthUrl("");
            const retry = await enableTailscale(secret);
            if (retry.success && retry.tunnelUrl) {
              await pingHealth(retry.tunnelUrl);
              onNotice("Tailscale funnel enabled");
              await syncStatus();
              return;
            }
          }
        }
        setTsStatusMessage("Login timed out. Please try again.");
        return;
      }
      if (result.funnelNotEnabled && result.enableUrl) {
        setTsAuthUrl(result.enableUrl);
        setTsProgress("Enable Funnel in Tailscale admin");
        setTsStatusMessage("Funnel is not enabled on your tailnet.");
        return;
      }
      if (!result.success) {
        setTsStatusMessage(result.error || "Failed to connect Tailscale");
        return;
      }
      if (result.tunnelUrl) {
        await pingHealth(result.tunnelUrl);
      }
      onNotice("Tailscale funnel enabled");
      await syncStatus();
    } catch (error) {
      setTsStatusMessage(error instanceof Error ? error.message : "Failed to connect Tailscale");
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
      onError(error instanceof Error ? error.message : "Tailscale check failed");
    }
  };

  const handleDisableTailscale = async () => {
    setTsLoading(true);
    try {
      await disableTailscale(secret);
      setShowDisableTsModal(false);
      setTsReachable(false);
      onNotice("Tailscale disabled");
      await syncStatus();
    } catch (error) {
      onError(error instanceof Error ? error.message : "Failed to disable Tailscale");
    } finally {
      setTsLoading(false);
    }
  };

  const handleDashboardAccess = async (value: boolean) => {
    try {
      const result = await saveTunnelDashboardAccess(secret, value);
      setTunnelDashboardAccess(result.tunnel_dashboard_access);
    } catch (error) {
      onError(error instanceof Error ? error.message : "Failed to update tunnel dashboard access");
    }
  };

  const renderTunnelActions = () => {
    if (loading) return <span className="endpoint-row-status">Checking...</span>;
    if (tunnelEnabled && tunnelReachable && !tunnelLoading) {
      return (
        <button type="button" className="endpoint-row-power" onClick={() => setShowDisableModal(true)} title="Disable tunnel">
          <span className="material-symbols-outlined">power_settings_new</span>
        </button>
      );
    }
    if (tunnelEnabled && !tunnelLoading) {
      return (
        <>
          <span className="endpoint-row-status warn">Reconnecting...</span>
          <button type="button" className="endpoint-row-power" onClick={() => setShowDisableModal(true)} title="Disable tunnel">
            <span className="material-symbols-outlined">power_settings_new</span>
          </button>
        </>
      );
    }
    if (tunnelLoading) return <span className="endpoint-row-status">{tunnelProgress || "Creating tunnel..."}</span>;
    if (tunnelStatusMessage) {
      return (
        <>
          <span className="endpoint-row-status error">{tunnelStatusMessage}</span>
          <Button size="sm" onClick={() => setShowEnableModal(true)}>Enable</Button>
        </>
      );
    }
    return (
      <Button
        size="sm"
        onClick={() => {
          if (apiKeyCount === 0) {
            setTunnelStatusMessage("Create at least one API key before enabling the tunnel.");
            return;
          }
          setShowEnableModal(true);
        }}
      >
        Enable
      </Button>
    );
  };

  const renderTailscaleActions = () => {
    if (loading) return <span className="endpoint-row-status">Checking...</span>;
    if (tsEnabled && tsReachable && !tsLoading) {
      return (
        <button type="button" className="endpoint-row-power" onClick={() => setShowDisableTsModal(true)} title="Disable Tailscale">
          <span className="material-symbols-outlined">power_settings_new</span>
        </button>
      );
    }
    if (tsEnabled && !tsLoading) {
      return (
        <>
          <span className="endpoint-row-status warn">Reconnecting...</span>
          <button type="button" className="endpoint-row-power" onClick={() => setShowDisableTsModal(true)} title="Disable Tailscale">
            <span className="material-symbols-outlined">power_settings_new</span>
          </button>
        </>
      );
    }
    if (tsLoading) {
      return (
        <>
          <span className="endpoint-row-status">{tsProgress || "Connecting..."}</span>
          {tsAuthUrl ? (
            <Button size="sm" onClick={() => window.open(tsAuthUrl, "tailscale_auth", "width=600,height=700,noopener,noreferrer")}>
              Open login
            </Button>
          ) : null}
        </>
      );
    }
    if (tsStatusMessage) {
      return (
        <>
          <span className="endpoint-row-status error">{tsStatusMessage}</span>
          <Button size="sm" onClick={() => void openTailscale()}>Enable</Button>
        </>
      );
    }
    return <Button size="sm" onClick={() => void openTailscale()}>Enable</Button>;
  };

  return (
  <div className="tunnel-section">
      <TunnelRow
        label="Tunnel"
        active={tunnelEnabled}
        url={tunnelEnabled && tunnelPublicUrl ? normalizeBaseUrl(tunnelPublicUrl) : undefined}
        copyId="tunnel_url"
        copied={copied}
        onCopy={copy}
        actions={renderTunnelActions()}
      />

      <TunnelRow
        label="Tailscale"
        active={tsEnabled}
        url={tsEnabled && tsUrl ? normalizeBaseUrl(tsUrl) : undefined}
        copyId="ts_url"
        copied={copied}
        onCopy={copy}
        actions={renderTailscaleActions()}
      />

      {(tunnelEnabled || tsEnabled) && apiKeyCount === 0 ? (
        <div className="apis-card-warning">
          <SecurityWarning message="Tunnel is active but no API keys exist. Create a key so remote clients can authenticate." />
        </div>
      ) : null}

      {tunnelEnabled || tsEnabled ? (
        <div className="endpoint-row tunnel-dashboard-row">
          <span className={tunnelDashboardAccess ? "endpoint-row-badge active" : "endpoint-row-badge"}>Dashboard</span>
          <label className="tunnel-dashboard-toggle">
            <Toggle checked={tunnelDashboardAccess} onChange={() => void handleDashboardAccess(!tunnelDashboardAccess)} />
            <span>Allow dashboard via tunnel</span>
          </label>
        </div>
      ) : null}

      <Modal open={showEnableModal} title="Enable Cloudflare Tunnel" onClose={() => setShowEnableModal(false)}>
        <p className="modal-copy">
          Expose tproxy on the internet via Cloudflare quick tunnel. A stable short URL is registered automatically.
          Requires outbound port 7844. Setup may take 10–30 seconds.
        </p>
        <div className="modal-actions">
          <Button onClick={() => void handleEnableTunnel()}>Start tunnel</Button>
          <Button variant="ghost" onClick={() => setShowEnableModal(false)}>Cancel</Button>
        </div>
      </Modal>

      <ConfirmDialog
        open={showDisableModal}
        title="Disable tunnel"
        message="Remote access via the Cloudflare tunnel will stop working."
        confirmText="Disable"
        variant="danger"
        onConfirm={() => void handleDisableTunnel()}
        onClose={() => setShowDisableModal(false)}
      />

      <Modal open={showTsModal} title="Tailscale Funnel" onClose={() => setShowTsModal(false)}>
        <p className="modal-copy">
          {tsInstalled === false
            ? "Install Tailscale first (e.g. brew install tailscale), then click Connect."
            : "Connect Tailscale and start Funnel on this machine."}
        </p>
        <div className="modal-actions">
          <Button onClick={() => void (tsInstalled === false ? openTailscale() : handleConnectTailscale())}>
            {tsInstalled === false ? "Retry check" : "Connect"}
          </Button>
          <Button variant="ghost" onClick={() => setShowTsModal(false)}>Cancel</Button>
        </div>
      </Modal>

      <ConfirmDialog
        open={showDisableTsModal}
        title="Disable Tailscale"
        message="Tailscale Funnel will be stopped."
        confirmText="Disable"
        variant="danger"
        onConfirm={() => void handleDisableTailscale()}
        onClose={() => setShowDisableTsModal(false)}
      />
  </div>
  );
}
