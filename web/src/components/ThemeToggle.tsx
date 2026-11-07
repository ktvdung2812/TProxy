import { useTranslation } from "react-i18next";
import { useTheme, type Theme } from "./useTheme";

export function ThemeToggle() {
  const { t } = useTranslation();
  const { theme, setTheme } = useTheme();
  return (
    <div className="theme-select icon-btn">
      <span className="material-symbols-outlined" aria-hidden="true">
        {theme === "dark" ? "dark_mode" : theme === "light" ? "light_mode" : "brightness_auto"}
      </span>
      <span className="material-symbols-outlined theme-select-arrow" aria-hidden="true">expand_more</span>
      <select
        aria-label={t("common.themeToggle.label")}
        title={t("common.themeToggle.label")}
        value={theme}
        onChange={(event) => setTheme(event.target.value as Theme)}
      >
        <option value="light">{t("common.themeToggle.light")}</option>
        <option value="dark">{t("common.themeToggle.dark")}</option>
        <option value="auto">{t("common.themeToggle.auto")}</option>
      </select>
    </div>
  );
}
