const { app, BrowserWindow } = require("electron");

const DASHBOARD_URL = process.env.TPROXY_URL || "http://127.0.0.1:28120/dashboard/";

function createWindow() {
  const window = new BrowserWindow({
    width: 1280,
    height: 860,
    backgroundColor: "#020503",
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
    },
  });
  window.loadURL(DASHBOARD_URL);
}

app.whenReady().then(() => {
  createWindow();
  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0) {
      createWindow();
    }
  });
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") {
    app.quit();
  }
});
