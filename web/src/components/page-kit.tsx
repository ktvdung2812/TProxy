import { useEffect, useLayoutEffect, useRef, useState } from "react";
import type { ButtonHTMLAttributes, ReactNode, RefObject } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { cn } from "./ui";

/* ============================================================
   Page kit — the building blocks every control-plane page shares:
   stat tiles, toolbar controls, overflow menus, the bulk bar,
   first-run guides and status pills. Styles live in the cp-*
   block of styles.css.
   ============================================================ */

export type Tone = "neutral" | "success" | "warning" | "danger" | "info" | "muted" | "primary";

export function isTypingTarget(target: EventTarget | null) {
  const element = target as HTMLElement | null;
  if (!element) return false;
  // A focused checkbox or radio (e.g. just-ticked selection) must not swallow Esc and other page shortcuts.
  if (element.tagName === "INPUT" && ["checkbox", "radio"].includes((element as HTMLInputElement).type)) return false;
  return element.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(element.tagName);
}

/** True while any app modal is open, so page-level shortcuts stand down. */
export function isDialogOpen() {
  return typeof document !== "undefined" && Boolean(document.querySelector(".modal-overlay"));
}

function readStorage(key: string): string | null {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

function writeStorage(key: string, value: string) {
  try {
    window.localStorage.setItem(key, value);
  } catch {
    // Private mode or a full quota: the preference just does not persist.
  }
}

/** A string preference persisted in localStorage. */
export function useStoredState<T extends string>(key: string, fallback: T, allowed?: readonly T[]) {
  const [value, setValue] = useState<T>(() => {
    const stored = readStorage(key) as T | null;
    if (stored === null) return fallback;
    return !allowed || allowed.includes(stored) ? stored : fallback;
  });
  useEffect(() => {
    writeStorage(key, value);
  }, [key, value]);
  return [value, setValue] as const;
}

/* ------------------------------------------------------------
   Stat tiles
   ------------------------------------------------------------ */
export type PageStat = {
  key: string;
  label: ReactNode;
  value: ReactNode;
  hint?: ReactNode;
  icon?: string;
  tone?: Tone;
  /** Highlights the tile as the active filter. */
  active?: boolean;
  /** Makes the tile a filter button. */
  onClick?: () => void;
  title?: string;
  /** Renders the value in the mono face — for times, money and ids. */
  mono?: boolean;
  /** Dims the value when there is nothing to report (e.g. zero errors). */
  muted?: boolean;
};

/**
 * Numbers that answer "is everything fine?" at a glance. Tiles with an
 * onClick double as filters, so the answer is one click from the list.
 */
export function PageStats({ items, label }: { items: PageStat[]; label?: string }) {
  return (
    <div className="cp-stats" role="group" aria-label={label} style={{ ["--cp-stat-cols" as string]: items.length }}>
      {items.map((item) => {
        const body = (
          <>
            <span className="cp-stat-label">
              {item.icon ? <span className="material-symbols-outlined" aria-hidden>{item.icon}</span> : null}
              <span>{item.label}</span>
            </span>
            <span className={cn("cp-stat-value", item.mono && "cp-stat-value-mono")}>{item.value}</span>
            {item.hint ? <span className="cp-stat-hint">{item.hint}</span> : null}
          </>
        );
        const className = cn(
          "cp-stat",
          `cp-tone-${item.tone || "neutral"}`,
          item.active && "is-active",
          item.muted && "is-muted",
          !item.onClick && "is-static",
        );
        return item.onClick ? (
          <button
            key={item.key}
            type="button"
            className={className}
            aria-pressed={item.active}
            onClick={item.onClick}
            title={item.title}
          >
            {body}
          </button>
        ) : (
          <div key={item.key} className={className} title={item.title}>
            {body}
          </div>
        );
      })}
    </div>
  );
}

/* ------------------------------------------------------------
   Toolbar
   ------------------------------------------------------------ */
export function Toolbar({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn("cp-toolbar", className)}>{children}</div>;
}

export function ToolbarRow({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn("cp-toolbar-row", className)}>{children}</div>;
}

/** Right-aligned cluster inside a toolbar row. */
export function ToolbarEnd({ children }: { children: ReactNode }) {
  return <div className="cp-toolbar-end">{children}</div>;
}

type SearchFieldProps = {
  value: string;
  onChange: (value: string) => void;
  placeholder: string;
  /** Registers "/" as a page-wide shortcut to focus this field. */
  shortcut?: boolean;
  inputRef?: RefObject<HTMLInputElement | null>;
  className?: string;
};

export function SearchField({ value, onChange, placeholder, shortcut = true, inputRef, className }: SearchFieldProps) {
  const { t } = useTranslation();
  const ownRef = useRef<HTMLInputElement>(null);
  const ref = inputRef ?? ownRef;

  useEffect(() => {
    if (!shortcut) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "/" || event.metaKey || event.ctrlKey || event.altKey) return;
      if (isTypingTarget(event.target) || isDialogOpen()) return;
      event.preventDefault();
      ref.current?.focus();
      ref.current?.select();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [shortcut, ref]);

  return (
    <label className={cn("cp-search", className)}>
      <span className="material-symbols-outlined" aria-hidden>search</span>
      <input
        ref={ref}
        type="search"
        value={value}
        placeholder={placeholder}
        aria-label={placeholder}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={(event) => {
          if (event.key !== "Escape") return;
          if (value) {
            event.stopPropagation();
            onChange("");
          } else {
            event.currentTarget.blur();
          }
        }}
      />
      {value ? (
        <button type="button" className="cp-search-clear" onClick={() => onChange("")} aria-label={t("kit.clearSearch")}>
          <span className="material-symbols-outlined" aria-hidden>close</span>
        </button>
      ) : shortcut ? (
        <kbd className="cp-kbd" title={t("kit.searchShortcut")}>/</kbd>
      ) : null}
    </label>
  );
}

export type FilterTab<T extends string> = {
  value: T;
  label: ReactNode;
  icon?: string;
  /** A logo element (e.g. ProviderLogo) shown instead of an icon. */
  logo?: ReactNode;
  count?: number;
  title?: string;
  tone?: Tone;
};

/** Pill tabs for the page's main facet (provider, status, kind…). */
export function FilterTabs<T extends string>({
  items,
  value,
  onChange,
  label,
}: {
  items: FilterTab<T>[];
  value: T;
  onChange: (value: T) => void;
  label: string;
}) {
  return (
    <div className="cp-tabs" role="group" aria-label={label}>
      {items.map((item) => {
        const active = item.value === value;
        return (
          <button
            key={item.value}
            type="button"
            className={cn("cp-tab", item.tone && `cp-tone-${item.tone}`, active && "is-active")}
            aria-pressed={active}
            title={item.title}
            onClick={() => onChange(item.value)}
          >
            {item.logo ?? (item.icon ? <span className="material-symbols-outlined" aria-hidden>{item.icon}</span> : null)}
            <span>{item.label}</span>
            {item.count !== undefined ? <span className="cp-tab-count">{item.count}</span> : null}
          </button>
        );
      })}
    </div>
  );
}

export type SegmentItem<T extends string> = { value: T; label: string; icon?: string };

/** Compact segmented switch; icon-only items keep their label as tooltip. */
export function Segmented<T extends string>({
  items,
  value,
  onChange,
  label,
}: {
  items: SegmentItem<T>[];
  value: T;
  onChange: (value: T) => void;
  label: string;
}) {
  return (
    <div className="cp-segment" role="group" aria-label={label}>
      {items.map((item) => (
        <button
          key={item.value}
          type="button"
          className={cn(item.value === value && "is-active", !item.icon && "has-text")}
          aria-pressed={item.value === value}
          onClick={() => onChange(item.value)}
          title={item.label}
          aria-label={item.label}
        >
          {item.icon ? <span className="material-symbols-outlined" aria-hidden>{item.icon}</span> : item.label}
        </button>
      ))}
    </div>
  );
}

export function SelectField<T extends string>({
  icon = "sort",
  value,
  onChange,
  options,
  label,
}: {
  icon?: string;
  value: T;
  onChange: (value: T) => void;
  options: Array<{ value: T; label: string }>;
  label: string;
}) {
  return (
    <label className="cp-select">
      <span className="material-symbols-outlined" aria-hidden>{icon}</span>
      <select value={value} onChange={(event) => onChange(event.target.value as T)} aria-label={label}>
        {options.map((option) => (
          <option key={option.value} value={option.value}>{option.label}</option>
        ))}
      </select>
      <span className="material-symbols-outlined cp-select-caret" aria-hidden>expand_more</span>
    </label>
  );
}

type ToolButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  icon?: string;
  active?: boolean;
  badge?: number;
  spinning?: boolean;
  tone?: Tone;
};

