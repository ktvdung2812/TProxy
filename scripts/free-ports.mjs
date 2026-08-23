#!/usr/bin/env node
import { execSync } from "node:child_process";

const PORTS = [28120, 28122];

function listeningPids(port) {
  if (process.platform === "win32") {
    return windowsListeningPids(port);
  }
  return unixListeningPids(port);
}

function unixListeningPids(port) {
  try {
    const out = execSync(`lsof -t -iTCP:${port} -sTCP:LISTEN`, {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"],
    });
    return [...new Set(out.trim().split(/\s+/).filter(Boolean))];
  } catch {
    return [];
  }
}

function windowsListeningPids(port) {
  let out = "";
  try {
    out = execSync("netstat -ano -p tcp", {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"],
      windowsHide: true,
    });
  } catch {
    return [];
  }
  const pids = new Set();
  for (const line of out.split(/\r?\n/)) {
    if (!/\bLISTENING\b/i.test(line)) {
      continue;
    }
    const parts = line.trim().split(/\s+/);
    if (parts.length < 5) {
      continue;
    }
    const local = parts[1];
    const pid = parts[parts.length - 1];
    const match = local.match(/:(\d+)$/);
    if (!match || Number(match[1]) !== port) {
      continue;
    }
    if (!/^\d+$/.test(pid) || pid === "0") {
      continue;
    }
    pids.add(pid);
  }
  return [...pids];
}

function killPid(pid) {
  try {
    if (process.platform === "win32") {
      execSync(`taskkill /PID ${pid} /F`, {
        stdio: "ignore",
        windowsHide: true,
      });
      return;
    }
    process.kill(Number(pid), "SIGTERM");
  } catch {
    // Process already gone.
  }
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

const occupied = [];
for (const port of PORTS) {
  const pids = listeningPids(port);
  if (pids.length === 0) {
    continue;
  }
  occupied.push({ port, pids });
  console.log(`Freeing port ${port} (pid: ${pids.join(", ")})`);
  for (const pid of pids) {
    killPid(pid);
  }
}

if (occupied.length > 0) {
  await sleep(1000);
}

for (const port of PORTS) {
  const pids = listeningPids(port);
  if (pids.length > 0) {
    console.error(`Port ${port} is still in use`);
    process.exit(1);
  }
}
