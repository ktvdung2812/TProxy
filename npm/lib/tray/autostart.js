const fs = require("node:fs");
const path = require("node:path");
const os = require("node:os");
const { execSync } = require("node:child_process");

const APP_NAME = "tproxy";
const APP_LABEL = "com.tproxy.autostart";

/**
 * Resolve absolute path to the tproxy.js launcher.
 */
function getLauncherPath(cliPath) {
  if (cliPath) {
    const resolved = path.resolve(cliPath);
    if (fs.existsSync(resolved)) return resolved;
  }
  if (process.argv[1]) {
    const resolved = path.resolve(process.argv[1]);
    if (fs.existsSync(resolved) && path.basename(resolved).includes("tproxy")) {
      return resolved;
    }
  }
  const computed = path.resolve(__dirname, "..", "..", "bin", "tproxy.js");
  if (fs.existsSync(computed)) return computed;
  return null;
}

function enableAutoStart(cliPath) {
  const platform = process.platform;
  if (!["darwin", "win32", "linux"].includes(platform)) return false;
  if (platform === "linux" && !process.env.DISPLAY && !process.env.WAYLAND_DISPLAY) return false;
  try {
    if (platform === "darwin") return enableMacOS(cliPath);
    if (platform === "win32") return enableWindows(cliPath);
    if (platform === "linux") return enableLinux(cliPath);
  } catch {
    /* optional feature */
  }
  return false;
}

function disableAutoStart() {
  const platform = process.platform;
  try {
    if (platform === "darwin") return disableMacOS();
    if (platform === "win32") return disableWindows();
    if (platform === "linux") return disableLinux();
  } catch {
    /* ignore */
  }
  return false;
}

function isAutoStartEnabled() {
  const platform = process.platform;
  try {
    if (platform === "darwin") {
      const plistPath = path.join(os.homedir(), "Library", "LaunchAgents", `${APP_LABEL}.plist`);
      if (!fs.existsSync(plistPath)) return false;
      try {
        execSync(`launchctl list ${APP_LABEL}`, {
          stdio: ["ignore", "ignore", "ignore"],
          timeout: 3000,
        });
        return true;
      } catch {
        return false;
      }
    }
    if (platform === "win32") {
      const startupPath = path.join(
        process.env.APPDATA || "",
        "Microsoft",
        "Windows",
        "Start Menu",
        "Programs",
        "Startup",
        `${APP_NAME}.vbs`,
      );
      return fs.existsSync(startupPath);
    }
    if (platform === "linux") {
      const desktopPath = path.join(os.homedir(), ".config", "autostart", `${APP_NAME}.desktop`);
      return fs.existsSync(desktopPath);
    }
  } catch {
    /* ignore */
  }
  return false;
}

function isAgentSelfMacOS() {
  try {
    const output = execSync(`launchctl list ${APP_LABEL}`, {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"],
      timeout: 3000,
    });
    const match = output.match(/"PID"\s*=\s*(\d+)/);
    return !!(match && parseInt(match[1], 10) === process.pid);
  } catch {
    return false;
  }
}

function enableMacOS(cliPath) {
  const launchAgentsDir = path.join(os.homedir(), "Library", "LaunchAgents");
  const plistPath = path.join(launchAgentsDir, `${APP_LABEL}.plist`);
  fs.mkdirSync(launchAgentsDir, { recursive: true });

  const nodePath = process.execPath;
  const launcher = getLauncherPath(cliPath);
  if (!launcher) return false;

  const launchPath = `${path.dirname(nodePath)}:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin`;
  const logDir = path.join(os.homedir(), ".tproxy", "logs");
  fs.mkdirSync(logDir, { recursive: true });

  const plistContent = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>${APP_LABEL}</string>
    <key>ProgramArguments</key>
    <array>
        <string>${nodePath}</string>
        <string>${launcher}</string>
        <string>--tray</string>
    </array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>${launchPath}</string>
    </dict>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <false/>
    <key>StandardOutPath</key>
    <string>${path.join(logDir, "autostart.log")}</string>
    <key>StandardErrorPath</key>
    <string>${path.join(logDir, "autostart.error.log")}</string>
</dict>
</plist>`;

  fs.writeFileSync(plistPath, plistContent);

  if (isAgentSelfMacOS()) return true;

  try {
    execSync(`launchctl unload "${plistPath}"`, { stdio: "ignore" });
  } catch {
    /* not loaded */
  }
  try {
    execSync(`launchctl load -w "${plistPath}"`, { stdio: "ignore" });
  } catch {
    /* file still present for next login */
  }
  return true;
}

function disableMacOS() {
  const plistPath = path.join(os.homedir(), "Library", "LaunchAgents", `${APP_LABEL}.plist`);
  if (!isAgentSelfMacOS()) {
    try {
      execSync(`launchctl unload "${plistPath}"`, { stdio: "ignore" });
    } catch {
      /* ignore */
    }
  }
  if (fs.existsSync(plistPath)) fs.unlinkSync(plistPath);
  return true;
}

function enableWindows(cliPath) {
  const startupDir = path.join(
    process.env.APPDATA || "",
    "Microsoft",
    "Windows",
    "Start Menu",
    "Programs",
    "Startup",
  );
  if (!fs.existsSync(startupDir)) return false;
  const vbsPath = path.join(startupDir, `${APP_NAME}.vbs`);
  const nodePath = process.execPath;
  const launcher = getLauncherPath(cliPath);
  if (!launcher) return false;
  const vbsContent = `Set WshShell = CreateObject("WScript.Shell")
WshShell.Run """${nodePath}"" ""${launcher}"" --tray", 0, False
`;
  fs.writeFileSync(vbsPath, vbsContent);
  return true;
}

function disableWindows() {
  const vbsPath = path.join(
    process.env.APPDATA || "",
    "Microsoft",
    "Windows",
    "Start Menu",
    "Programs",
    "Startup",
    `${APP_NAME}.vbs`,
  );
  if (fs.existsSync(vbsPath)) fs.unlinkSync(vbsPath);
  return true;
}

function enableLinux(cliPath) {
  const autostartDir = path.join(os.homedir(), ".config", "autostart");
  fs.mkdirSync(autostartDir, { recursive: true });
  const desktopPath = path.join(autostartDir, `${APP_NAME}.desktop`);
  const nodePath = process.execPath;
  const launcher = getLauncherPath(cliPath);
  if (!launcher) return false;
  const desktopContent = `[Desktop Entry]
Type=Application
Name=TProxy
Comment=TProxy AI Gateway
Exec=${nodePath} ${launcher} --tray
Hidden=false
NoDisplay=false
X-GNOME-Autostart-enabled=true
`;
  fs.writeFileSync(desktopPath, desktopContent);
  return true;
}

function disableLinux() {
  const desktopPath = path.join(os.homedir(), ".config", "autostart", `${APP_NAME}.desktop`);
  if (fs.existsSync(desktopPath)) fs.unlinkSync(desktopPath);
  return true;
}

module.exports = {
  enableAutoStart,
  disableAutoStart,
  isAutoStartEnabled,
  getLauncherPath,
};
