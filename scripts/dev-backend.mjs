#!/usr/bin/env node
import { copyFileSync, existsSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { loadEnvRun } from "./env-run.mjs";
import { runForeground } from "./run-foreground.mjs";
import { ensureGo } from "./toolchain.mjs";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
loadEnvRun(root);

const go = await ensureGo();

const example = join(root, "config.example.yaml");
const config = join(root, "config.yaml");
if (!existsSync(config)) {
  copyFileSync(example, config);
  console.log("Created config.yaml from config.example.yaml");
}

const backendPort = process.env.TPROXY_DEV_BACKEND_PORT || "28122";
const publicPort = process.env.TPROXY_PUBLIC_PORT || "28120";
writeFileSync(join(root, ".config.dev.yaml"), rewriteServerPort(readFileSync(config, "utf8"), backendPort), "utf8");

process.env.TPROXY_PUBLIC_PORT = publicPort;
// Do not automatically reopen an internet-facing Cloudflare Tunnel in development.
// Enable it manually from APIs → Tunnel after the local dashboard is ready.
process.env.TPROXY_SKIP_TUNNEL_AUTO = "1";

console.log(`tproxy dev backend → http://127.0.0.1:${backendPort}`);
console.log(`public entry (dashboard + API) → http://127.0.0.1:${publicPort}`);
runForeground(go, ["run", "./cmd/tproxy", "--config", ".config.dev.yaml"], { cwd: root });

function rewriteServerPort(yaml, port) {
  const lines = yaml.split(/\r?\n/);
  let inServer = false;
  const out = [];
  for (const line of lines) {
    if (/^server:\s*$/.test(line)) {
      inServer = true;
      out.push(line);
      continue;
    }
    if (inServer && /^\S/.test(line)) {
      inServer = false;
    }
    if (inServer && /^\s+port:\s*/.test(line)) {
      out.push(line.replace(/port:\s*[0-9]+/, `port: ${port}`));
      inServer = false;
      continue;
    }
    out.push(line);
  }
  return `${out.join("\n")}${yaml.endsWith("\n") ? "" : "\n"}`;
}
