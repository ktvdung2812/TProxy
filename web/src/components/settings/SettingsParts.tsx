import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { SectionHead, StatusPill, Tag } from "../page-kit";
import { cn } from "../ui";
import type { SettingsSectionId } from "./utils";

/** A titled settings group; its id is the jump target of the section index. */
export function SettingsPanel({
  id,
  icon,
  title,
  subtitle,
  dirty,
  readOnly,
  meta,
  actions,
  children,
  footer,
}: {
  id: SettingsSectionId;
  icon: string;
  title: ReactNode;
  subtitle?: ReactNode;
  dirty?: boolean;
  readOnly?: boolean;
  meta?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <section id={`settings-${id}`} className={cn("cp-panel st-panel", dirty && "is-dirty")} data-section={id}>
      <SectionHead
        leading={<span className="material-symbols-outlined cp-section-icon" aria-hidden>{icon}</span>}
        title={title}
        subtitle={subtitle}
        meta={
          <>
            {meta}
            {dirty ? <StatusPill tone="warning">{t("settings.page.unsaved")}</StatusPill> : null}
            {readOnly ? (
              <Tag tone="muted" title={t("settings.page.readOnlyHint")}>{t("settings.page.readOnly")}</Tag>
            ) : null}
          </>
        }
        actions={actions}
      />
      <div className="st-panel-body">{children}</div>
      {footer ? <div className="st-panel-foot">{footer}</div> : null}
    </section>
  );
}

/** Label + helper text on the left, the control on the right (stacked on narrow screens). */
export function SettingRow({
  label,
  hint,
  htmlFor,
  children,
  error,
  className,
}: {
  label: ReactNode;
  hint?: ReactNode;
  htmlFor?: string;
  children: ReactNode;
  error?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("st-row", className)}>
      <div className="st-row-label">
        {htmlFor ? <label htmlFor={htmlFor}>{label}</label> : <span className="st-row-title">{label}</span>}
        {hint ? <p>{hint}</p> : null}
      </div>
      <div className="st-row-control">
        {children}
        {error ? (
          <p className="st-row-error" role="alert">
            <span className="material-symbols-outlined" aria-hidden>error</span>
            {error}
          </p>
        ) : null}
      </div>
    </div>
  );
}

export function PanelSkeleton({ rows = 3 }: { rows?: number }) {
  return (
    <div className="cp-skeleton" aria-busy="true">
      {Array.from({ length: rows }, (_, index) => <span key={index} />)}
    </div>
  );
}
