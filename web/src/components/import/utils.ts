export type ImportSource = "9router" | "cliproxy";

export function detectImportSource(payload: unknown): ImportSource | null {
  if (Array.isArray(payload)) {
    const first = payload[0];
    if (isCliproxyAuth(first)) {
      return "cliproxy";
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
