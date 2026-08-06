import { useTranslation } from "react-i18next";
import { IconButton } from "./ui";
import { useTheme } from "./useTheme";

/** Round header button that flips between light and dark mode. */
export function ThemeToggle() {
  const { t } = useTranslation();
  const { isDark, toggleTheme } = useTheme();
  return (
    <IconButton
      icon={isDark ? "light_mode" : "dark_mode"}
      label={isDark ? t("common.switchToLight") : t("common.switchToDark")}
      onClick={toggleTheme}
    />
  );
}
