#!/usr/bin/env node
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { findGo } from "./toolchain.mjs";
import { runForeground } from "./run-foreground.mjs";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const go = findGo();
if (!go) {
  console.error("Go is not installed. Run: npm run dev");
  process.exit(1);
}
runForeground(go, process.argv.slice(2), { cwd: root });
