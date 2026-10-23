import { isAutoApplyTool, type CLITool } from "../../cli-tools/constants";
import type { Tone } from "../page-kit";
import type { CLIToolStatus } from "./api";

/** Whether a tool's local config points at this gateway (only knowable for auto-apply tools). */
export type ConfigState = "connected" | "other" | "not_configured";

/**
 * What the operator should know about a tool at a glance:
 * - connected: the local config already points at tproxy
 * - other: installed and configured, but against an endpoint this dashboard never hands out
 * - not_configured: installed, not pointed at tproxy yet
 * - not_installed: auto-apply tool that was not detected on this machine
 * - manual: no auto-apply; the operator copies values/config by hand
 * - unknown: the status endpoint did not report this tool
 * - checking: status request in flight
 * - unsupported: MITM tools, which tproxy does not intercept
 */
export type ToolState =
  | "connected"
  | "other"
  | "not_configured"
  | "not_installed"
  | "manual"
  | "unknown"
  | "checking"
  | "unsupported";

/** Filter buckets shared by the stat tiles and the filter tabs. */
export type ToolBucket = "connected" | "setup" | "missing" | "manual";
export const TOOL_BUCKETS: ToolBucket[] = ["connected", "setup", "missing", "manual"];

export const TOOL_STATE_TONE: Record<ToolState, Tone> = {
  connected: "success",
  other: "warning",
  not_configured: "warning",
  not_installed: "neutral",
  manual: "info",
  unknown: "muted",
  checking: "muted",
  unsupported: "muted",
};

export const TOOL_BUCKET_TONE: Record<ToolBucket, Tone> = {
  connected: "success",
  setup: "warning",
  missing: "neutral",
  manual: "info",
};

export const TOOL_BUCKET_ICON: Record<ToolBucket, string> = {
  connected: "check_circle",
  setup: "build",
  missing: "download",
  manual: "content_copy",
};

/** Sort rank for "by status": things that need the operator first. */
const STATE_RANK: Record<ToolState, number> = {
  other: 0,
  not_configured: 1,
  connected: 2,
  manual: 3,
  not_installed: 4,
  checking: 5,
  unknown: 6,
  unsupported: 7,
};

export function stateRank(state: ToolState): number {
  return STATE_RANK[state];
}

export function bucketOf(state: ToolState): ToolBucket | null {
  if (state === "connected") return "connected";
  if (state === "other" || state === "not_configured") return "setup";
  if (state === "not_installed") return "missing";
  if (state === "manual") return "manual";
  return null;
}

function sameEndpoint(a: string, b: string): boolean {
  const strip = (value: string) => value.trim().replace(/\/+$/, "").replace(/\/v1$/, "").toLowerCase();
  return Boolean(a) && Boolean(b) && strip(a) === strip(b);
}

/**
 * Three-way status like 9router's matchKnownEndpoint: a tool pointed at an endpoint
 * this dashboard never hands out is "Other", not "Connected" — otherwise a stale
 * config silently reads as healthy.
 */
export function configState(status: CLIToolStatus | null | undefined, knownBaseUrls: string[]): ConfigState | null {
  if (!status?.installed) return null;
  const endpoint = status.endpoint ?? "";
  if (!endpoint) return status.has_tproxy || status.has_9router ? "connected" : "not_configured";
  if (knownBaseUrls.some((known) => sameEndpoint(known, endpoint))) return "connected";
  if (status.has_tproxy || status.has_9router) return "connected";
  return "other";
}

export function toolState(
  tool: CLITool,
  status: CLIToolStatus | null | undefined,
  knownBaseUrls: string[],
  loading = false,
): ToolState {
  if (tool.configType === "mitm") return "unsupported";
  if (!isAutoApplyTool(tool.id)) return "manual";
  if (!status) return loading ? "checking" : "unknown";
  return configState(status, knownBaseUrls) ?? "not_installed";
}

/** The local config file a tool reads, preferring what the backend reported. */
export function toolConfigPath(tool: CLITool, status?: CLIToolStatus | null): string {
  return status?.config_path || status?.settings_path || tool.settingsFile || "";
}
