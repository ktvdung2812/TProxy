#!/usr/bin/env node
import { mkdirSync, rmSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { nativeBinaryFileName, nativeTargets } = require("../lib/native-platforms");
const scriptDir = dirname(fileURLToPath(import.meta.url));
const packageRoot = resolve(scriptDir, "..");
const repositoryRoot = resolve(packageRoot, "..");
const outputDir = resolve(packageRoot, "bin", "native");

const syncVersion = spawnSync(process.execPath, ["scripts/sync-version.mjs"], {
  cwd: repositoryRoot,
  stdio: "inherit",
});
if (syncVersion.status !== 0) {
  process.exit(syncVersion.status ?? 1);
}

rmSync(outputDir, { recursive: true, force: true });
mkdirSync(outputDir, { recursive: true });

for (const target of nativeTargets) {
  const output = resolve(outputDir, nativeBinaryFileName(target));
  process.stdout.write(`Building ${target.platform}-${target.arch}\n`);
  const result = spawnSync(
    "go",
    ["build", "-trimpath", "-p=1", "-ldflags=-s -w", "-o", output, "./cmd/tproxy"],
    {
      cwd: repositoryRoot,
      env: {
        ...process.env,
        CGO_ENABLED: "0",
        GOOS: target.goos,
        GOARCH: target.goarch,
        GOMAXPROCS: "2",
      },
      stdio: "inherit",
    },
  );

  if (result.status !== 0) {
    process.exit(result.status ?? 1);
  }
}