/** Secondary pill action for toolbars and section headers. */
export function ToolButton({ icon, active, badge, spinning, tone, className, children, ...props }: ToolButtonProps) {
  return (
    <button
      type="button"
      className={cn("cp-tool-btn", tone && `cp-tone-${tone}`, active && "is-active", className)}
      {...props}
    >
      {icon ? (
        <span className={cn("material-symbols-outlined", spinning && "animate-spin")} aria-hidden>
          {spinning ? "progress_activity" : icon}
        </span>
      ) : null}
      {children ? <span>{children}</span> : null}
      {badge ? <span className="cp-badge-count">{badge}</span> : null}
    </button>
  );
}

type IconActionProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  icon: string;
  label: string;
  active?: boolean;
  bordered?: boolean;
  spinning?: boolean;
  danger?: boolean;
};

/** Square icon button with its label as tooltip. */
export function IconAction({ icon, label, active, bordered, spinning, danger, className, ...props }: IconActionProps) {
  return (
    <button
      type="button"
      className={cn("cp-icon-btn", bordered && "is-bordered", active && "is-active", danger && "is-danger", className)}
      aria-label={label}
      title={label}
      {...props}
    >
      <span className={cn("material-symbols-outlined", spinning && "animate-spin")} aria-hidden>
        {spinning ? "progress_activity" : icon}
      </span>
    </button>
  );
}

