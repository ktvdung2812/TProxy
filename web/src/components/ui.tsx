import { useEffect } from "react";
import type {
  ButtonHTMLAttributes,
  HTMLAttributes,
  InputHTMLAttributes,
  ReactNode,
  SelectHTMLAttributes,
  TextareaHTMLAttributes,
} from "react";

/** Merge class names — conditional + dedupe, mirrors 9router's cn() util. */
export function cn(...classes: Array<string | false | null | undefined>): string {
  return classes.filter(Boolean).join(" ").replace(/\s+/g, " ").trim();
}

/* ============================================================
   Button
   ============================================================ */
type ButtonVariant = "primary" | "secondary" | "outline" | "ghost" | "danger" | "success";
type ButtonSize = "sm" | "md" | "lg";

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: ButtonVariant;
  size?: ButtonSize;
  icon?: string;
  iconRight?: string;
  loading?: boolean;
  block?: boolean;
};

export function Button({
  variant = "primary",
  size = "md",
  icon,
  iconRight,
  loading = false,
  block = false,
  className,
  disabled,
  children,
  ...props
}: ButtonProps) {
  return (
    <button
      className={cn(
        "btn",
        `btn-${variant}`,
        `btn-${size}`,
        block && "btn-block",
        className,
      )}
      disabled={disabled || loading}
      {...props}
    >
      {loading ? (
        <span className="material-symbols-outlined animate-spin">progress_activity</span>
      ) : icon ? (
        <span className="material-symbols-outlined">{icon}</span>
      ) : null}
      {children}
      {iconRight && !loading ? (
        <span className="material-symbols-outlined">{iconRight}</span>
      ) : null}
    </button>
  );
}

/* ============================================================
   IconButton — round ghost button for header actions
   ============================================================ */
type IconButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  icon: string;
  label: string;
};

export function IconButton({ icon, label, className, ...props }: IconButtonProps) {
  return (
    <button className={cn("icon-btn", className)} aria-label={label} title={label} {...props}>
      <span className="material-symbols-outlined">{icon}</span>
    </button>
  );
}

/* ============================================================
   Badge
   ============================================================ */
type BadgeVariant = "default" | "primary" | "success" | "warning" | "error" | "info";
type BadgeSize = "sm" | "md" | "lg";

type BadgeProps = HTMLAttributes<HTMLSpanElement> & {
  variant?: BadgeVariant;
  size?: BadgeSize;
  dot?: boolean;
  icon?: string;
};

export function Badge({
  variant = "default",
  size = "md",
  dot = false,
  icon,
  className,
  children,
  ...props
}: BadgeProps) {
  return (
    <span className={cn("badge", variant, size, className)} {...props}>
      {dot ? <span className="badge-dot" /> : null}
      {icon ? <span className="material-symbols-outlined" style={{ fontSize: 14 }}>{icon}</span> : null}
      {children}
    </span>
  );
}

/* ============================================================
   Card
   ============================================================ */
type CardPad = "none" | "xs" | "sm" | "md" | "lg";

type CardProps = HTMLAttributes<HTMLDivElement> & {
  title?: ReactNode;
  subtitle?: ReactNode;
  icon?: string;
  action?: ReactNode;
  pad?: CardPad;
  hover?: boolean;
  elev?: boolean;
};

export function Card({
  title,
  subtitle,
  icon,
  action,
  pad = "md",
  hover = false,
  elev = false,
  className,
  children,
  ...props
}: CardProps) {
  const padClass = pad !== "none" ? `card-pad-${pad}` : "";
  return (
    <div className={cn("card", elev && "elev", hover && "hover", padClass, className)} {...props}>
      {(title || action) && (
        <div className="card-head">
          <div className="card-head-left">
            {icon ? (
              <div className="card-icon">
                <span className="material-symbols-outlined">{icon}</span>
              </div>
            ) : null}
            <div>
              {title ? <h3 className="card-title">{title}</h3> : null}
              {subtitle ? <p className="card-subtitle">{subtitle}</p> : null}
            </div>
          </div>
          {action}
        </div>
      )}
      {children}
    </div>
  );
}

/* ============================================================
   Form fields: Input / Select / Textarea + Field wrapper
   ============================================================ */
type FieldProps = {
  label?: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  required?: boolean;
  className?: string;
  children: ReactNode;
};

export function Field({ label, hint, error, required, className, children }: FieldProps) {
  return (
    <div className={cn("field", className)}>
      {label ? (
        <label className="field-label">
          {label}
          {required ? <span className="req">*</span> : null}
        </label>
      ) : null}
      {children}
      {error ? (
        <p className="field-error" style={{ fontSize: 12, color: "var(--color-danger)", display: "flex", alignItems: "center", gap: 4 }}>
          <span className="material-symbols-outlined" style={{ fontSize: 14 }}>error</span>
          {error}
        </p>
      ) : null}
      {hint && !error ? <p className="field-hint" style={{ fontSize: 12, color: "var(--color-text-muted)" }}>{hint}</p> : null}
    </div>
  );
}

