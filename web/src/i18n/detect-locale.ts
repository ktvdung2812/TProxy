import type { AppLocale } from "./constants";
import { DEFAULT_LOCALE } from "./constants";
import { getStoredLocale } from "./storage";

function normalizeLocale(raw: string): AppLocale | null {
  const lower = raw.toLowerCase();
  if (lower === "vi" || lower.startsWith("vi-")) return "vi";
  if (lower === "en" || lower.startsWith("en-")) return "en";
  return null;
}

function getQueryParamLocale(): AppLocale | null {
  try {
    const params = new URLSearchParams(window.location.search);
    const lang = params.get("lang");
    if (lang) return normalizeLocale(lang);
  } catch {
    // SSR or no window
  }
  return null;
}

function getBrowserLocale(): AppLocale | null {
  try {
    const languages = navigator.languages || [navigator.language];
    for (const lang of languages) {
      const normalized = normalizeLocale(lang);
      if (normalized) return normalized;
    }
  } catch {
    // no navigator
  }
  return null;
}

/**
 * Detection order:
 * 1. URL query param `?lang=`
 * 2. localStorage
 * 3. navigator.language
 * 4. default locale (vi)
 */
export function detectLocale(): AppLocale {
  return (
    getQueryParamLocale() ??
    getStoredLocale() ??
    getBrowserLocale() ??
    DEFAULT_LOCALE
  );
}
