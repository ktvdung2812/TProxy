import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  appendFileSync,
  createReadStream,
  createWriteStream,
  existsSync,
  mkdirSync,
  readFileSync,
  rmSync,
} from "node:fs";
import { homedir, tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { Readable } from "node:stream";
import { pipeline } from "node:stream/promises";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const MIN_NODE_MAJOR = 18;

export function ensureNode() {
  const major = Number(process.versions.node.split(".")[0]);
  if (major >= MIN_NODE_MAJOR) {
    return;
  }
  console.error(
    `Node.js ${process.versions.node} is too old. This project needs Node.js ${MIN_NODE_MAJOR} or newer.`,
  );
  process.exit(1);
}

export async function ensureGo() {
  const found = findGo();
  if (found) {
    return found;
  }
  const required = requiredGoVersion();
  let release;
  try {
    release = await selectGoRelease(required);
  } catch (err) {
    console.error(err instanceof Error ? err.message : String(err));
    process.exit(1);
  }
  console.log(`Installing Go ${formatVersion(release.version)} (project requires ${formatVersion(required)})...`);
  try {
    await installGoRelease(release);
  } catch (err) {
    console.error(err instanceof Error ? err.message : String(err));
    process.exit(1);
  }
  const installed = findGo();
  if (!installed) {
    console.error("Go install finished but the go binary was not found.");
    process.exit(1);
  }
  persistUserPath(dirname(installed));
  const version = readGoVersion(installed);
  console.log(`Installed Go ${version ? formatVersion(version) : "toolchain"} at ${dirname(dirname(installed))}`);
  return installed;
}

export function findGo() {
  const required = requiredGoVersion();
  let best = null;
  for (const bin of candidateBins()) {
    const version = readGoVersion(bin);
    if (!version || cmpVersion(version, required) < 0) {
      continue;
    }
    if (!best || cmpVersion(version, best.version) > 0) {
      best = { bin, version };
    }
  }
  if (!best) {
    return null;
  }
  prependPath(dirname(best.bin));
  return best.bin;
}

function requiredGoVersion() {
  const text = readFileSync(join(root, "go.mod"), "utf8");
  const match = text.match(/^go\s+(\d+\.\d+(?:\.\d+)?)\s*$/m);
  if (!match) {
    console.error("Could not read the Go version from go.mod");
    process.exit(1);
  }
  return parseVersion(match[1]);
}

function candidateBins() {
  const dirs = [];
  if (process.platform === "win32") {
    const local = process.env.LOCALAPPDATA || join(homedir(), "AppData", "Local");
    dirs.push(join(local, "Programs", "Go", "bin"));
    dirs.push(join(process.env.ProgramFiles || "C:\\Program Files", "Go", "bin"));
    dirs.push(join(process.env["ProgramFiles(x86)"] || "C:\\Program Files (x86)", "Go", "bin"));
  } else {
    dirs.push(join(homedir(), ".local", "go", "bin"));
    dirs.push("/usr/local/go/bin");
    dirs.push("/usr/lib/go/bin");
  }
  const pathValue = process.env.Path || process.env.PATH || "";
  const sep = process.platform === "win32" ? ";" : ":";
  for (const dir of pathValue.split(sep)) {
    if (dir) {
      dirs.push(dir);
    }
  }
  const names = process.platform === "win32" ? ["go.exe"] : ["go"];
  const seen = new Set();
  const bins = [];
  for (const dir of dirs) {
    for (const name of names) {
      const bin = join(dir, name);
      const key = process.platform === "win32" ? bin.toLowerCase() : bin;
      if (seen.has(key) || !existsSync(bin)) {
        continue;
      }
      seen.add(key);
      bins.push(bin);
    }
  }
  return bins;
}

function readGoVersion(bin) {
  const result = spawnSync(bin, ["version"], {
    encoding: "utf8",
    windowsHide: true,
  });
  if (result.error || result.status !== 0) {
    return null;
  }
  return parseVersion(result.stdout);
}

async function selectGoRelease(required) {
  const response = await fetch("https://go.dev/dl/?mode=json");
  if (!response.ok) {
    throw new Error(`Could not list Go releases (${response.status})`);
  }
  const releases = await response.json();
  const os = { win32: "windows", darwin: "darwin", linux: "linux" }[process.platform];
  const arch = { x64: "amd64", arm64: "arm64", ia32: "386" }[process.arch];
  if (!os || !arch) {
    throw new Error(`Unsupported platform ${process.platform}/${process.arch}`);
  }
  const matches = [];
  for (const release of releases) {
    if (!release.stable) {
      continue;
    }
    const version = parseVersion(release.version);
    if (!version || cmpVersion(version, required) < 0) {
      continue;
    }
    const file = (release.files || []).find(
      (item) => item.kind === "archive" && item.os === os && item.arch === arch,
    );
    if (!file) {
      continue;
    }
    matches.push({ version, file });
  }
  matches.sort((a, b) => cmpVersion(b.version, a.version));
  if (matches.length === 0) {
    throw new Error(`No stable Go release >= ${formatVersion(required)} for ${os}/${arch}`);
  }
  return matches[0];
}

async function installGoRelease(release) {
  const archive = join(tmpdir(), `tproxy-${release.file.filename}`);
  const url = `https://go.dev/dl/${release.file.filename}`;
  try {
    await download(url, archive);
    const actual = await fileSha256(archive);
    const expected = String(release.file.sha256 || "").toLowerCase();
    if (!expected || actual !== expected) {
      throw new Error(`Checksum mismatch for ${release.file.filename}`);
    }
    const managed = managedGoRoot();
    rmSync(managed, { recursive: true, force: true });
    mkdirSync(dirname(managed), { recursive: true });
    const extracted = spawnSync("tar", ["-xf", archive, "-C", dirname(managed)], {
      stdio: "inherit",
      windowsHide: true,
    });
    if (extracted.error) {
      throw new Error(`Could not unpack Go: ${extracted.error.message}`);
    }
    if ((extracted.status ?? 1) !== 0) {
      throw new Error("Could not unpack the Go archive");
    }
    const binName = process.platform === "win32" ? "go.exe" : "go";
    if (!existsSync(join(managed, "bin", binName))) {
      throw new Error(`Go archive did not contain bin/${binName}`);
    }
  } finally {
    rmSync(archive, { force: true });
  }
}

function managedGoRoot() {
  if (process.platform === "win32") {
    const local = process.env.LOCALAPPDATA || join(homedir(), "AppData", "Local");
    return join(local, "Programs", "Go");
  }
  return join(homedir(), ".local", "go");
}

async function download(url, dest) {
  const response = await fetch(url, { signal: AbortSignal.timeout(10 * 60 * 1000) });
  if (!response.ok || !response.body) {
    throw new Error(`Download failed (${response.status}) ${url}`);
  }
  await pipeline(Readable.fromWeb(response.body), createWriteStream(dest));
}

function fileSha256(path) {
  return new Promise((resolve, reject) => {
    const hash = createHash("sha256");
    createReadStream(path)
      .on("error", reject)
      .on("data", (chunk) => hash.update(chunk))
      .on("end", () => resolve(hash.digest("hex")));
  });
}

function persistUserPath(binDir) {
  if (process.platform === "win32") {
    persistWindowsPath(binDir);
    return;
  }
  persistUnixPath(binDir);
}

function persistWindowsPath(binDir) {
  const quoted = `'${binDir.replaceAll("'", "''")}'`;
  const script = `
$dir = ${quoted}
$current = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($null -eq $current) { $current = '' }
$has = $false
foreach ($item in ($current -split ';')) {
  if ($item -and ($item.TrimEnd('\\') -ieq $dir.TrimEnd('\\'))) { $has = $true }
}
if (-not $has) {
  $next = if ($current -eq '') { $dir } else { "$dir;$current" }
  [Environment]::SetEnvironmentVariable('Path', $next, 'User')
}
`;
  const result = spawnSync("powershell.exe", ["-NoProfile", "-Command", script], {
    stdio: "inherit",
    windowsHide: true,
  });
  if (result.error || (result.status ?? 1) !== 0) {
    console.warn("Go is installed, but it could not be added to the user PATH.");
  }
}

function persistUnixPath(binDir) {
  const profile = join(homedir(), ".profile");
  const line = `export PATH="${binDir}:$PATH"`;
  const current = existsSync(profile) ? readFileSync(profile, "utf8") : "";
  if (current.includes(binDir)) {
    return;
  }
  appendFileSync(profile, `\n# Added by tproxy\n${line}\n`);
}

function prependPath(dir) {
  const key = process.platform === "win32" && process.env.Path ? "Path" : "PATH";
  const current = process.env[key] || "";
  const sep = process.platform === "win32" ? ";" : ":";
  const listed = current.split(sep).some((entry) => entry && entry.toLowerCase() === dir.toLowerCase());
  if (!listed) {
    process.env[key] = current ? `${dir}${sep}${current}` : dir;
  }
}

function parseVersion(text) {
  const match = String(text).match(/(\d+)\.(\d+)(?:\.(\d+))?/);
  if (!match) {
    return null;
  }
  return [Number(match[1]), Number(match[2]), Number(match[3] || 0)];
}

function cmpVersion(a, b) {
  for (let i = 0; i < 3; i++) {
    if (a[i] !== b[i]) {
      return a[i] - b[i];
    }
  }
  return 0;
}

function formatVersion(version) {
  return version.join(".");
}
