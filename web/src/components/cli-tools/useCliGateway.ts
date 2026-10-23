import { useEffect, useMemo, useState } from "react";
import { fetchAdminSettings } from "../settings/api";
import { fetchTunnelStatus } from "../apis/tunnelApi";
import { readStoredPublicBaseUrl, storePublicBaseUrl } from "../../lib/publicBaseUrl";
import {
  buildCliLanBaseUrl,
  buildCliTailscaleBaseUrl,
  buildCliTunnelBaseUrl,
  defaultCliBaseUrlKind,
  knownCliBaseUrls,
  readStoredCliBaseUrlKind,
  readStoredCliLanIP,
  resolveCliBaseUrlForKind,
  storeCliBaseUrlKind,
  storeCliLanIP,
  type CliBaseUrlKind,
  type CliGatewaySettings,
} from "../../lib/cliBaseUrl";

export type CliGateway = ReturnType<typeof useCliGateway>;

/**
 * Which base URLs this gateway can hand out (localhost, LAN, tunnel, Tailscale)
 * and which one the operator picked. Shared by the tools list — to tell
 * "connected to us" from "connected elsewhere" — and the setup page.
 */
export function useCliGateway(secret: string) {
  const [settings, setSettings] = useState<CliGatewaySettings>({
    serverPort: 28120,
    allowLan: false,
    lanIPs: [],
    publicBaseUrl: "",
  });
  const [publicUrlOverride, setPublicUrlOverrideState] = useState(() => readStoredPublicBaseUrl());
  const [kind, setKindState] = useState<CliBaseUrlKind>("local");
  const [lanIP, setLanIPState] = useState(() => readStoredCliLanIP());
  const [settingsLoaded, setSettingsLoaded] = useState(false);

  useEffect(() => {
    if (!secret) return;
    let cancelled = false;
    void fetchAdminSettings(secret)
      .then((response) => {
        if (cancelled) return;
        const next: CliGatewaySettings = {
          serverPort: response.server_port || 28120,
          allowLan: Boolean(response.allow_lan_management),
          lanIPs: response.lan_ips || [],
          publicBaseUrl: response.public_base_url || "",
        };
        // Keep the Tailscale URL resolved by the tunnel effect; it is not part of
        // gateway settings and the two effects race.
        setSettings((prev) => ({ ...next, tailscaleUrl: prev.tailscaleUrl }));
        setKindState(() => {
          const stored = readStoredCliBaseUrlKind();
          if (stored === "tunnel" && !buildCliTunnelBaseUrl(next.publicBaseUrl || publicUrlOverride)) {
            return "local";
          }
          if (stored === "lan" && (!next.allowLan || next.lanIPs.length === 0)) {
            return "local";
          }
          // "tailscale" is validated by the tunnel effect once its status arrives.
          if (stored) return stored;
          return defaultCliBaseUrlKind(next, publicUrlOverride);
        });
      })
      .catch(() => {
        /* settings endpoint may be unavailable during startup */
      })
      .finally(() => {
        if (!cancelled) setSettingsLoaded(true);
      });
    return () => {
      cancelled = true;
    };
  }, [secret, publicUrlOverride]);

  // Tailscale Funnel lives on the tunnel endpoint, not in gateway settings.
  useEffect(() => {
    if (!secret) return;
    let cancelled = false;
    void fetchTunnelStatus(secret)
      .then((status) => {
        if (cancelled) return;
        const url = status.tailscale?.running ? status.tailscale?.tunnelUrl ?? "" : "";
        setSettings((prev) => (prev.tailscaleUrl === url ? prev : { ...prev, tailscaleUrl: url }));
        if (!url) {
          setKindState((prev) => (prev === "tailscale" ? "local" : prev));
        }
      })
      .catch(() => {
        /* tunnel endpoint may be unavailable */
      });
    return () => {
      cancelled = true;
    };
  }, [secret]);

  const effectiveLanIP = lanIP || settings.lanIPs[0] || "";
  const lanUrl = useMemo(
    () => (effectiveLanIP ? buildCliLanBaseUrl(effectiveLanIP, settings.serverPort) : ""),
    [effectiveLanIP, settings.serverPort],
  );
  const tunnelUrl = useMemo(
    () => buildCliTunnelBaseUrl(settings.publicBaseUrl || publicUrlOverride),
    [settings.publicBaseUrl, publicUrlOverride],
  );
  const tailscaleUrl = useMemo(() => buildCliTailscaleBaseUrl(settings.tailscaleUrl || ""), [settings.tailscaleUrl]);
  const knownBaseUrls = useMemo(() => knownCliBaseUrls(settings, publicUrlOverride), [settings, publicUrlOverride]);
  const baseUrl = useMemo(
    () => resolveCliBaseUrlForKind(kind, { settings, publicUrlOverride, lanIP: effectiveLanIP }),
    [kind, settings, publicUrlOverride, effectiveLanIP],
  );

  const setKind = (next: CliBaseUrlKind) => {
    setKindState(next);
    storeCliBaseUrlKind(next);
  };

  const setLanIP = (ip: string) => {
    setLanIPState(ip);
    storeCliLanIP(ip);
  };

  const setPublicUrlOverride = (value: string) => {
    setPublicUrlOverrideState(value);
    storePublicBaseUrl(value);
  };

  return {
    settings,
    settingsLoaded,
    kind,
    setKind,
    effectiveLanIP,
    setLanIP,
    lanUrl,
    tunnelUrl,
    tailscaleUrl,
    knownBaseUrls,
    baseUrl,
    publicUrlOverride,
    setPublicUrlOverride,
  };
}