/** Auto-refresh switch with its own countdown tick, so the page does not re-render every second. */
export function AutoRefreshToggle({
  enabled,
  nextAt,
  onToggle,
  title,
}: {
  enabled: boolean;
  nextAt: number;
  onToggle: () => void;
  title?: string;
}) {
  const { t } = useTranslation();
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!enabled) return;
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [enabled]);
  const seconds = Math.max(0, Math.ceil((nextAt - now) / 1000));
  return (
    <button type="button" className={cn("cp-auto", enabled && "is-on")} onClick={onToggle} aria-pressed={enabled} title={title}>
      <span className="cp-auto-switch" aria-hidden />
      <span>{t("kit.autoRefresh")}</span>
      {enabled ? <span className="cp-auto-count">{seconds}s</span> : null}
    </button>
  );
}

export function ProgressLine({ value, total, children }: { value: number; total: number; children: ReactNode }) {
  return (
    <div className="cp-progress" role="status">
      <div className="cp-progress-track">
        <div className="cp-progress-fill" style={{ width: `${total > 0 ? (value / total) * 100 : 0}%` }} />
      </div>
      <span>{children}</span>
    </div>
  );
}

/* ------------------------------------------------------------
   Status pill
   ------------------------------------------------------------ */
export function StatusPill({
  tone = "neutral",
  children,
  title,
  loading,
  dot = true,
  className,
}: {
  tone?: Tone;
  children: ReactNode;
  title?: string;
  loading?: boolean;
  dot?: boolean;
  className?: string;
}) {
  return (
    <span className={cn("cp-status", `cp-tone-${tone}`, loading && "is-loading", className)} title={title}>
      {dot ? <span className="cp-status-dot" aria-hidden /> : null}
      {children}
    </span>
  );
}

