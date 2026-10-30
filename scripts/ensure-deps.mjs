#!/usr/bin/env node
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const targets = [root, join(root, "web")];

for (const dir of targets) {
  ensureDeps(dir);
}

function ensureDeps(dir) {
  const pkgPath = join(dir, "package.json");
  if (!existsSync(pkgPath)) {
    return;
  }
  const pkg = JSON.parse(readFileSync(pkgPath, "utf8"));
  const fingerprint = depsFingerprint(dir);
  const stampPath = join(dir, "node_modules", ".cache", "tproxy-deps-stamp");
  const stamp = existsSync(stampPath) ? readFileSync(stampPath, "utf8").trim() : "";
  const missing = missingPackages(dir, pkg);
  if (missing.length === 0 && stamp === fingerprint) {
    return;
  }

  const label = relative(root, dir) || ".";
  if (missing.length > 0) {
    console.log(`Installing missing npm packages in ${label}: ${missing.join(", ")}`);
  } else {
    console.log(`Syncing npm packages in ${label}`);
  }

  const result = runNpmInstall(dir);
  if (result.error) {
    console.error(`npm install failed in ${label}: ${result.error.message}`);
    process.exit(1);
  }
  if ((result.status ?? 1) !== 0) {
    process.exit(result.status ?? 1);
  }

  const stillMissing = missingPackages(dir, pkg);
  if (stillMissing.length > 0) {
    console.error(
      `npm install in ${label} finished but these packages are still missing: ${stillMissing.join(", ")}`,
    );
    process.exit(1);
  }

  mkdirSync(dirname(stampPath), { recursive: true });
  writeFileSync(stampPath, `${depsFingerprint(dir)}\n`, "utf8");
}

function runNpmInstall(cwd) {
  const npmCli = join(dirname(process.execPath), "node_modules", "npm", "bin", "npm-cli.js");
  if (existsSync(npmCli)) {
    return spawnSync(process.execPath, [npmCli, "install"], {
      cwd,
      stdio: "inherit",
      env: process.env,
      windowsHide: true,
    });
  }
  const command = process.platform === "win32" ? "npm.cmd install" : "npm install";
  return spawnSync(command, {
    cwd,
    stdio: "inherit",
    shell: true,
    env: process.env,
    windowsHide: true,
  });
}

function depsFingerprint(dir) {
  const hash = createHash("sha256");
  for (const name of ["package.json", "package-lock.json"]) {
    const path = join(dir, name);
    if (!existsSync(path)) {
      continue;
    }
    hash.update(name);
    hash.update("\0");
    hash.update(readFileSync(path));
    hash.update("\0");
  }
  return hash.digest("hex");
}

function missingPackages(dir, pkg) {
  const names = [
    ...Object.keys(pkg.dependencies || {}),
    ...Object.keys(pkg.devDependencies || {}),
  ];
  const missing = [];
  for (const name of names) {
    if (!packageReady(dir, name)) {
      missing.push(name);
    }
  }
  return missing;
}

function packageReady(dir, name) {
  const pkgJsonPath = join(dir, "node_modules", ...name.split("/"), "package.json");
  if (!existsSync(pkgJsonPath)) {
    return false;
  }
  let meta;
  try {
    meta = JSON.parse(readFileSync(pkgJsonPath, "utf8"));
  } catch {
    return false;
  }
  for (const bin of binNames(meta)) {
    if (!binExists(dir, bin)) {
      return false;
    }
  }
  return true;
}

function binNames(meta) {
  const bins = meta.bin;
  if (!bins) {
    return [];
  }
  if (typeof bins === "string") {
    return [meta.name];
  }
  return Object.keys(bins);
}

function binExists(dir, name) {
  const base = join(dir, "node_modules", ".bin", name);
  if (existsSync(base)) {
    return true;
  }
  if (process.platform !== "win32") {
    return false;
  }
  return existsSync(`${base}.cmd`) || existsSync(`${base}.ps1`) || existsSync(`${base}.exe`);
}
