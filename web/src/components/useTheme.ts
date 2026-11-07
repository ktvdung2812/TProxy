import { useEffect, useState } from "react";

export type Theme = "light" | "dark" | "auto";
const STORAGE_KEY = "tproxy-theme";

function readInitialTheme(): Theme {
  if (typeof window === "undefined") return "dark";
  try {
    const saved = window.localStorage.getItem(STORAGE_KEY);
    if (saved === "light" || saved === "dark" || saved === "auto") return saved;
  } catch {
    /* ignore */
  }
  return "dark"; // default to dark to match the previous control center
}

function applyTheme(isDark: boolean) {
  if (typeof document === "undefined") return;
  const root = document.documentElement;
  root.classList.toggle("dark", isDark);
  root.classList.toggle("light", !isDark);
}

/** Theme controller. Defaults to dark; persists choice to localStorage. */
export function useTheme() {
  const [theme, setTheme] = useState<Theme>(readInitialTheme);

  useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const update = () => applyTheme(theme === "dark" || (theme === "auto" && media.matches));
    update();
    if (theme === "auto") media.addEventListener("change", update);
    try {
      window.localStorage.setItem(STORAGE_KEY, theme);
    } catch {
      /* ignore */
    }
    return () => media.removeEventListener("change", update);
  }, [theme]);

  return { theme, setTheme };
}
