import { Button } from "./ui";
import { useLocale } from "../i18n/LocaleProvider";

/** Header/login button that flips between Vietnamese and English. */
export function LanguageToggle() {
  const { locale, nextLocale, setLocale, isChangingLocale } = useLocale();

  return (
    <Button
      type="button"
      variant="secondary"
      size="md"
      icon="translate"
      disabled={isChangingLocale}
      onClick={() => void setLocale(nextLocale)}
      aria-label={locale === "vi" ? "Switch to English" : "Chuyển sang tiếng Việt"}
    >
      {locale === "vi" ? "EN" : "VI"}
    </Button>
  );
}
