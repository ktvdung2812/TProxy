export type Import9routerResult = {
  ok: boolean;
  dry_run: boolean;
  counts: {
    providers: number;
    credentials: number;
    api_keys: number;
    models: number;
    combos: number;
    proxy_pools: number;
  };
  warnings: string[];
  errors: string[];
};

export type ImportCliproxyResult = {
  ok: boolean;
  dry_run: boolean;
  counts: {
    providers: number;
    credentials: number;
  };
  warnings: string[];
  errors: string[];
};

async function adminFetch<T>(
  secret: string,
  path: string,
  method: "GET" | "POST" = "GET",
  body?: unknown,
) {
  const response = await fetch(path, {
    method,
    headers: {
      ...(secret ? { Authorization: `Bearer ${secret}` } : {}),
      ...(body ? { "Content-Type": "application/json" } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    const message =
      data?.error?.message ||
      (Array.isArray(data?.errors) ? data.errors.join("\n") : "") ||
      `HTTP ${response.status}`;
    throw new Error(message);
  }
  return data as T;
}

export function import9routerBackup(secret: string, payload: unknown, dryRun = false) {
  const query = dryRun ? "?dry_run=true" : "";
  return adminFetch<Import9routerResult>(secret, `/api/admin/import/9router${query}`, "POST", payload);
}

export function importCliproxyAuth(secret: string, payload: unknown, dryRun = false) {
  const query = dryRun ? "?dry_run=true" : "";
  return adminFetch<ImportCliproxyResult>(secret, `/api/admin/import/cliproxyapi${query}`, "POST", payload);
}
