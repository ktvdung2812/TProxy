import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const npmPkg = JSON.parse(readFileSync(join(root, "npm/package.json"), "utf8"));
const version = String(npmPkg.version || "").trim();
if (!version) {
  console.error("npm/package.json is missing version");
  process.exit(1);
}
writeFileSync(join(root, "internal/version/current.txt"), `${version}\n`, "utf8");
console.log(`synced version ${version} -> internal/version/current.txt`);
