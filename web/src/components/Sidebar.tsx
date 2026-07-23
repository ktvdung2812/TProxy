import { NavLink, useLocation } from "react-router-dom";
import { cn } from "./ui";
import { isRouteActive, NAV_SECTIONS } from "../navigation";

type SidebarProps = {
  onClose?: () => void;
  online?: boolean;
  collapsed?: boolean;
  onToggleCollapse?: () => void;
  collapseLabel?: string;
  onLogout?: () => void;
};

/** Vibrancy sidebar with macOS traffic lights, gradient brand logo, grouped nav, status. */
export function Sidebar({ onClose, online = true, collapsed = false, onToggleCollapse, collapseLabel, onLogout }: SidebarProps) {
  return (
    <aside className={cn("sidebar bg-vibrancy", collapsed && "is-collapsed")}>
      <div className="sidebar-header">
        {!collapsed && (
          <div className="traffic-lights" style={{ marginBottom: 18 }}>
            <span className="traffic-light red" />
            <span className="traffic-light yellow" />
            <span className="traffic-light green" />
          </div>
        )}

        <div className="sidebar-brand-row">
          <NavLink className="sidebar-brand" to="/" onClick={() => onClose?.()} title="tproxy control center">
            <span className="brand-mark">
              <span className="material-symbols-outlined">hub</span>
            </span>
            {!collapsed && (
              <span className="sidebar-brand-text">
                <span className="brand-name">tproxy</span>
                <br />
                <span className="brand-sub">control center</span>
              </span>
            )}
          </NavLink>

          {onToggleCollapse && (
            <button
              type="button"
              className="sidebar-collapse-btn"
              onClick={onToggleCollapse}
              aria-label={collapseLabel || (collapsed ? "Expand sidebar" : "Collapse sidebar")}
              title={collapseLabel || (collapsed ? "Expand sidebar" : "Collapse sidebar")}
            >
              <span className="material-symbols-outlined">{collapsed ? "chevron_right" : "chevron_left"}</span>
            </button>
          )}
        </div>
      </div>

      <nav className="sidebar-nav custom-scrollbar">
        {NAV_SECTIONS.map((section) => (
          <div key={section.id} className="nav-section">
            {!collapsed && <p className="nav-section-label">{section.label}</p>}
            {collapsed && section.id !== NAV_SECTIONS[0]?.id ? <div className="nav-section-divider" aria-hidden /> : null}
            {section.routes.map((item) => (
              <NavButton key={item.id} item={item} onClose={onClose} collapsed={collapsed} />
            ))}
          </div>
        ))}
      </nav>

      <div className="sidebar-status" title={online ? "Gateway online" : "Gateway offline"}>
        <span className={cn("status-dot", !online && "offline")} />
        <span className="sidebar-status-label">{online ? "Gateway online" : "Gateway offline"}</span>
      </div>
      {onLogout ? (
        <button type="button" className="sidebar-logout-btn" onClick={onLogout} title="Đăng xuất">
          <span className="material-symbols-outlined">logout</span>
          {!collapsed ? <span>Đăng xuất</span> : null}
        </button>
      ) : null}
    </aside>
  );
}

function NavButton({
  item,
  onClose,
  collapsed,
}: {
  item: { id: string; path: string; label: string; icon: string };
  onClose?: () => void;
  collapsed?: boolean;
}) {
  const location = useLocation();
  const active = isRouteActive(location.pathname, item);

  return (
    <NavLink
      className={() => cn("nav-link", active && "active")}
      to={item.path}
      end={item.id === "overview"}
      onClick={() => onClose?.()}
      title={collapsed ? item.label : undefined}
    >
      <span className="material-symbols-outlined">{item.icon}</span>
      <span className="nav-link-label">{item.label}</span>
    </NavLink>
  );
}
