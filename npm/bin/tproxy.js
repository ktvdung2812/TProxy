#!/usr/bin/env node
const { spawn } = require("node:child_process");
const path = require("node:path");
const fs = require("node:fs");
const os = require("node:os");

const packageRoot = path.resolve(__dirname, "..");
const repoRoot = path.resolve(packageRoot, "..");
const binary = path.join(repoRoot, "bin", "tproxy");
const defaultConfigTemplate = path.join(packageRoot, "config.default.yaml");
const userConfigDir = path.join(os.homedir(), ".tproxy");
const userConfigPath = path.join(userConfigDir, "config.yaml");

function ensureUserConfig() {
  if (fs.existsSync(userConfigPath)) {
    return userConfigPath;
  }
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
  const hasConfigFlag = args.some((arg, index) => arg === "--config" || arg.startsWith("--config=") || (arg === "-config" && args[index + 1]));
  if (hasConfigFlag) {
    return args;
  }
  return ["--config", ensureUserConfig(), ...args];
}

const args = withDefaultConfig(process.argv.slice(2));

function runGoRun() {
  const child = spawn("go", ["run", "./cmd/tproxy", ...args], {
    cwd: repoRoot,
    stdio: "inherit",
    env: process.env,
  });
  child.on("exit", (code) => process.exit(code ?? 0));
}

if (fs.existsSync(binary)) {
  const child = spawn(binary, args, { stdio: "inherit", env: process.env });
  child.on("exit", (code) => process.exit(code ?? 0));
} else {
  runGoRun();
}