/** Square tag for plans, kinds and other static attributes. */
export function Tag({ children, title, tone = "primary" }: { children: ReactNode; title?: string; tone?: Tone }) {
  return (
    <span className={cn("cp-tag", `cp-tone-${tone}`)} title={title}>
      {children}
    </span>
  );
}

/* ------------------------------------------------------------
   Section header — groups a list under a provider, team, etc.
   ------------------------------------------------------------ */
export function SectionHead({
  leading,
  title,
  titleExtra,
  subtitle,
  meta,
  actions,
}: {
  leading?: ReactNode;
  title: ReactNode;
  titleExtra?: ReactNode;
  subtitle?: ReactNode;
  meta?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <header className="cp-section-head">
      {leading}
      <div className="cp-section-title">
        <h3>
          {title}
          {titleExtra}
        </h3>
        {subtitle ? <span>{subtitle}</span> : null}
      </div>
      <div className="cp-section-meta">{meta}</div>
      {actions ? <div className="cp-section-actions">{actions}</div> : null}
    </header>
  );
}

/** Coloured counts next to a section title, e.g. "● 3 ● 1". */
export function ToneCounts({ items, label }: { items: Array<{ tone: Tone; count: number; label: string }>; label?: string }) {
  return (
    <span className="cp-tone-counts" aria-label={label}>
      {items.filter((item) => item.count > 0).map((item) => (
        <span key={item.label} className={`cp-tone-${item.tone}`} title={item.label}>{item.count}</span>
      ))}
    </span>
  );
}

/* ------------------------------------------------------------
   Empty state
   ------------------------------------------------------------ */
export function EmptyPanel({
  icon = "inbox",
  title,
  description,
  action,
  loading,
  compact,
}: {
  icon?: string;
  title: ReactNode;
  description?: ReactNode;
  action?: ReactNode;
  loading?: boolean;
  compact?: boolean;
}) {
  return (
    <div className={cn("cp-empty", compact && "is-compact")}>
      <span className={cn("material-symbols-outlined", loading && "animate-spin")} aria-hidden>
        {loading ? "progress_activity" : icon}
      </span>
      <h3>{title}</h3>
      {description ? <p>{description}</p> : null}
      {action}
    </div>
  );
}

/* ------------------------------------------------------------
   First-run guide
   ------------------------------------------------------------ */
/** Open state for a page guide; it starts open until the user dismisses it once. */
export function usePageGuide(id: string) {
  const storageKey = `tproxy.guide.${id}`;
  const [open, setOpen] = useState(() => readStorage(storageKey) !== "dismissed");
  const dismiss = () => {
    setOpen(false);
    writeStorage(storageKey, "dismissed");
  };
  return { open, setOpen, toggle: () => setOpen((value) => !value), dismiss };
}

export function GuideToggle({ open, onToggle }: { open: boolean; onToggle: () => void }) {
  const { t } = useTranslation();
  return <IconAction icon="help" label={t("kit.guideToggle")} bordered active={open} aria-pressed={open} onClick={onToggle} />;
}

export function PageGuide({
  open,
  title,
  steps,
  legend,
  onDismiss,
}: {
  open: boolean;
  title: ReactNode;
  steps: Array<{ icon: string; text: ReactNode }>;
  legend?: ReactNode;
  onDismiss: () => void;
}) {
  const { t } = useTranslation();
  if (!open) return null;
  return (
    <aside className="cp-guide">
      <div className="cp-guide-head">
        <span className="cp-guide-kicker">{t("kit.guideKicker")}</span>
        <strong>{title}</strong>
      </div>
      <ol className="cp-guide-steps" style={{ ["--cp-guide-cols" as string]: Math.min(steps.length, 3) }}>
        {steps.map((step, index) => (
          <li key={index}>
            <span className="material-symbols-outlined" aria-hidden>{step.icon}</span>
            <p>{step.text}</p>
          </li>
        ))}
      </ol>
      <div className="cp-guide-foot">
        {legend}
        <button type="button" className="cp-link-btn" onClick={onDismiss}>{t("kit.guideDismiss")}</button>
      </div>
    </aside>
  );
}

