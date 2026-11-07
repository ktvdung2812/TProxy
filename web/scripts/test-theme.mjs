import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import { stripTypeScriptTypes } from "node:module";

const source = readFileSync(new URL("../src/components/useTheme.ts", import.meta.url), "utf8");
const compiled = stripTypeScriptTypes(source)
  .replace('import { useEffect, useState } from "react";', "")
  .replace("export function useTheme", "function useTheme") + "\nexports.useTheme = useTheme;";
const html = readFileSync(new URL("../index.html", import.meta.url), "utf8");
const startup = html.match(/<script>([\s\S]*?)<\/script>/)[1];

for (const systemDark of [false, true]) {
  for (const saved of [null, "light", "dark", "auto"]) {
    let stored = saved;
    const classes = new Set(["dark"]);
    const root = { classList: {
      toggle(name, enabled) { if (enabled) classes.add(name); else classes.delete(name); },
      add(name) { classes.add(name); },
    } };
    let listener;
    const media = {
      matches: systemDark,
      addEventListener(_event, callback) { listener = callback; },
      removeEventListener(_event, callback) { if (listener === callback) listener = undefined; },
    };
    const localStorage = { getItem: () => stored, setItem: (_key, value) => { stored = value; } };
    const window = { localStorage, matchMedia: () => media };
    const document = { documentElement: root };
    const expectedDark = saved === "auto" ? systemDark : saved !== "light";
    runInNewContext(startup, { window, document, localStorage });
    assert.equal(classes.has("dark"), expectedDark, `startup: ${saved}, system dark: ${systemDark}`);

    let state;
    let cleanup;
    const react = {
      useState(initial) { state ??= initial(); return [state, (value) => { state = value; }]; },
      useEffect(effect) { cleanup?.(); cleanup = effect(); },
    };
    const exports = {};
    runInNewContext(compiled, { exports, ...react, window, document });
    let control = exports.useTheme();
    assert.equal(classes.has("dark"), expectedDark, `mounted: ${saved}, system dark: ${systemDark}`);
    if (saved === "auto") {
      media.matches = !systemDark;
      listener();
      assert.equal(classes.has("dark"), !systemDark, "auto follows system changes");
    }
    for (const next of ["light", "auto", "dark"]) {
      control.setTheme(next);
      control = exports.useTheme();
      assert.equal(control.theme, next);
      assert.equal(stored, next);
      assert.equal(classes.has("dark"), next === "dark" || (next === "auto" && media.matches));
      assert.equal(Boolean(listener), next === "auto");
    }
    cleanup();
  }
}
console.log("Theme startup, persistence and system changes: OK");
