// Lazy-install systray2 into ~/.tproxy/runtime/node_modules (macOS/Linux).
// Windows uses PowerShell NotifyIcon — no native binary.
const { spawnSync } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");
const os = require("node:os");

const SYSTRAY_PKG = "systray2";
const SYSTRAY_VERSION = "2.1.4";

function getRuntimeDir() {
  return path.join(os.homedir(), ".tproxy", "runtime");
}

function getRuntimeNodeModules() {
  return path.join(getRuntimeDir(), "node_modules");
}

function ensureRuntimeDir() {
  const dir = getRuntimeDir();
  fs.mkdirSync(dir, { recursive: true });
  const pkgPath = path.join(dir, "package.json");
  if (!fs.existsSync(pkgPath)) {
    fs.writeFileSync(
      pkgPath,
      JSON.stringify({ name: "tproxy-runtime", version: "1.0.0", private: true }, null, 2),
    );
  }
  return dir;
}

function hasSystray() {
  return fs.existsSync(path.join(getRuntimeNodeModules(), SYSTRAY_PKG, "package.json"));
}

function chmodSystrayBin() {
  if (process.platform === "win32") return;
  const binName = process.platform === "darwin" ? "tray_darwin_release" : "tray_linux_release";
  const binPath = path.join(getRuntimeNodeModules(), SYSTRAY_PKG, "traybin", binName);
  if (!fs.existsSync(binPath)) return;
  try {
    fs.chmodSync(binPath, 0o755);
  } catch {
    /* ignore */
  }
}

function npmInstall(pkgs, { silent = false } = {}) {
  const cwd = ensureRuntimeDir();
  if (!silent) console.log("⏳ Installing system tray runtime (first run)...");
  const result = spawnSync(
    process.platform === "win32" ? "npm.cmd" : "npm",
    ["install", "--no-save", "--no-fund", "--no-audit", ...pkgs],
    { cwd, encoding: "utf8", timeout: 120000, env: process.env },
  );
  if (result.status !== 0 && !silent) {
    console.warn("⚠️  System tray install failed — tray icon may be unavailable");
    if (result.stderr) console.warn(String(result.stderr).slice(0, 400));
    console.warn(`   Retry: cd "${cwd}" && npm install ${pkgs.join(" ")}`);
  }
  return result.status === 0;
}

function ensureTrayRuntime({ silent = false } = {}) {
  if (process.platform === "win32") {
    return { systray: false, skipped: true };
  }
  if (hasSystray()) {
    chmodSystrayBin();
    return { systray: true };
  }
  const ok = npmInstall([`${SYSTRAY_PKG}@${SYSTRAY_VERSION}`], { silent });
  if (ok) chmodSystrayBin();
  return { systray: ok };
}

module.exports = {
  ensureTrayRuntime,
  getRuntimeDir,
  getRuntimeNodeModules,
};