/** Small colour legend entry for guides: a swatch and its meaning. */
export function LegendItem({ tone, children }: { tone: Tone; children: ReactNode }) {
  return <span className={cn("cp-legend", `cp-tone-${tone}`)}>{children}</span>;
}

/** Inline explanatory note, optionally with a trailing action link. */
export function PageNote({ icon = "info", children, action }: { icon?: string; children: ReactNode; action?: ReactNode }) {
  return (
    <p className="cp-note">
      <span className="material-symbols-outlined" aria-hidden>{icon}</span>
      <span>{children}</span>
      {action}
    </p>
  );
}

export function LinkButton({ className, ...props }: ButtonHTMLAttributes<HTMLButtonElement>) {
  return <button type="button" className={cn("cp-link-btn", className)} {...props} />;
}

/* ------------------------------------------------------------
   Overflow menu
   ------------------------------------------------------------ */
export type ActionMenuItem = {
  key: string;
  icon: string;
  label: string;
  hint?: string;
  danger?: boolean;
  disabled?: boolean;
  onSelect: () => void;
};

const MENU_WIDTH = 248;

/**
 * Overflow menu rendered into document.body with fixed positioning, so a
 * dimmed (opacity) or clipped parent can neither fade nor cut it off.
 */
export function ActionMenu({
  label,
  items,
  disabled,
  text,
  icon = "more_horiz",
  badge,
}: {
  label: string;
  items: Array<ActionMenuItem | null | false | undefined>;
  disabled?: boolean;
  /** Visible trigger text; omitted for the compact "⋯" icon trigger. */
  text?: string;
  icon?: string;
  badge?: number;
}) {
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState<{ top: number; left: number } | null>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const visibleItems = items.filter(Boolean) as ActionMenuItem[];

  const place = () => {
    if (!triggerRef.current) return;
    const rect = triggerRef.current.getBoundingClientRect();
    const menuHeight = menuRef.current?.offsetHeight ?? 0;
    const left = Math.max(8, Math.min(rect.right - MENU_WIDTH, window.innerWidth - MENU_WIDTH - 8));
    const below = rect.bottom + 6;
    const top = below + menuHeight > window.innerHeight - 8 ? Math.max(8, rect.top - menuHeight - 6) : below;
    setPosition({ top, left });
  };

  useLayoutEffect(() => {
    if (open) place();
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const close = () => setOpen(false);
    const onPointer = (event: PointerEvent) => {
      const target = event.target as Node;
      if (menuRef.current?.contains(target) || triggerRef.current?.contains(target)) return;
      close();
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.stopPropagation();
        close();
        triggerRef.current?.focus();
      }
    };
    document.addEventListener("pointerdown", onPointer);
    document.addEventListener("keydown", onKey, true);
    // Follow the trigger while the page scrolls instead of closing: opening a
    // menu on a partly hidden row scrolls it into view, which used to close it at once.
    const onScroll = (event: Event) => {
      if (menuRef.current?.contains(event.target as Node)) return;
      const rect = triggerRef.current?.getBoundingClientRect();
      if (!rect || rect.bottom < 0 || rect.top > window.innerHeight) close();
      else place();
    };
    window.addEventListener("resize", close);
    window.addEventListener("scroll", onScroll, true);
    menuRef.current?.querySelector<HTMLButtonElement>("button:not(:disabled)")?.focus({ preventScroll: true });
    return () => {
      document.removeEventListener("pointerdown", onPointer);
      document.removeEventListener("keydown", onKey, true);
      window.removeEventListener("resize", close);
      window.removeEventListener("scroll", onScroll, true);
    };
  }, [open]);

  return (
    <>
      <button
        ref={triggerRef}
        type="button"
        className={cn(text ? "cp-tool-btn" : "cp-icon-btn", open && "is-active")}
        disabled={disabled}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={label}
        title={label}
        onClick={(event) => {
          event.stopPropagation();
          setOpen((value) => !value);
        }}
      >
        <span className="material-symbols-outlined" aria-hidden>{icon}</span>
        {text ? <span>{text}</span> : null}
        {badge ? <span className="cp-badge-count">{badge}</span> : null}
      </button>
      {open
        ? createPortal(
            <div
              ref={menuRef}
              className="cp-menu"
              role="menu"
              style={{ top: position?.top ?? -9999, left: position?.left ?? -9999, width: MENU_WIDTH }}
              onClick={(event) => event.stopPropagation()}
              onKeyDown={(event) => {
                // React bubbles portal events to the trigger's ancestors (a
                // clickable card, say), whose Enter handler would otherwise
                // swallow the menu item's click.
                event.stopPropagation();
                if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
                event.preventDefault();
                const buttons = [...(menuRef.current?.querySelectorAll<HTMLButtonElement>("button:not(:disabled)") || [])];
                const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
                const next = event.key === "ArrowDown" ? index + 1 : index - 1;
                buttons[(next + buttons.length) % buttons.length]?.focus();
              }}
            >
              {visibleItems.map((item) => (
                <button
                  key={item.key}
                  type="button"
                  role="menuitem"
                  className={cn("cp-menu-item", item.danger && "is-danger")}
                  disabled={item.disabled}
                  onClick={() => {
                    setOpen(false);
                    item.onSelect();
                  }}
                >
                  <span className="material-symbols-outlined" aria-hidden>{item.icon}</span>
                  <span className="cp-menu-item-copy">
                    <span>{item.label}</span>
                    {item.hint ? <small>{item.hint}</small> : null}
                  </span>
                </button>
              ))}
            </div>,
            document.body,
          )
        : null}
    </>
  );
}

