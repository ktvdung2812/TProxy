export type CoworkPlugin = {
  name: string;
  title?: string;
  url: string;
  transport?: string;
  oauth?: boolean;
  toolNames?: string[];
  custom?: boolean;
};

export type CLIToolStatus = {
  installed: boolean;
  has_tproxy: boolean;
  has_9router: boolean;
  settings_path?: string;
  config_path?: string;
  message?: string;
  /** Endpoint the tool is currently pointed at — empty when unconfigured. */
  endpoint?: string;
  model?: string;
  models?: string[];
  /** Cowork only: managed MCP catalog and the entries currently written. */
  plugins?: CoworkPlugin[];
  active_plugins?: string[];
};

export type CLIToolApplyRequest = {
  baseUrl: string;
  apiKey: string;
  model: string;
  models?: string[];
  subagentModel?: string;
  env?: Record<string, string>;
  /** Cowork only: managed MCP servers to write (empty array = write none). */
  plugins?: CoworkPlugin[];
  customPlugins?: CoworkPlugin[];
};

function authHeaders(secret: string): Record<string, string> {
  return secret ? { Authorization: `Bearer ${secret}` } : {};
}

async function parseResponse<T>(response: Response): Promise<T> {
  const data = (await response.json()) as T & { error?: { message?: string } };
  if (!response.ok) {
    throw new Error(data.error?.message ?? `Request failed (${response.status})`);
  }
  return data;
}

export async function fetchCLIToolStatuses(secret: string): Promise<Record<string, CLIToolStatus>> {
  const response = await fetch("/api/admin/cli-tools/status", { headers: authHeaders(secret) });
  const data = await parseResponse<{ statuses: Record<string, CLIToolStatus> }>(response);
  return data.statuses ?? {};
}

export async function fetchCLIToolStatus(secret: string, toolId: string): Promise<CLIToolStatus> {
  const response = await fetch(`/api/admin/cli-tools/${encodeURIComponent(toolId)}`, {
    headers: authHeaders(secret),
  });
  return parseResponse<CLIToolStatus>(response);
}

export async function applyCLIToolConfig(secret: string, toolId: string, body: CLIToolApplyRequest): Promise<void> {
  const response = await fetch(`/api/admin/cli-tools/${encodeURIComponent(toolId)}`, {
    method: "POST",
    headers: { ...authHeaders(secret), "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  await parseResponse<{ success: boolean }>(response);
}

export async function resetCLIToolConfig(secret: string, toolId: string): Promise<void> {
  const response = await fetch(`/api/admin/cli-tools/${encodeURIComponent(toolId)}`, {
    method: "DELETE",
    headers: authHeaders(secret),
  });
  await parseResponse<{ success: boolean }>(response);
}
