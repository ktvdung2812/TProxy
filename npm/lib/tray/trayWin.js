const { spawn } = require("node:child_process");
const path = require("node:path");
const readline = require("node:readline");

let psProcess = null;
let clickHandler = null;

function sendCommand(cmd) {
  if (psProcess && psProcess.stdin.writable) {
    psProcess.stdin.write(`${JSON.stringify(cmd)}\n`, "utf8");
  }
}

/**
 * Windows tray via PowerShell NotifyIcon (no native binary).
 */
function initWinTray(options) {
  const { iconPath, tooltip, items, onClick } = options;
  clickHandler = onClick;
  const scriptPath = path.join(__dirname, "tray.ps1");

  try {
    psProcess = spawn(
      "powershell.exe",
      [
        "-NoProfile",
        "-ExecutionPolicy",
        "Bypass",
        "-WindowStyle",
        "Hidden",
        "-InputFormat",
        "Text",
        "-OutputFormat",
        "Text",
        "-File",
        scriptPath,
        "-IconPath",
        iconPath,
        "-Tooltip",
        tooltip,
      ],
      { windowsHide: true, stdio: ["pipe", "pipe", "pipe"] },
    );
  } catch {
    return null;
  }

  const rl = readline.createInterface({ input: psProcess.stdout });
  rl.on("line", (line) => {
    try {
      const evt = JSON.parse(line);
      if (evt.type === "click" && clickHandler) clickHandler(evt.index);
    } catch {
      /* ignore parse noise */
    }
  });

  psProcess.on("error", () => {});
  psProcess.stderr.on("data", () => {});

  items.forEach((item, index) => {
    sendCommand({ action: "add-item", index, title: item.title, enabled: item.enabled });
  });

  return {
    updateItem(index, title, enabled) {
      sendCommand({ action: "update-item", index, title, enabled });
    },
    setTooltip(text) {
      sendCommand({ action: "set-tooltip", text });
    },
    kill() {
      try {
        sendCommand({ action: "kill" });
      } catch {
        /* ignore */
      }
      setTimeout(() => {
        if (psProcess && !psProcess.killed) {
          try {
            psProcess.kill();
          } catch {
            /* ignore */
          }
        }
        psProcess = null;
      }, 300);
    },
  };
}

module.exports = { initWinTray };
