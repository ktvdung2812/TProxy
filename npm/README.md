# @ktvdung1606/tproxy

npm CLI wrapper for the [tproxy](https://github.com/ktvdung2812/TProxy) AI gateway.

## Install

```bash
npm install -g @ktvdung1606/tproxy
```

The package includes native gateway binaries for macOS (Apple Silicon and Intel),
Linux (x64 and ARM64), and Windows (x64 and ARM64). Node.js 18 or newer is required.

## First login

On the first gateway start, the dashboard password is initialized to `123123`.
Change it immediately in **Settings**. This default is used only when no dashboard
password has been saved and `TPROXY_MANAGEMENT_SECRET` is not set.

## Automated releases

Pushing a version tag such as `v0.1.14` runs the GitHub Actions release workflow.
It verifies that the tag matches `package.json`, cross-compiles the bundled
binaries, and publishes to npm. Add an npm granular token with publish access as
the repository Actions secret named `NPM_TOKEN` before creating the tag.

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

- The launcher runs its bundled binary, so it does not depend on a separately
  installed `tproxy` executable or Go toolchain.
- To override the bundled binary for development, set `TPROXY_BINARY` to an
  absolute path you trust.
- First tray run on macOS/Linux may install `systray2` into `~/.tproxy/runtime/` (one-time).
- Windows tray uses PowerShell `NotifyIcon` (no native binary).
- Default config is created at `~/.tproxy/config.yaml` if missing.
