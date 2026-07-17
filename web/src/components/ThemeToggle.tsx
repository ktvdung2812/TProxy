import { IconButton } from "./ui";
import { useTheme } from "./useTheme";

/** Round header button that flips between light and dark mode. */
export function ThemeToggle() {
  const { isDark, toggleTheme } = useTheme();
  return (
    <IconButton
      icon={isDark ? "light_mode" : "dark_mode"}
      label={`Switch to ${isDark ? "light" : "dark"} mode`}
      onClick={toggleTheme}
    />
  );
}
