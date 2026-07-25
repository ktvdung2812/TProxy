import { buildHostBaseUrl, buildLocalGatewayBaseUrl, normalizeBaseUrl } from "../components/apis/utils";
import { normalizeCliBaseUrl, normalizePublicOrigin } from "./publicBaseUrl";

export type CliBaseUrlKind = "local" | "lan" | "tunnel";

const KIND_STORAGE_KEY = "tproxy.cli-base-url-kind";
const LAN_IP_STORAGE_KEY = "tproxy.cli-lan-ip";

export function readStoredCliBaseUrlKind(): CliBaseUrlKind | null {
  if (typeof window === "undefined") return null;
  try {
    const stored = localStorage.getItem(KIND_STORAGE_KEY);
    if (stored === "local" || stored === "lan" || stored === "tunnel") return stored;
  } catch {
    /* ignore */
  }
  return null;
}

export function storeCliBaseUrlKind(kind: CliBaseUrlKind) {
  if (typeof window === "undefined") return;
  try {
    localStorage.setItem(KIND_STORAGE_KEY, kind);
  } catch {
    /* ignore */
  }
}

export function readStoredCliLanIP(): string {
  if (typeof window === "undefined") return "";
  try {
    return localStorage.getItem(LAN_IP_STORAGE_KEY) || "";
  } catch {
    return "";
  }
}

export function storeCliLanIP(ip: string) {
  if (typeof window === "undefined") return;
  try {
    const trimmed = ip.trim();
    if (trimmed) localStorage.setItem(LAN_IP_STORAGE_KEY, trimmed);
    else localStorage.removeItem(LAN_IP_STORAGE_KEY);
  } catch {
    /* ignore */
  }
}

export type CliGatewaySettings = {
  serverPort: number;
  allowLan: boolean;
  lanIPs: string[];
  publicBaseUrl: string;
};

export function buildCliLocalBaseUrl(serverPort: number): string {
  return buildLocalGatewayBaseUrl(serverPort);
}

export function buildCliLanBaseUrl(ip: string, serverPort: number): string {
  return buildHostBaseUrl(ip, serverPort);
}

export function buildCliTunnelBaseUrl(publicBaseUrl: string): string {
  const origin = normalizePublicOrigin(publicBaseUrl);
  return origin ? normalizeCliBaseUrl(origin) : "";
}

export function resolveCliBaseUrlForKind(
  kind: CliBaseUrlKind,
  options: {
    settings: CliGatewaySettings;
    publicUrlOverride?: string;
    lanIP?: string;
  },
): string {
  const { settings, publicUrlOverride = "", lanIP = "" } = options;

  if (kind === "local") {
    return buildCliLocalBaseUrl(settings.serverPort);
  }

  if (kind === "lan") {
    const ip = lanIP || settings.lanIPs[0] || "";
    if (ip) return buildCliLanBaseUrl(ip, settings.serverPort);
    return buildCliLocalBaseUrl(settings.serverPort);
  }

  const tunnelOrigin =
    normalizePublicOrigin(settings.publicBaseUrl || "") || normalizePublicOrigin(publicUrlOverride);
  if (tunnelOrigin) return normalizeCliBaseUrl(tunnelOrigin);

  if (typeof window !== "undefined" && !["localhost", "127.0.0.1", "::1"].includes(window.location.hostname)) {
    return normalizeBaseUrl(window.location.origin);
  }

  return buildCliLocalBaseUrl(settings.serverPort);
}

export function defaultCliBaseUrlKind(settings: CliGatewaySettings, publicUrlOverride = ""): CliBaseUrlKind {
  const tunnel = buildCliTunnelBaseUrl(settings.publicBaseUrl || publicUrlOverride);
  if (tunnel) return "tunnel";
  if (settings.allowLan && settings.lanIPs.length > 0) return "lan";
  return "local";
}
