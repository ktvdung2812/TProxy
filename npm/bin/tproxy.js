#!/usr/bin/env node
const { spawn } = require("node:child_process");
const path = require("node:path");
const fs = require("node:fs");

const repoRoot = path.resolve(__dirname, "..", "..");
const binary = path.join(repoRoot, "bin", "tproxy");
const args = process.argv.slice(2);

function runGoRun() {
  const child = spawn("go", ["run", "./cmd/tproxy", ...args], {
    cwd: repoRoot,
    stdio: "inherit",
    env: process.env,
  });
  child.on("exit", (code) => process.exit(code ?? 0));
}

if (fs.existsSync(binary)) {
  const child = spawn(binary, args, { stdio: "inherit", env: process.env });
  child.on("exit", (code) => process.exit(code ?? 0));
} else {
  runGoRun();
}