/* ------------------------------------------------------------
   Bulk bar
   ------------------------------------------------------------ */
/**
 * Floating bar for actions on the current selection; it sticks to the bottom
 * of the viewport while anything is selected, and Esc clears the selection.
 */
export function BulkBar({
  count,
  busy,
  onClear,
  selectAll,
  children,
}: {
  count: number;
  busy?: boolean;
  onClear: () => void;
  /** Optional "select all N" link shown while the selection is partial. */
  selectAll?: { label: string; onSelect: () => void } | null;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  const clearRef = useRef(onClear);
  clearRef.current = onClear;

  useEffect(() => {
    if (count === 0) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || isTypingTarget(event.target) || isDialogOpen()) return;
      clearRef.current();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [count]);

  if (count === 0) return null;
  return (
    <div className="cp-bulkbar" role="toolbar" aria-label={t("kit.bulkToolbar")}>
      <div className="cp-bulkbar-count">
        <strong>{count}</strong>
        <span>{t("kit.selected", { count })}</span>
      </div>
      {selectAll ? (
        <button type="button" className="cp-link-btn" disabled={busy} onClick={selectAll.onSelect}>
          {selectAll.label}
        </button>
      ) : null}
      <span className="cp-bulkbar-divider" aria-hidden />
      <div className="cp-bulkbar-actions">{children}</div>
      <button
        type="button"
        className="cp-bulkbar-close"
        disabled={busy}
        onClick={onClear}
        aria-label={t("kit.clearSelection")}
        title={`${t("kit.clearSelection")} (Esc)`}
      >
        <span className="material-symbols-outlined" aria-hidden>close</span>
      </button>
    </div>
  );
}

export function BulkButton({ icon, tone, spinning, className, children, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { icon: string; tone?: Tone; spinning?: boolean }) {
  return (
    <button type="button" className={cn("cp-bulk-btn", tone && `cp-tone-${tone}`, className)} {...props}>
      <span className={cn("material-symbols-outlined", spinning && "animate-spin")} aria-hidden>
        {spinning ? "progress_activity" : icon}
      </span>
      {children}
    </button>
  );
}
