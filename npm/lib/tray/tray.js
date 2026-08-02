const { exec } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");

let trayInstance = null;
let isWinTray = false;

function getIconBase64() {
  // Prefer project brand mark (hub on green gradient). On Retina macOS,
  // 128px embeds cleaner than 32px when systray downscales.
  const candidates = process.platform === "win32"
    ? ["icon.ico", "icon-64.png", "icon.png"]
    : ["icon-128.png", "icon-64.png", "icon.png", "icon-32.png"];
  for (const iconFile of candidates) {
    try {
      const iconPath = path.join(__dirname, iconFile);
      if (fs.existsSync(iconPath)) {
        return fs.readFileSync(iconPath).toString("base64");
      }
    } catch {
      /* try next */
    }
  }
  // Last-resort 1×1 brand green pixel (should not hit if assets ship).
  return "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";
}

function isTraySupported() {
  const platform = process.platform;
  if (!["darwin", "win32", "linux"].includes(platform)) return false;
  if (platform === "linux" && !process.env.DISPLAY && !process.env.WAYLAND_DISPLAY) return false;
  return true;
}

function buildMenuItems(port, autostartEnabled) {
  return [
    { title: `TProxy (Port ${port})`, tooltip: "Server is running", enabled: false },
    { title: "Open Dashboard", tooltip: "Open in browser", enabled: true },
    {
      title: autostartEnabled ? "✓ Auto-start Enabled" : "Enable Auto-start",
      tooltip: "Run on OS startup",
      enabled: true,
    },
    { title: "Quit", tooltip: "Stop server and exit", enabled: true },
  ];
}

const MENU_INDEX = { STATUS: 0, DASHBOARD: 1, AUTOSTART: 2, QUIT: 3 };

function getAutostartEnabled() {
  try {
    const { isAutoStartEnabled } = require("./autostart");
    return isAutoStartEnabled();
  } catch {
    return false;
  }
}

function handleClick(index, options, onAutostartToggle) {
  const { onQuit, onOpenDashboard, port } = options;
  if (index === MENU_INDEX.DASHBOARD) {
    if (onOpenDashboard) onOpenDashboard();
    else openBrowser(`http://127.0.0.1:${port}/dashboard/`);
  } else if (index === MENU_INDEX.AUTOSTART) {
    const enabled = getAutostartEnabled();
    try {
      const { enableAutoStart, disableAutoStart } = require("./autostart");
      if (enabled) disableAutoStart();
      else enableAutoStart(options.cliPath);
      onAutostartToggle(!enabled);
    } catch {
      /* ignore */
    }
  } else if (index === MENU_INDEX.QUIT) {
    process.stderr.write("\n👋 Shutting down TProxy...\n");
    if (onQuit) onQuit();
    killTray().finally(() => {
      setTimeout(() => process.exit(0), 400);
    });
  }
}

function initTray(options) {
  if (!isTraySupported()) return null;
  if (process.platform === "win32") return initWindowsTray(options);
  return initUnixTray(options);
}

function initWindowsTray(options) {
  const { port } = options;
  try {
    const { initWinTray } = require("./trayWin");
    const iconPath = path.join(__dirname, "icon.ico");
    const items = buildMenuItems(port, getAutostartEnabled());
    trayInstance = initWinTray({
      iconPath,
      tooltip: `TProxy - Port ${port}`,
      items,
      onClick: (index) => {
        handleClick(index, options, (newEnabled) => {
          const newTitle = newEnabled ? "✓ Auto-start Enabled" : "Enable Auto-start";
          trayInstance.updateItem(MENU_INDEX.AUTOSTART, newTitle, true);
        });
      },
    });
    isWinTray = true;
    return trayInstance;
  } catch {
    return null;
  }
}

function resolveSystray() {
  let runtimeDir = null;
  try {
    const { getRuntimeNodeModules } = require("../trayRuntime");
    runtimeDir = getRuntimeNodeModules();
  } catch {
    /* ignore */
  }
  if (runtimeDir) {
    try {
      return { mod: require(path.join(runtimeDir, "systray2")).default, isV2: true };
    } catch {
      /* try next */
    }
  }
  try {
    return { mod: require("systray2").default, isV2: true };
  } catch {
    /* try legacy */
  }
  try {
    return { mod: require("systray").default, isV2: false };
  } catch {
    return null;
  }
}

