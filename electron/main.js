const { app, BrowserWindow, Tray, Menu, nativeImage, shell } = require("electron");
const path = require("node:path");
const { spawn } = require("node:child_process");
const http = require("node:http");
const fs = require("node:fs");
const os = require("node:os");

const DASHBOARD_URL = process.env.TPROXY_URL || "http://127.0.0.1:28120/dashboard/";
const PORT = Number(process.env.TPROXY_PORT || 28120);

let mainWindow = null;
let tray = null;
let gatewayChild = null;

function iconPath() {
  const candidates = [
    path.join(__dirname, "icon.png"),
    path.join(__dirname, "..", "npm", "lib", "tray", "icon.png"),
  ];
  for (const p of candidates) {
    if (fs.existsSync(p)) return p;
  }
  return null;
}

function createWindow() {
  if (mainWindow) {
    mainWindow.show();
    mainWindow.focus();
    return;
  }
  mainWindow = new BrowserWindow({
    width: 1280,
    height: 860,
    backgroundColor: "#020503",
    show: true,
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
    },
  });
  mainWindow.loadURL(DASHBOARD_URL);
  mainWindow.on("close", (event) => {
    // Hide to tray instead of quitting (desktop app behavior).
    if (!app.isQuitting) {
      event.preventDefault();
      mainWindow.hide();
    }
  });
  mainWindow.on("closed", () => {
    mainWindow = null;
  });
}

function isServerUp() {
  return new Promise((resolve) => {
    const req = http.get({ host: "127.0.0.1", port: PORT, path: "/dashboard/", timeout: 1200 }, (res) => {
      res.resume();
      resolve(true);
    });
    req.on("error", () => resolve(false));
    req.on("timeout", () => {
      req.destroy();
      resolve(false);
    });
  });
}

function resolveBinary() {
  const repoBin = path.join(__dirname, "..", "bin", "tproxy");
  if (fs.existsSync(repoBin)) return repoBin;
  return null;
}

async function ensureGateway() {
  if (await isServerUp()) return;
  const binary = resolveBinary();
  const logDir = path.join(os.homedir(), ".tproxy", "logs");
  fs.mkdirSync(logDir, { recursive: true });
  const out = fs.openSync(path.join(logDir, "electron-gateway.log"), "a");
  const err = fs.openSync(path.join(logDir, "electron-gateway.error.log"), "a");
  if (binary) {
    gatewayChild = spawn(binary, [], { stdio: ["ignore", out, err], windowsHide: true });
  } else {
    gatewayChild = spawn("go", ["run", "./cmd/tproxy"], {
      cwd: path.join(__dirname, ".."),
      stdio: ["ignore", out, err],
    });
  }
  for (let i = 0; i < 40; i++) {
    if (await isServerUp()) return;
    await new Promise((r) => setTimeout(r, 500));
  }
}

function buildTrayMenu() {
  const openAtLogin = app.getLoginItemSettings().openAtLogin;
  return Menu.buildFromTemplate([
    { label: `TProxy (Port ${PORT})`, enabled: false },
    {
      label: "Open Dashboard",
      click: () => {
        createWindow();
      },
    },
    {
      label: "Open in Browser",
      click: () => shell.openExternal(DASHBOARD_URL),
    },
    { type: "separator" },
    {
      label: openAtLogin ? "✓ Start at Login" : "Start at Login",
      click: () => {
        const next = !app.getLoginItemSettings().openAtLogin;
        app.setLoginItemSettings({ openAtLogin: next, openAsHidden: true });
        tray.setContextMenu(buildTrayMenu());
      },
    },
    { type: "separator" },
    {
      label: "Quit",
      click: () => {
        app.isQuitting = true;
        if (gatewayChild && !gatewayChild.killed) {
          try {
            gatewayChild.kill("SIGTERM");
          } catch {
            /* ignore */
          }
        }
        app.quit();
      },
    },
  ]);
}

function createTray() {
  const p = iconPath();
  const image = p ? nativeImage.createFromPath(p) : nativeImage.createEmpty();
  tray = new Tray(image.isEmpty() ? nativeImage.createFromDataURL(
    "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAYAAAAf8/9hAAAAKElEQVQ4T2NkYGD4z0ABYBzVMKoBBgP+/2dgYBjVQBqYRjVQHXgDALxyB/3a2mFJAAAAAElFTkSuQmCC",
  ) : image);
  tray.setToolTip(`TProxy - Port ${PORT}`);
  tray.setContextMenu(buildTrayMenu());
  tray.on("click", () => createWindow());
  tray.on("double-click", () => createWindow());
}

app.whenReady().then(async () => {
  if (process.platform === "darwin") {
    app.dock.hide();
  }
  await ensureGateway();
  createTray();
  // Don't force a window on autostart; open when user clicks tray.
  if (!app.getLoginItemSettings().wasOpenedAtLogin) {
    createWindow();
  }
  app.on("activate", () => createWindow());
});

app.on("window-all-closed", (e) => {
  // Keep running in tray.
  e.preventDefault?.();
});

app.on("before-quit", () => {
  app.isQuitting = true;
  if (gatewayChild && !gatewayChild.killed) {
    try {
      gatewayChild.kill("SIGTERM");
    } catch {
      /* ignore */
    }
  }
});
