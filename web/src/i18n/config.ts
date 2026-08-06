import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import LanguageDetector from "i18next-browser-languagedetector";
import { DEFAULT_LOCALE, SUPPORTED_LOCALES, STORAGE_KEY } from "./constants";
import { detectLocale } from "./detect-locale";

// Import locale resources
import vi from "../locales/vi.json";
import en from "../locales/en.json";

const resources = { vi: { translation: vi }, en: { translation: en } };

i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources,
    lng: detectLocale(),
    fallbackLng: DEFAULT_LOCALE,
    supportedLngs: [...SUPPORTED_LOCALES],

    interpolation: {
      escapeValue: false, // React already escapes
    },

    detection: {
      order: ["querystring", "localStorage", "navigator"],
      lookupQuerystring: "lang",
      lookupLocalStorage: STORAGE_KEY,
      caches: ["localStorage"],
    },

    // Dev helpers
    saveMissing: import.meta.env.DEV,
    missingKeyHandler: (_lngs, _ns, key) => {
      if (import.meta.env.DEV) {
        console.warn(`[i18n] Missing key: ${key}`);
      }
    },

    react: {
      useSuspense: false,
    },
  });

export default i18n;
