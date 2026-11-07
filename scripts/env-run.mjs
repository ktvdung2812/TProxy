import { randomBytes } from "node:crypto";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

// Secrets the local server needs. A missing one is generated once and kept in
// .env.run, so later runs reuse it and stored credentials stay decryptable.
const SECRETS = {
  TPROXY_MASTER_KEY: () => randomBytes(32).toString("base64url"),
  TPROXY_MANAGEMENT_SECRET: () => randomBytes(32).toString("hex"),
  TPROXY_API_KEY: () => `tp_${randomBytes(24).toString("base64url")}`,
};

// Unset, empty, or still the `replace-with-...` value copied from .env.example.
const isUnset = (value) => !value?.trim() || value.trim().startsWith("replace-with-");

/**
 * Load `.env.run` into `process.env` (same role as `source .env.run`), first
 * generating any secret that neither the shell nor `.env.run` provides.
 */
export function loadEnvRun(root) {
  const path = join(root, ".env.run");
  const text = existsSync(path) ? readFileSync(path, "utf8") : "";
  const values = parseEnv(text);

  const missing = Object.keys(SECRETS).filter((key) => isUnset(values[key]) && isUnset(process.env[key]));
  if (missing.length > 0) {
    const lines = text ? text.replace(/\s+$/, "").split(/\r?\n/) : ["# Local tproxy secrets. Keep this file private and back it up."];
    for (const key of missing) {
      values[key] = SECRETS[key]();
      const line = `${key}=${values[key]}`;
      const at = lines.findIndex((l) => new RegExp(`^\\s*(export\\s+)?${key}\\s*=`).test(l));
      if (at >= 0) lines[at] = line;
      else lines.push(line);
    }
    const eol = text.includes("\r\n") ? "\r\n" : "\n";
    writeFileSync(path, `${lines.join(eol)}${eol}`, { encoding: "utf8", mode: 0o600 });
    console.log(`Generated ${missing.join(", ")} in .env.run`);
    if (missing.includes("TPROXY_MASTER_KEY")) {
      console.log("Back up .env.run — TPROXY_MASTER_KEY decrypts stored provider credentials.");
    }
  }

  for (const [key, value] of Object.entries(values)) {
    if (!isUnset(value)) {
      process.env[key] = value;
    }
  }
}

function parseEnv(text) {
  const values = {};
  for (const raw of text.split(/\r?\n/)) {
    let line = raw.trim();
    if (!line || line.startsWith("#")) {
      continue;
    }
    if (line.startsWith("export ")) {
      line = line.slice(7).trim();
    }
    const eq = line.indexOf("=");
    if (eq <= 0) {
      continue;
    }
    const key = line.slice(0, eq).trim();
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) {
      continue;
    }
    let value = line.slice(eq + 1).trim();
    if (
      (value.startsWith('"') && value.endsWith('"')) ||
      (value.startsWith("'") && value.endsWith("'"))
    ) {
      value = value.slice(1, -1);
    }
    values[key] = value;
  }
  return values;
}
