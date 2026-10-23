export type ImportResult = {
  source: "9router" | "cliproxy" | "sub2api" | "chatgpt" | "auth-bundle";
  total_accounts: number;
  result: {
    ok: boolean;
    dry_run: boolean;
    counts: Record<string, number>;
    warnings: string[] | null;
    errors: string[] | null;
  };
};

export async function importData(
  secret: string,
  payload: unknown,
  options: { dryRun: boolean; maxAccounts?: number; skipDuplicates: boolean },
): Promise<ImportResult> {
  const params = new URLSearchParams({
    dry_run: String(options.dryRun),
    skip_duplicates: String(options.skipDuplicates),
  });
  if (options.maxAccounts) params.set("max_accounts", String(options.maxAccounts));
  const response = await fetch(`/api/admin/import?${params}`, {
    method: "POST",
    headers: { "Content-Type": "application/json", ...(secret ? { Authorization: `Bearer ${secret}` } : {}) },
    body: JSON.stringify(payload),
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok && !data.result) {
    throw new Error(data?.error?.message || `HTTP ${response.status}`);
  }
  return data as ImportResult;
}
