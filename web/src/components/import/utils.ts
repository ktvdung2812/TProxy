export type ImportSource = "9router" | "cliproxy" | "sub2api";

export function detectImportSource(payload: unknown): ImportSource | null {
  if (Array.isArray(payload)) {
    const first = payload[0];
    if (isCliproxyAuth(first)) {
      return "cliproxy";
    }
    if (payload.some(isSub2apiAccount)) {
      return "sub2api";
    }
    return null;
  }
  if (!payload || typeof payload !== "object") {
    return null;
  }
  const record = payload as Record<string, unknown>;
  if ("providerConnections" in record || "apiKeys" in record || "combos" in record) {
    return "9router";
  }
  if (isSub2apiExport(record)) {
    return "sub2api";
  }
  if (isCliproxyAuth(record)) {
    return "cliproxy";
  }
  return null;
}

function isCliproxyAuth(value: unknown): boolean {
  if (!value || typeof value !== "object") {
    return false;
  }
  const record = value as Record<string, unknown>;
  const type = typeof record.type === "string" ? record.type.trim() : "";
  if (!type) {
    return false;
  }
  return typeof record.access_token === "string" || typeof record.api_key === "string";
}

// sub2api exports wrap everything in { exported_at, proxies, accounts } where
// each account carries platform + credentials.
function isSub2apiExport(value: unknown, depth = 0): boolean {
  if (depth > 8 || !value || typeof value !== "object") return false;
  if (Array.isArray(value)) return value.some(isSub2apiAccount);
  const record = value as Record<string, unknown>;
  if ("accounts" in record) {
    return Array.isArray(record.accounts) && record.accounts.some(isSub2apiAccount);
  }
  if (isSub2apiAccount(record)) {
    return true;
  }
  for (const key of ["data", "export", "payload", "account"]) {
    if (key in record) return isSub2apiExport(record[key], depth + 1);
  }
  return false;
}

function isSub2apiAccount(value: unknown): boolean {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return false;
  }
  const account = value as Record<string, unknown>;
  return (
    typeof account.platform === "string" &&
    account.platform.trim() !== "" &&
    account.credentials !== null &&
    typeof account.credentials === "object" &&
    !Array.isArray(account.credentials) &&
    Object.keys(account.credentials).length > 0
  );
}
