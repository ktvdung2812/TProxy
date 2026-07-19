import { useEffect, useRef, useState, type ReactNode } from "react";
import { Button, IconButton, cn } from "./ui";
import { ThemeToggle } from "./ThemeToggle";

type HeaderProps = {
  title: string;
  description?: string;
  icon?: string;
  onRefresh?: () => void;
  onLogout?: () => void;
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
        <h1>
          <span className="material-symbols-outlined">{icon}</span>
          {title}
        </h1>
        {description ? <p>{description}</p> : null}
      </div>

      <div className="header-actions">
        {extra}
        {onRefresh && (
          <Button variant="secondary" size="md" icon={loading ? "" : "refresh"} loading={loading} onClick={onRefresh}>
            {loading ? "Loading…" : "Refresh"}
          </Button>
        )}
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
