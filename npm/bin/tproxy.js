#!/usr/bin/env node
const { spawn } = require("node:child_process");
const path = require("node:path");
const fs = require("node:fs");
const os = require("node:os");
const net = require("node:net");
const http = require("node:http");

const packageRoot = path.resolve(__dirname, "..");
const repoRoot = path.resolve(packageRoot, "..");
const defaultConfigTemplate = path.join(packageRoot, "config.default.yaml");
const userConfigDir = path.join(os.homedir(), ".tproxy");
const userConfigPath = path.join(userConfigDir, "config.yaml");

function ensureUserConfig() {
  if (fs.existsSync(userConfigPath)) return userConfigPath;
  fs.mkdirSync(userConfigDir, { recursive: true });
  if (fs.existsSync(defaultConfigTemplate)) {
    fs.copyFileSync(defaultConfigTemplate, userConfigPath);
    return userConfigPath;
  }
  const fallback = [
    "server:",
    "  host: 127.0.0.1",
    "  port: 28120",
    "database:",
    "  driver: sqlite",
    "  dsn: tproxy.db",
    "proxy-pools: []",
    "teams: []",
    "client-api-keys: []",
    "providers: []",
    "models: []",
    "combos: []",
    "",
  ].join("\n");
  fs.writeFileSync(userConfigPath, fallback, "utf8");
  return userConfigPath;
}

function withDefaultConfig(args) {
  const hasConfigFlag = args.some(
    (arg, index) =>
      arg === "--config" ||
      arg.startsWith("--config=") ||
      (arg === "-config" && args[index + 1]),
  );
  if (hasConfigFlag) return args;
  return ["--config", ensureUserConfig(), ...args];
}

function resolveBinary() {
  const candidates = [
    path.join(repoRoot, "bin", "tproxy"),
    path.join(packageRoot, "bin", "tproxy"),
    path.join(packageRoot, "bin", "tproxy.exe"),
    path.join(repoRoot, "bin", "tproxy.exe"),
  ];
  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) return candidate;
  }
  // PATH lookup
  const pathEnv = process.env.PATH || "";
  for (const dir of pathEnv.split(path.delimiter)) {
    const candidate = path.join(dir, process.platform === "win32" ? "tproxy.exe" : "tproxy");
    if (fs.existsSync(candidate)) return candidate;
  }
  return null;
}

function readPortFromConfig(configPath) {
  try {
    const raw = fs.readFileSync(configPath, "utf8");
    const match = raw.match(/^\s*port:\s*(\d+)\s*$/m);
    if (match) return Number(match[1]);
  } catch {
    /* ignore */
  }
  return 28120;
}

function waitServerReady(port, { timeoutMs = 20000, intervalMs = 200 } = {}) {
  const deadline = Date.now() + timeoutMs;
  return new Promise((resolve) => {
    const tryConnect = () => {
      const socket = net.connect({ host: "127.0.0.1", port }, () => {
        socket.destroy();
        resolve(true);
      });
      socket.on("error", () => {
        socket.destroy();
        if (Date.now() >= deadline) return resolve(false);
        setTimeout(tryConnect, intervalMs);
      });
    };
    tryConnect();
  });
}

function isServerUp(port) {
  return new Promise((resolve) => {
    const req = http.get({ host: "127.0.0.1", port, path: "/dashboard/", timeout: 1500 }, (res) => {
      res.resume();
      resolve(res.statusCode > 0 && res.statusCode < 500);
    });
    req.on("error", () => resolve(false));
    req.on("timeout", () => {
      req.destroy();
      resolve(false);
    });
  });
}

function parseLauncherArgs(argv) {
  const result = {
    tray: false,
    autostartCmd: null,
    passthrough: [],
  };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === "--tray" || arg === "-t") {
      result.tray = true;
      continue;
    }
    if (arg === "tray") {
      result.tray = true;
      continue;
    }
    if (arg === "autostart") {
      result.autostartCmd = argv[i + 1] || "status";
      i += 1;
      continue;
    }
    if (arg === "--help" || arg === "-h") {
      result.help = true;
      continue;
    }
    result.passthrough.push(arg);
  }
  return result;
}

function printHelp() {
  console.log(`tproxy — AI gateway launcher

Usage:
  tproxy [flags]                 Start the gateway (foreground)
  tproxy --tray | -t | tray      Start gateway + system tray icon
  tproxy autostart enable        Enable start at login (with tray)
  tproxy autostart disable       Disable start at login
  tproxy autostart status        Show autostart state

Tray menu:
  • Open Dashboard
  • Enable / Disable Auto-start
  • Quit

Examples:
  tproxy --config ~/.tproxy/config.yaml
  tproxy --tray
  tproxy autostart enable
`);
}

async function runAutostart(cmd) {
  const { enableAutoStart, disableAutoStart, isAutoStartEnabled } = require("../lib/tray/autostart");
  const launcher = path.resolve(__filename);
  if (cmd === "enable") {
    const ok = enableAutoStart(launcher);
    console.log(ok ? "✅ Auto-start enabled (TProxy will start with the system in tray mode)." : "❌ Failed to enable auto-start.");
    process.exit(ok ? 0 : 1);
  }
  if (cmd === "disable") {
    const ok = disableAutoStart();
    console.log(ok ? "✅ Auto-start disabled." : "❌ Failed to disable auto-start.");
    process.exit(ok ? 0 : 1);
  }
  // status
  console.log(isAutoStartEnabled() ? "Auto-start: enabled" : "Auto-start: disabled");
  process.exit(0);
}

