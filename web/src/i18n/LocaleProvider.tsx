import React, { createContext, useCallback, useContext, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { AppLocale } from "./constants";
import { DEFAULT_LOCALE, LOCALE_LABELS, SUPPORTED_LOCALES } from "./constants";
import { setStoredLocale } from "./storage";

export interface LocaleContextValue {
  locale: AppLocale;
  setLocale: (locale: AppLocale) => void;
  localeLabel: string;
  nextLocale: AppLocale;
  isChangingLocale: boolean;
}

const LocaleContext = createContext<LocaleContextValue | null>(null);

export function LocaleProvider({ children }: { children: React.ReactNode }) {
  const { i18n } = useTranslation();
  const [isChangingLocale, setIsChangingLocale] = useState(false);

  const locale = (
    (SUPPORTED_LOCALES as readonly string[]).includes(i18n.language) ? i18n.language : DEFAULT_LOCALE
  ) as AppLocale;

  const nextLocale: AppLocale = locale === "vi" ? "en" : "vi";

  const setLocale = useCallback(
    async (newLocale: AppLocale) => {
      if (!(SUPPORTED_LOCALES as readonly string[]).includes(newLocale) || newLocale === i18n.language) return;
      setIsChangingLocale(true);
      try {
        await i18n.changeLanguage(newLocale);
        setStoredLocale(newLocale);
        // Keep an explicit URL override in sync so it cannot undo the choice on reload.
        try {
          const url = new URL(window.location.href);
          if (url.searchParams.has("lang")) {
            url.searchParams.set("lang", newLocale);
            window.history.replaceState(window.history.state, "", url);
          }
        } catch {
          // Storage still preserves the choice when history is unavailable.
        }
        document.documentElement.lang = newLocale;
      } finally {
        setIsChangingLocale(false);
      }
    },
    [i18n],
  );

  useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);

  return (
    <LocaleContext.Provider
      value={{
        locale,
        setLocale,
        localeLabel: LOCALE_LABELS[locale],
        nextLocale,
        isChangingLocale,
      }}
    >
      {children}
    </LocaleContext.Provider>
  );
}

export function useLocale(): LocaleContextValue {
  const ctx = useContext(LocaleContext);
  if (!ctx) {
    throw new Error("useLocale must be used within LocaleProvider");
  }
  return ctx;
}
