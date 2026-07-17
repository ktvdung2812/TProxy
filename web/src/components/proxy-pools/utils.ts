export function formatDateTime(value?: string) {
  if (!value) return "Never";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "Never";
  return date.toLocaleString();
}

export function statusVariant(status?: string): "success" | "error" | "default" {
  if (status === "healthy" || status === "active") return "success";
  if (status === "error") return "error";
  return "default";
}

export function suggestPoolId(name: string) {
  const base = name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
  return base || `proxy-pool-${Date.now()}`;
}

export function parseProxyLine(line: string) {
  const trimmed = line.trim();
  if (!trimmed) return null;

  if (trimmed.includes("://")) {
    const parsed = new URL(trimmed);
    const hostLabel = parsed.port ? `${parsed.hostname}:${parsed.port}` : parsed.hostname;
    return {
      proxyUrl: parsed.toString(),
      name: `Imported ${hostLabel}`,
    };
  }

  const parts = trimmed.split(":");
  if (parts.length === 4) {
    const [host, port, username, password] = parts;
    if (!host || !port || !username || !password) {
      throw new Error("Invalid host:port:user:pass format");
    }
    const proxyUrl = `http://${encodeURIComponent(username)}:${encodeURIComponent(password)}@${host}:${port}`;
    return {
      proxyUrl: new URL(proxyUrl).toString(),
      name: `Imported ${host}:${port}`,
    };
  }

  throw new Error("Unsupported format");
}
