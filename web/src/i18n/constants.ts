export const SUPPORTED_LOCALES = ["vi", "en"] as const;
export type AppLocale = (typeof SUPPORTED_LOCALES)[number];
export const DEFAULT_LOCALE: AppLocale = "vi";
export const LOCALE_LABELS: Record<AppLocale, string> = {
  vi: "Tiếng Việt",
  en: "English",
};
export const STORAGE_KEY = "tproxy.locale";
