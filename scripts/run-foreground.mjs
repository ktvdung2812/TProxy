import { spawn } from "node:child_process";

/** Spawn a process with inherited stdio and exit with its status. */
export function runForeground(command, args, { cwd } = {}) {
  const child = spawn(command, args, {
    cwd,
    env: process.env,
    stdio: "inherit",
    windowsHide: false,
  });

  const stop = () => {
    if (child.exitCode === null && !child.killed) {
      child.kill();
    }
  };
  process.on("SIGINT", stop);
  process.on("SIGTERM", stop);

  child.on("error", (err) => {
    if (err.code === "ENOENT") {
      console.error(`command not found: ${command}`);
    } else {
      console.error(err.message);
    }
    process.exit(1);
  });
  child.on("exit", (code, signal) => {
    if (signal) {
      process.exit(1);
    }
    process.exit(code ?? 1);
  });
}