type InputProps = InputHTMLAttributes<HTMLInputElement> & {
  icon?: string;
};

export function Input({ icon, className, ...props }: InputProps) {
  if (icon) {
    return (
      <div className="input-icon">
        <span className="material-symbols-outlined">{icon}</span>
        <input className={cn("input", className)} {...props} />
      </div>
    );
  }
  return <input className={cn("input", className)} {...props} />;
}

export function Select({ className, children, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select className={cn("select", className)} {...props}>
      {children}
    </select>
  );
}

export function Textarea({ className, ...props }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea className={cn("textarea", className)} {...props} />;
}

/* ============================================================
   Toggle switch
   ============================================================ */
type ToggleProps = InputHTMLAttributes<HTMLInputElement> & {
  label?: ReactNode;
};

export function Toggle({ label, className, checked, ...props }: ToggleProps) {
  const control = (
    <span className={cn("toggle", className)}>
      <input type="checkbox" checked={checked} {...props} />
      <span className="toggle-track">
        <span className="toggle-thumb" />
      </span>
    </span>
  );
  if (!label) return control;
  return (
    <label style={{ display: "inline-flex", alignItems: "center", gap: 8, cursor: "pointer" }}>
      {control}
      <span style={{ fontSize: 13, color: "var(--color-text-main)" }}>{label}</span>
    </label>
  );
}

/* ============================================================
   Modal — overlay dialog (port of 9router Modal.js, blue theme)
   ============================================================ */
type ModalProps = HTMLAttributes<HTMLDivElement> & {
  open: boolean;
  onClose: () => void;
  title?: ReactNode;
  subtitle?: ReactNode;
  icon?: string;
  headerAction?: ReactNode;
  size?: "sm" | "md" | "lg";
  footer?: ReactNode;
  closeOnBackdrop?: boolean;
};

export function Modal({
  open,
  onClose,
  title,
  subtitle,
  icon,
  headerAction,
  size = "md",
  footer,
  closeOnBackdrop = true,
  className,
  children,
  ...props
}: ModalProps) {
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = "";
    };
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div
      className="modal-overlay"
      onClick={closeOnBackdrop ? onClose : undefined}
      role="dialog"
      aria-modal="true"
    >
      <div
        className={cn("modal", `modal-${size}`, className)}
        onClick={(e) => e.stopPropagation()}
        {...props}
      >
        {(title || icon || headerAction) && (
          <div className="modal-head">
            <div className="modal-title-row">
              {icon ? (
                <div className="modal-icon">
                  <span className="material-symbols-outlined">{icon}</span>
                </div>
              ) : null}
              <div>
                {title ? <h3 className="modal-title">{title}</h3> : null}
                {subtitle ? <p className="modal-subtitle">{subtitle}</p> : null}
              </div>
            </div>
            {headerAction ? <div className="modal-head-action">{headerAction}</div> : null}
            <button className="modal-close" onClick={onClose} aria-label="Close">
              <span className="material-symbols-outlined">close</span>
            </button>
          </div>
        )}
        <div className="modal-body custom-scrollbar">{children}</div>
        {footer ? <div className="modal-footer">{footer}</div> : null}
      </div>
    </div>
  );
}

/* ============================================================
   EmptyState — muted placeholder for empty lists
   ============================================================ */
type EmptyStateProps = {
  icon?: string;
  text: ReactNode;
  hint?: ReactNode;
  className?: string;
};

export function EmptyState({ icon = "inbox", text, hint, className }: EmptyStateProps) {
  return (
    <div className={cn("empty-state", className)}>
      <span className="material-symbols-outlined empty-state-icon">{icon}</span>
      <div>
        <p className="empty-state-text">{text}</p>
        {hint ? <p className="empty-state-hint">{hint}</p> : null}
      </div>
    </div>
  );
}

/* ============================================================
   ConfirmDialog — convenience wrapper around Modal for yes/no prompts
   ============================================================ */
type ConfirmDialogProps = {
  open: boolean;
  title: string;
  message: ReactNode;
  confirmText?: string;
  cancelText?: string;
  variant?: "primary" | "danger";
  onConfirm: () => void;
  onClose: () => void;
};

export function ConfirmDialog({
  open,
  title,
  message,
  confirmText = "Confirm",
  cancelText = "Cancel",
  variant = "primary",
  onConfirm,
  onClose,
}: ConfirmDialogProps) {
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={title}
      icon={variant === "danger" ? "warning" : "help"}
      size="sm"
      footer={
        <>
          <Button variant="secondary" size="md" onClick={onClose}>
            {cancelText}
          </Button>
          <Button variant={variant} size="md" onClick={onConfirm}>
            {confirmText}
          </Button>
        </>
      }
    >
      <p style={{ margin: 0, color: "var(--color-text-muted)", fontSize: 14, lineHeight: 1.6 }}>{message}</p>
    </Modal>
  );
}
