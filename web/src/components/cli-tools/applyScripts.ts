import type { ManualConfigEntry } from "./ManualConfigModal";

export type ApplyScriptShell = "bash" | "powershell";

function bashPath(filename: string): string {
  if (filename.startsWith("~/")) {
    return `"$HOME/${filename.slice(2)}"`;
  }
  if (filename.startsWith("~")) {
    return `"$HOME${filename.slice(1)}"`;
  }
  return `"${filename}"`;
}

function powershellPath(filename: string): string {
  if (filename.startsWith("~/") || filename.startsWith("~\\")) {
    return `"$HOME/${filename.slice(2).replace(/\\/g, "/")}"`;
  }
  if (filename.startsWith("~")) {
    return `"$HOME${filename.slice(1).replace(/\\/g, "/")}"`;
  }
  if (filename.includes("%USERPROFILE%")) {
    const rest = filename.replace(/^%USERPROFILE%[\\/]?/i, "").replace(/\\/g, "/");
    return `"$env:USERPROFILE/${rest}"`;
  }
  return `"${filename.replace(/\\/g, "/")}"`;
}

function bashSupportsPath(filename: string): boolean {
  return !filename.includes("%USERPROFILE%");
}

export function buildApplyScript(shell: ApplyScriptShell, configs: ManualConfigEntry[]): string | null {
  if (configs.length === 0) return null;
  if (shell === "bash") {
    if (!configs.every((config) => bashSupportsPath(config.filename))) {
      return null;
    }
    return buildBashApplyScript(configs);
  }
  return buildPowerShellApplyScript(configs);
}

function buildBashApplyScript(configs: ManualConfigEntry[]): string {
  const body: string[] = [
    "set -euo pipefail",
    "",
    "# Apply tproxy CLI tool configuration (same files as dashboard Apply to local config)",
  ];
  for (const config of configs) {
    const path = bashPath(config.filename);
    body.push("");
    body.push(`mkdir -p "$(dirname ${path})"`);
    body.push(`cat > ${path} <<'TPROXY_EOF'`);
    body.push(config.content);
    body.push("TPROXY_EOF");
  }
  body.push("");
  body.push('echo "tproxy configuration applied."');

  return ["bash <<'TPROXY_APPLY_EOF'", ...body, "TPROXY_APPLY_EOF"].join("\n");
}

function buildPowerShellApplyScript(configs: ManualConfigEntry[]): string {
  const lines = [
    "$ErrorActionPreference = 'Stop'",
    "",
    "# Apply tproxy CLI tool configuration (same files as dashboard Apply to local config)",
  ];
  for (const config of configs) {
    const path = powershellPath(config.filename);
    lines.push("");
    lines.push(`$target = ${path}`);
    lines.push('New-Item -ItemType Directory -Force -Path (Split-Path -Parent $target) | Out-Null');
    lines.push("@'");
    lines.push(config.content);
    lines.push("'@ | Set-Content -Path $target -Encoding utf8");
  }
  lines.push("");
  lines.push('Write-Host "tproxy configuration applied."');
  return lines.join("\n");
}

export function applyScriptAvailable(shell: ApplyScriptShell, configs: ManualConfigEntry[]): boolean {
  if (configs.length === 0) return false;
  if (shell === "bash") {
    return configs.every((config) => bashSupportsPath(config.filename));
  }
  return true;
}
