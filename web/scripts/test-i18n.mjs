import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";

const localeDir = new URL("../src/locales/", import.meta.url);
const readLocale = (name) => JSON.parse(readFileSync(new URL(name, localeDir), "utf8"));
const flatten = (object, prefix = "") => Object.fromEntries(Object.entries(object).flatMap(([key, value]) => {
  const path = prefix ? `${prefix}.${key}` : key;
  return typeof value === "object" && value !== null ? Object.entries(flatten(value, path)) : [[path, value]];
}));
const en = flatten(readLocale("en.json"));
const vi = flatten(readLocale("vi.json"));
const logicalKeys = (entries) => new Set(Object.keys(entries).map((key) => key.replace(/_(one|other)$/, "")));
const variables = (value) => [...new Set([...value.matchAll(/{{\s*([^{}\s,]+)[^{}]*}}/g)].map((match) => match[1]))].sort();
const checkTranslations = (english, vietnamese) => {
  assert.deepEqual([...logicalKeys(english)].sort(), [...logicalKeys(vietnamese)].sort(), "locale keys differ");
  for (const key of logicalKeys(english)) {
    assert.deepEqual(variables(english[`${key}_other`] ?? english[key]), variables(vietnamese[`${key}_other`] ?? vietnamese[key]), `interpolation differs: ${key}`);
  }
};
checkTranslations(en, vi);
for (const feature of ["a", "b"]) {
  const english = `feature-${feature}.en.json`;
  const vietnamese = `feature-${feature}.vi.json`;
  assert.equal(existsSync(new URL(english, localeDir)), existsSync(new URL(vietnamese, localeDir)), `missing ${feature} locale fragment`);
  if (existsSync(new URL(english, localeDir))) checkTranslations(flatten(readLocale(english)), flatten(readLocale(vietnamese)));
}

const server = await createServer({ configFile: false, root: fileURLToPath(new URL("..", import.meta.url)), server: { middlewareMode: true }, appType: "custom" });
try {
  const { detectLocale } = await server.ssrLoadModule("/src/i18n/detect-locale.ts");
  const storage = new Map();
  globalThis.localStorage = { getItem: (key) => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value) };
  globalThis.window = { location: { search: "?lang=EN-us" } };
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: { languages: ["fr", "vi-VN"] } });
  assert.equal(detectLocale(), "en");
  window.location.search = "?lang=invalid";
  storage.set("tproxy.locale", "en");
  assert.equal(detectLocale(), "en");
  storage.clear();
  assert.equal(detectLocale(), "vi");
  navigator.languages = ["fr"];
  assert.equal(detectLocale(), "vi");
  navigator.languages = [];
  navigator.language = "en-GB";
  assert.equal(detectLocale(), "en");

  const { default: i18n } = await server.ssrLoadModule("/src/i18n/config.ts");
  const { NAV_SECTIONS } = await server.ssrLoadModule("/src/navigation.ts");
  for (const locale of ["en", "vi"]) {
    for (const section of NAV_SECTIONS) {
      assert.ok(i18n.exists(section.i18nKey, { lng: locale, fallbackLng: false }), `${locale}: ${section.i18nKey}`);
      for (const route of section.routes) {
        for (const key of [route.i18nKey, `${route.i18nKey}_page_title`, `${route.i18nKey}_page_desc`]) {
          assert.ok(i18n.exists(key, { lng: locale, fallbackLng: false }), `${locale}: ${key}`);
        }
      }
    }
  }
  await i18n.changeLanguage("en");
  assert.equal(i18n.t("common.close"), "Close");
  await i18n.changeLanguage("vi");
  assert.equal(i18n.t("common.close"), "Đóng");
  i18n.addResource("vi", "translation", "fallbackProbe", "Dự phòng");
  await i18n.changeLanguage("en");
  assert.equal(i18n.t("fallbackProbe"), "Dự phòng");
  for (const feature of ["a", "b"]) {
    for (const locale of ["en", "vi"]) {
      const filename = `feature-${feature}.${locale}.json`;
      if (!existsSync(new URL(filename, localeDir))) continue;
      const namespace = `feature${feature.toUpperCase()}`;
      assert.deepEqual(i18n.getResource(locale, "translation", namespace), readLocale(filename)[namespace], `${filename} not loaded`);
    }
  }
  console.log("Locale keys, interpolation, detection, switching, fallback and available fragments: OK");
} finally {
  await server.close();
}