function chmodTrayBin(pkgName) {
  try {
    const { getRuntimeNodeModules } = require("../trayRuntime");
    const binName = process.platform === "darwin" ? "tray_darwin_release" : "tray_linux_release";
    const candidates = [
      path.join(getRuntimeNodeModules(), pkgName, "traybin", binName),
      path.join(__dirname, "..", "..", "node_modules", pkgName, "traybin", binName),
    ];
    for (const p of candidates) {
      if (fs.existsSync(p)) fs.chmodSync(p, 0o755);
    }
  } catch {
    /* ignore */
  }
}

function initUnixTray(options) {
  const { port } = options;
  try {
    const resolved = resolveSystray();
    if (!resolved) return null;
    const { mod: SysTray, isV2 } = resolved;
    chmodTrayBin(isV2 ? "systray2" : "systray");

    const items = buildMenuItems(port, getAutostartEnabled());
    const menu = {
      icon: getIconBase64(),
      isTemplateIcon: false,
      title: "",
      tooltip: `TProxy - Port ${port}`,
      items,
    };

    trayInstance = new SysTray({ menu, debug: false, copyDir: true });
    isWinTray = false;

    trayInstance.onClick((action) => {
      handleClick(action.seq_id, options, (newEnabled) => {
        trayInstance.sendAction({
          type: "update-item",
          item: {
            title: newEnabled ? "✓ Auto-start Enabled" : "Enable Auto-start",
            tooltip: "Run on OS startup",
            enabled: true,
          },
          seq_id: MENU_INDEX.AUTOSTART,
        });
      });
    });

    if (isV2 && typeof trayInstance.ready === "function") {
      trayInstance.ready().catch((err) => {
        process.stderr.write(`[tproxy] tray failed to start: ${err && err.message ? err.message : err}\n`);
      });
    }

    return trayInstance;
  } catch (err) {
    process.stderr.write(`[tproxy] tray init error: ${err.message}\n`);
    return null;
  }
}

function killTray() {
  const instance = trayInstance;
  const wasWin = isWinTray;
  trayInstance = null;
  if (!instance) return Promise.resolve();

  if (wasWin) {
    try {
      instance.kill();
    } catch {
      /* ignore */
    }
    return Promise.resolve();
  }

  let proc = null;
  try {
    proc = instance._process || (typeof instance.process === "function" ? instance.process() : null);
  } catch {
    /* ignore */
  }

  const gracefulQuit = () => {
    try {
      instance.kill(true);
    } catch {
      /* ignore */
    }
  };
  const closeIpc = () => {
    try {
      instance.kill(false);
    } catch {
      /* ignore */
    }
  };

  if (!proc || !proc.pid) {
    gracefulQuit();
    closeIpc();
    return Promise.resolve();
  }

  return new Promise((resolve) => {
    let done = false;
    const finish = () => {
      if (done) return;
      done = true;
      closeIpc();
      resolve();
    };
    proc.once("exit", finish);
    gracefulQuit();
    setTimeout(() => {
      try {
        process.kill(proc.pid, 0);
        proc.kill("SIGTERM");
      } catch {
        /* already dead */
      }
    }, 800);
    setTimeout(() => {
      try {
        process.kill(proc.pid, 0);
        proc.kill("SIGKILL");
      } catch {
        /* already dead */
      }
    }, 1600);
    const deadline = Date.now() + 3000;
    const poll = setInterval(() => {
      try {
        process.kill(proc.pid, 0);
      } catch {
        clearInterval(poll);
        finish();
        return;
      }
      if (Date.now() > deadline) {
        clearInterval(poll);
        finish();
      }
    }, 50);
  });
}

function openBrowser(url) {
  const platform = process.platform;
  let cmd;
  if (platform === "darwin") cmd = `open "${url}"`;
  else if (platform === "win32") cmd = `start "" "${url}"`;
  else cmd = `xdg-open "${url}"`;
  exec(cmd);
}

module.exports = {
  initTray,
  killTray,
  isTraySupported,
  openBrowser,
};
