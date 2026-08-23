#!/usr/bin/env node

const url = process.env.TPROXY_HEALTH_URL || "http://127.0.0.1:28122/healthz";
// First `go run` on Windows can spend a minute or more downloading modules
// and compiling. 480 * 250ms = 2 minutes.
const attempts = Number(process.env.TPROXY_HEALTH_ATTEMPTS || 480);

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

for (let attempt = 1; attempt <= attempts; attempt++) {
  try {
    const res = await fetch(url, { signal: AbortSignal.timeout(2000) });
    if (res.ok) {
      console.log(`backend ready → ${url}`);
      process.exit(0);
    }
  } catch {
    // Backend not listening yet.
  }
  await sleep(250);
}

console.error(`backend did not become ready at ${url}`);
process.exit(1);
