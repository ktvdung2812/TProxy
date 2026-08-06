import type { AppLocale } from "./constants";
import { SUPPORTED_LOCALES, STORAGE_KEY } from "./constants";

export function getStoredLocale(): AppLocale | null {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored && (SUPPORTED_LOCALES as readonly string[]).includes(stored)) {
      return stored as AppLocale;
    }
  } catch {
    // localStorage not available
  }
  return null;
}

export function setStoredLocale(locale: AppLocale): void {
  try {
    localStorage.setItem(STORAGE_KEY, locale);
  } catch {
    // localStorage not available
  }
}
