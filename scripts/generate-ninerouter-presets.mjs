#!/usr/bin/env node
/**
 * Regenerate internal/ninerouter/presets.go with connection metadata from 9router registry.
 * Run from repo root: node tproxy/scripts/generate-ninerouter-presets.mjs
 */
import fs from "fs";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const TPROXY_ROOT = path.resolve(__dirname, "..");
const REGISTRY_DIR = path.resolve(TPROXY_ROOT, "../9router-master/open-sse/providers/registry");
const OUT_FILE = path.join(TPROXY_ROOT, "internal/ninerouter/presets.go");

function readRegistryMeta() {
  const files = fs.readdirSync(REGISTRY_DIR).filter((f) => f.endsWith(".js") && f !== "index.js");
  const meta = {};
  for (const file of files) {
    const src = fs.readFileSync(path.join(REGISTRY_DIR, file), "utf8");
    const idM = src.match(/\bid:\s*["']([\w-]+)["']/);
    const catM = src.match(/\bcategory:\s*["']([\w-]+)["']/);
    if (!idM || !catM) continue;
    const id = idM[1];
    const authModesM = src.match(/\bauthModes:\s*\[([\s\S]*?)\]/);
    const authModes = authModesM
      ? [...authModesM[1].matchAll(/["']([\w-]+)["']/g)].map((m) => m[1])
      : [];
    const credentialAuthM = src.match(/^\s*authType:\s*["']([\w-]+)["']/m);
    const authHintM = src.match(/\bauthHint:\s*["']([^"']+)["']/);
    const apiKeyUrlM = src.match(/\bapiKeyUrl:\s*["']([^"']+)["']/);
    const supportsQuota = /features:\s*\{[\s\S]*?\busage:\s*true/.test(src);
    meta[id] = {
      category: catM[1],
      noAuth: /\bnoAuth:\s*true/.test(src),
      credentialAuth: credentialAuthM?.[1] || "",
      hasOAuth: /\bhasOAuth:\s*true/.test(src),
      authHint: authHintM?.[1] || "",
      apiKeyUrl: apiKeyUrlM?.[1] || "",
      authModes,
      supportsQuota,
    };
  }
  return meta;
}

function parseExistingPresets(source) {
  const presets = {};
  const block = source.match(/var Presets = map\[string\]Preset\{([\s\S]*?)\n\}/);
  if (!block) return presets;
  for (const line of block[1].split("\n")) {
    const idM = line.match(/^\t"([\w-]+)":\s*\{/);
    if (!idM) continue;
    const id = idM[1];
    const field = (name) => line.match(new RegExp(`${name}:"([^"]*)"`))?.[1] ?? "";
    const supportsQuota = line.match(/SupportsQuota:(true|false)/)?.[1] === "true";
    presets[id] = {
      id: field("ID") || id,
      type: field("Type"),
      name: field("Name"),
      baseURL: field("BaseURL"),
      authType: field("AuthType"),
      supportsQuota,
    };
  }
  return presets;
}

function parseAliases(source) {
  const aliases = {};
  const block = source.match(/var Aliases = map\[string\]string\{([\s\S]*?)\n\}/);
  if (!block) return aliases;
  const re = /"([\w-]+)":"([\w-]+)"/g;
  let m;
  while ((m = re.exec(block[1])) !== null) {
    aliases[m[1]] = m[2];
  }
  return aliases;
}

function goString(s) {
  return s.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
}

function goAuthModes(modes) {
  if (!modes.length) return "nil";
  return `[]string{${modes.map((mode) => `"${mode}"`).join(",")}}`;
}

function main() {
  const existingSource = fs.readFileSync(OUT_FILE, "utf8");
  const presets = parseExistingPresets(existingSource);
  const aliases = parseAliases(existingSource);
  const registry = readRegistryMeta();

  const ids = Object.keys(presets).sort();
  const lines = [
    "package ninerouter",
    "",
    'import "strings"',
    "",
    "// Code generated from 9router registry. Do not edit by hand.",
    "// Regenerate: node tproxy/scripts/generate-ninerouter-presets.mjs",
    "",
    "type Preset struct {",
    "\tID, Type, Name, BaseURL, AuthType, Category, CredentialAuth, AuthHint, ApiKeyURL string",
    "\tAuthModes []string",
    "\tSupportsQuota, NoAuth, HasOAuth bool",
    "}",
    "",
    "var Presets = map[string]Preset{",
  ];

  for (const id of ids) {
    const p = presets[id];
    const r = registry[id] || {};
    const category = r.category || "apikey";
    const supportsQuota = r.supportsQuota ?? p.supportsQuota ?? false;
    lines.push(
      `\t"${id}": {ID:"${p.id}",Type:"${p.type}",Name:"${goString(p.name)}",BaseURL:"${goString(p.baseURL)}",AuthType:"${p.authType}",Category:"${category}",CredentialAuth:"${r.credentialAuth || ""}",AuthHint:"${goString(r.authHint || "")}",ApiKeyURL:"${goString(r.apiKeyUrl || "")}",AuthModes:${goAuthModes(r.authModes || [])},SupportsQuota:${supportsQuota},NoAuth:${r.noAuth ? "true" : "false"},HasOAuth:${r.hasOAuth ? "true" : "false"}},`,
    );
  }
  lines.push("}", "", "var Aliases = map[string]string{");

  const aliasKeys = Object.keys(aliases).sort();
  for (const key of aliasKeys) {
    lines.push(`\t"${key}":"${aliases[key]}",`);
  }
  lines.push(
    "}",
    "",
    "func Lookup(providerID string) (Preset, bool) { id:=strings.TrimSpace(providerID); if id==\"\" {return Preset{},false}; if m,ok:=Aliases[id]; ok {id=m}; p,ok:=Presets[id]; return p,ok }",
    "func ResolveProviderID(providerID string) string { if m,ok:=Aliases[strings.TrimSpace(providerID)]; ok {return m}; return strings.TrimSpace(providerID) }",
    "",
  );

  fs.writeFileSync(OUT_FILE, lines.join("\n"));
  console.log(`Wrote ${ids.length} presets to ${OUT_FILE}`);
}

main();
