import { useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Button, IconButton, cn } from "./ui";
import { ThemeToggle } from "./ThemeToggle";
import { LanguageToggle } from "./LanguageToggle";

const GITHUB_REPO_URL = "https://github.com/ktvdung2812/TProxy";
const BUY_ME_A_COFFEE_URL = "https://buymeacoffee.com/ktvdung";
type HeaderProps = {
  title: string;
  description?: string;
  icon?: string;
  onRefresh?: () => void;
  onLogout?: () => void;
  onOpenNav?: () => void;
  loading?: boolean;
  extra?: ReactNode;
};

/** Top bar: page title/description, refresh, theme toggle. */
export function Header({
  title,
  description,
  icon = "space_dashboard",
  onRefresh,
  onLogout,
  onOpenNav,
  loading = false,
  extra,
}: HeaderProps) {
  const { t } = useTranslation();
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!menuOpen) return undefined;
    const onPointerDown = (event: MouseEvent) => {
      if (!menuRef.current?.contains(event.target as Node)) {
        setMenuOpen(false);
      }
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setMenuOpen(false);
    };
    document.addEventListener("mousedown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("mousedown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [menuOpen]);

  return (
    <header className={cn("app-header", extra ? "app-header-with-extra" : false)}>
      <div className="header-title">
        {onOpenNav ? (
          <IconButton icon="menu" label={t("nav.expandMenu")} className="header-menu-btn" onClick={onOpenNav} />
        ) : null}
        <div className="header-title-copy">
          <h1>
            <span className="material-symbols-outlined">{icon}</span>
            {title}
          </h1>
          {description ? <p>{description}</p> : null}
        </div>
      </div>

      <div className="header-actions">
        {extra ? <div className="header-extra">{extra}</div> : null}
        {onRefresh && (
          <Button className="header-refresh" variant="secondary" size="md" icon={loading ? "" : "refresh"} loading={loading} onClick={onRefresh}>
            <span className="header-refresh-label">{loading ? t("common.loading") : t("common.refresh")}</span>
          </Button>
        )}
        <a
          href={GITHUB_REPO_URL}
          target="_blank"
          rel="noopener noreferrer"
          className="icon-btn header-social-link"
          aria-label="GitHub repository"
          title="GitHub repository"
        >
          <svg viewBox="0 0 16 16" width="18" height="18" aria-hidden="true" focusable="false">
            <path
              fill="currentColor"
              d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"
            />
          </svg>
        </a>
        <a
          href={BUY_ME_A_COFFEE_URL}
          target="_blank"
          rel="noopener noreferrer"
          className="icon-btn header-social-link"
          aria-label="Buy me a coffee"
          title="Buy me a coffee"
        >
          <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true" focusable="false">
            <path
              fill="currentColor"
              d="M20.216 6.415h-.132V6.3a4.363 4.363 0 00-4.363-4.363H8.279A4.363 4.363 0 003.916 6.3v.115H3.784a2.106 2.106 0 00-2.1 2.1v7.894a2.106 2.106 0 002.1 2.1h16.432a2.106 2.106 0 002.1-2.1V8.515a2.106 2.106 0 00-2.1-2.1zM5.816 6.3a2.463 2.463 0 012.463-2.463h7.442a2.463 2.463 0 012.463 2.463v.115H5.816V6.3zm14.5 10.109a.79.79 0 01-.79.79H4.474a.79.79 0 01-.79-.79V8.515a.79.79 0 01.79-.79h15.052a.79.79 0 01.79.79v7.894zM7.263 15.79h9.474a.79.79 0 000-1.579H7.263a.79.79 0 000 1.579z"
            />
          </svg>
        </a>
        <div className="header-theme-toggle">
          <ThemeToggle />
        </div>
        <div className="header-language-toggle">
          <LanguageToggle />
        </div>
        <div className="header-menu" ref={menuRef}>
          <IconButton
            icon="more_vert"
            label="More"
            aria-expanded={menuOpen}
            onClick={() => setMenuOpen((open) => !open)}
          />
          {menuOpen ? (
            <div className="header-menu-panel" role="menu">
              {onLogout ? (
                <button
                  type="button"
                  className={cn("header-menu-item", "header-menu-item-danger")}
                  role="menuitem"
                  onClick={() => {
                    setMenuOpen(false);
                    onLogout();
                  }}
                >
                  <span className="material-symbols-outlined">logout</span>
                  <span>{t("nav.logout")}</span>
                </button>
              ) : null}
            </div>
          ) : null}
        </div>
      </div>
    </header>
  );
}
