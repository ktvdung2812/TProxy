#!/usr/bin/env node
import { copyFileSync, existsSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { loadEnvRun } from "./env-run.mjs";
import { runForeground } from "./run-foreground.mjs";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
loadEnvRun(root);

const go = resolveGo();
if (!go) {
  console.error("Go is not installed or not on PATH. Install it from https://go.dev/dl/ then reopen the terminal.");
  process.exit(1);
}

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
// Do not automatically reopen an internet-facing quick tunnel in development.
// Enable it manually from APIs → Tunnel after the local dashboard is ready.
process.env.TPROXY_SKIP_TUNNEL_AUTO = "1";

console.log(`tproxy dev backend → http://127.0.0.1:${backendPort}`);
console.log(`public entry (dashboard + API) → http://127.0.0.1:${publicPort}`);
runForeground(go, ["run", "./cmd/tproxy", "--config", ".config.dev.yaml"], { cwd: root });

function resolveGo() {
  const pathKey = process.env.Path ? "Path" : "PATH";
  const pathValue = process.env[pathKey] || process.env.PATH || "";
  const extra = [];
  if (process.platform === "win32") {
    extra.push(join(process.env.ProgramFiles || "C:\\Program Files", "Go", "bin"));
    extra.push(join(process.env["ProgramFiles(x86)"] || "C:\\Program Files (x86)", "Go", "bin"));
    extra.push(join(process.env.LOCALAPPDATA || "", "Programs", "Go", "bin"));
  }
  const names = process.platform === "win32" ? ["go.exe", "go"] : ["go"];
  for (const dir of [...extra, ...pathValue.split(process.platform === "win32" ? ";" : ":")]) {
    if (!dir) continue;
    for (const name of names) {
      const candidate = join(dir, name);
      if (existsSync(candidate)) {
        if (!pathValue.toLowerCase().includes(dir.toLowerCase())) {
          process.env[pathKey] = `${dir}${process.platform === "win32" ? ";" : ":"}${pathValue}`;
        }
        return candidate;
      }
    }
  }
  return null;
}

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
