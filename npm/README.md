# @ktvdung1606/tproxy

npm CLI wrapper for the [tproxy](https://github.com/ktvdung2812/TProxy) AI gateway.

## Install

```bash
npm install -g @ktvdung1606/tproxy
```

Place a built `tproxy` binary on your `PATH`, or run from a local clone:

```bash
cd tproxy && go build -o bin/tproxy ./cmd/tproxy
```

## Usage

```bash
# Foreground gateway
tproxy --config ~/.tproxy/config.yaml

# System tray (menu bar / notification area) — like 9Router
tproxy --tray
# or
tproxy tray
```

### Tray menu

| Item | Action |
|------|--------|
| **TProxy (Port …)** | Status (read-only) |
| **Open Dashboard** | Opens `http://127.0.0.1:<port>/dashboard/` |
| **Enable Auto-start** / **✓ Auto-start Enabled** | Toggle launch at login |
| **Quit** | Stop gateway and exit |

### Auto-start at login

```bash
tproxy autostart enable    # LaunchAgent / Startup / XDG autostart
tproxy autostart disable
tproxy autostart status
```

You can also toggle auto-start from the tray menu. On enable, the OS starts:

```text
node <path-to-tproxy.js> --tray
```

- **macOS:** `~/Library/LaunchAgents/com.tproxy.autostart.plist`
- **Windows:** Startup folder `tproxy.vbs`
- **Linux:** `~/.config/autostart/tproxy.desktop`

Logs (macOS autostart): `~/.tproxy/logs/`.

## Notes

- First tray run on macOS/Linux may install `systray2` into `~/.tproxy/runtime/` (one-time).
- Windows tray uses PowerShell `NotifyIcon` (no native binary).
- Default config is created at `~/.tproxy/config.yaml` if missing.
