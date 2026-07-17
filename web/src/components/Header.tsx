import type { ReactNode } from "react";
import { Button, IconButton } from "./ui";
import { ThemeToggle } from "./ThemeToggle";

type HeaderProps = {
  title: string;
  description?: string;
  icon?: string;
  onRefresh?: () => void;
  loading?: boolean;
  extra?: ReactNode;
};

/** Top bar: page title/description, refresh, theme toggle. */
export function Header({
  title,
  description,
  icon = "space_dashboard",
  onRefresh,
  loading = false,
  extra,
}: HeaderProps) {
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
        <IconButton icon="more_vert" label="More" />
      </div>
    </header>
  );
}
