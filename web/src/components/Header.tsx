import { useEffect, useRef, useState, type ReactNode } from "react";
import { Button, IconButton, cn } from "./ui";
import { ThemeToggle } from "./ThemeToggle";

const GITHUB_REPO_URL = "https://github.com/ktvdung2812/TProxy";
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
    <header className="app-header">
      <div className="header-title">
        {onOpenNav ? (
          <IconButton icon="menu" label="Mở menu" className="header-menu-btn" onClick={onOpenNav} />
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
        {extra}
        {onRefresh && (
          <Button variant="secondary" size="md" icon={loading ? "" : "refresh"} loading={loading} onClick={onRefresh}>
            {loading ? "Loading…" : "Refresh"}
          </Button>
        )}
        <a
          href={GITHUB_REPO_URL}
          target="_blank"
          rel="noopener noreferrer"
          className="icon-btn header-github-link"
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
        <ThemeToggle />
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
                  <span>Đăng xuất</span>
                </button>
              ) : null}
            </div>
          ) : null}
        </div>
      </div>
    </header>
  );
}