function spawnGateway(binary, args, { detached = false } = {}) {
  if (binary) {
    return spawn(binary, args, {
      stdio: detached ? "ignore" : "inherit",
      env: process.env,
      detached,
      windowsHide: true,
    });
  }
  // Dev fallback: go run from repo
  return spawn("go", ["run", "./cmd/tproxy", ...args], {
    cwd: repoRoot,
    stdio: detached ? "ignore" : "inherit",
    env: process.env,
    detached,
  });
}

function spawnGatewayForTray(binary, args) {
  const logDir = path.join(userConfigDir, "logs");
  fs.mkdirSync(logDir, { recursive: true });
  const outPath = path.join(logDir, "gateway.log");
  const errPath = path.join(logDir, "gateway.error.log");
  const outFd = fs.openSync(outPath, "a");
  const errFd = fs.openSync(errPath, "a");
  const env = { ...process.env };
  if (binary) {
    return spawn(binary, args, {
      stdio: ["ignore", outFd, errFd],
      env,
      windowsHide: true,
    });
  }
  return spawn("go", ["run", "./cmd/tproxy", ...args], {
    cwd: repoRoot,
    stdio: ["ignore", outFd, errFd],
    env,
  });
}

async function runTrayMode(rawArgs) {
  const { ensureTrayRuntime } = require("../lib/trayRuntime");
  ensureTrayRuntime({ silent: false });

  const args = withDefaultConfig(rawArgs.filter((a) => a !== "--tray" && a !== "-t" && a !== "tray"));
  let configPath = userConfigPath;
  for (let i = 0; i < args.length; i++) {
    if (args[i] === "--config" && args[i + 1]) configPath = args[i + 1];
    if (args[i].startsWith("--config=")) configPath = args[i].slice("--config=".length);
  }
  if (!fs.existsSync(configPath)) configPath = ensureUserConfig();
  const port = readPortFromConfig(configPath);
  const binary = resolveBinary();

  let child = null;
  const alreadyUp = await isServerUp(port);
  if (!alreadyUp) {
    child = spawnGatewayForTray(binary, args);
    child.on("error", (err) => {
      console.error(`❌ Failed to start tproxy: ${err.message}`);
      if (!binary) {
        console.error("   Build the binary with: (cd tproxy && go build -o bin/tproxy ./cmd/tproxy)");
      }
      process.exit(1);
    });
    const ready = await waitServerReady(port, { timeoutMs: 45000 });
    if (!ready) {
      console.error(`❌ TProxy did not become ready on port ${port}`);
      console.error(`   Check logs under ${path.join(userConfigDir, "logs")}`);
      if (child && !child.killed) child.kill();
      process.exit(1);
    }
  }

  // Survive terminal close / launchd session (tray owns the process).
  process.on("SIGHUP", () => {});

  const { initTray, killTray, openBrowser, isTraySupported } = require("../lib/tray/tray");
  if (!isTraySupported()) {
    console.warn("⚠️  System tray is not supported in this environment. Gateway is still running.");
    if (child) {
      child.on("exit", (code) => process.exit(code ?? 0));
    } else {
      // Attach forever so autostart process doesn't exit immediately.
      setInterval(() => {}, 60_000);
    }
    return;
  }

  const stopChild = () => {
    if (child && !child.killed) {
      try {
        child.kill("SIGTERM");
      } catch {
        /* ignore */
      }
    }
  };

  const tray = initTray({
    port,
    cliPath: path.resolve(__filename),
    onOpenDashboard: () => openBrowser(`http://127.0.0.1:${port}/dashboard/`),
    onQuit: () => {
      stopChild();
    },
  });

  if (!tray) {
    console.warn("⚠️  Could not create tray icon. Gateway is still running in the background of this process.");
  } else {
    console.log(`✅ TProxy running in system tray (port ${port})`);
    console.log("   Right-click the tray icon → Open Dashboard / Auto-start / Quit");
    if (process.stdout.isTTY) {
      console.log("   You can close this terminal; the tray keeps the gateway alive.\n");
    }
  }

  const shutdown = async (signal) => {
    process.stderr.write(`\n[tproxy] received ${signal}, shutting down...\n`);
    stopChild();
    await killTray();
    process.exit(0);
  };
  process.on("SIGINT", () => void shutdown("SIGINT"));
  process.on("SIGTERM", () => void shutdown("SIGTERM"));

  if (child) {
    child.on("exit", async (code) => {
      await killTray();
      process.exit(code ?? 0);
    });
  } else {
    // Server was already running: keep launcher alive for the tray only.
    setInterval(() => {}, 60_000);
  }
}

async function main() {
  const parsed = parseLauncherArgs(process.argv.slice(2));
  if (parsed.help) {
    printHelp();
    process.exit(0);
  }
  if (parsed.autostartCmd) {
    await runAutostart(parsed.autostartCmd);
    return;
  }
  if (parsed.tray) {
    await runTrayMode(parsed.passthrough);
    return;
  }

  // Default: passthrough to Go binary (foreground)
  const args = withDefaultConfig(parsed.passthrough);
  const binary = resolveBinary();
  const child = spawnGateway(binary, args, { detached: false });
  child.on("error", (err) => {
    console.error(`❌ Failed to start tproxy: ${err.message}`);
    if (!binary) {
      console.error("   Build the binary with: (cd tproxy && go build -o bin/tproxy ./cmd/tproxy)");
    }
    process.exit(1);
  });
  child.on("exit", (code) => process.exit(code ?? 0));
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
