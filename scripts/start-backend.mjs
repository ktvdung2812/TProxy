#!/usr/bin/env node
import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { loadEnvRun } from "./env-run.mjs";
import { runForeground } from "./run-foreground.mjs";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
loadEnvRun(root);

const bin = tproxyBin(root);
if (!bin) {
  console.error("bin/tproxy not found — run: npm run build");
  process.exit(1);
}

console.log("tproxy backend → http://127.0.0.1:28120");
runForeground(bin, ["--config", "config.yaml"], { cwd: root });

function tproxyBin(dir) {
  const names =
    process.platform === "win32" ? ["tproxy.exe", "tproxy"] : ["tproxy", "tproxy.exe"];
  for (const name of names) {
    const path = join(dir, "bin", name);
    if (existsSync(path)) {
      return path;
    }
  }
  return null;
}
