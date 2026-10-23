import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../ui";
import { IconAction, type Tone } from "../page-kit";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";

/** Copy button that flips to a check for two seconds. */
export function CopyAction({
  text,
  label,
  disabled,
  bordered = true,
}: {
  text: string;
  label?: string;
  disabled?: boolean;
  bordered?: boolean;
}) {
  const { t } = useTranslation();
  const { copied, copy } = useCopyToClipboard();
  const done = copied === "value";
  return (
    <IconAction
      icon={done ? "check" : "content_copy"}
      label={done ? t("common.copied") : label || t("common.copy")}
      bordered={bordered}
      active={done}
      disabled={disabled || !text}
      onClick={(event) => {
        event.stopPropagation();
        copy(text, "value");
      }}
    />
  );
}

/** A single value (URL, key, model id, command) with a copy button. */
export function CopyField({
  label,
  value,
  display,
  copyLabel,
  placeholder,
}: {
  label?: ReactNode;
  value: string;
  /** What to show instead of the raw value, e.g. a masked secret. */
  display?: string;
  copyLabel?: string;
  placeholder?: string;
}) {
  return (
    <div className="ct-copy-field">
      {label ? <span className="ct-copy-label">{label}</span> : null}
      <code className={cn(!value && "is-empty")}>{value ? display ?? value : placeholder ?? "—"}</code>
      <CopyAction text={value} label={copyLabel} />
    </div>
  );
}

/** Code panel with a language label and a copy button. */
export function CodeBlock({
  language,
  code,
  disabled,
  maxHeight,
}: {
  language: string;
  code: string;
  disabled?: boolean;
  maxHeight?: number;
}) {
  return (
    <div className="ct-code">
      <div className="ct-code-head">
        <span>{language}</span>
        <CopyAction text={code} disabled={disabled} bordered={false} />
      </div>
      <pre style={maxHeight ? { maxHeight } : undefined}>
        <code>{code}</code>
      </pre>
    </div>
  );
}

const NOTE_ICON: Partial<Record<Tone, string>> = {
  warning: "warning",
  danger: "error",
  success: "check_circle",
  info: "info",
};

/** Tinted inline note for hints, warnings and apply results. */
export function Note({ tone = "info", icon, children }: { tone?: Tone; icon?: string; children: ReactNode }) {
  return (
    <div className={cn("ct-note", `cp-tone-${tone}`)} role={tone === "danger" ? "alert" : undefined}>
      <span className="material-symbols-outlined" aria-hidden>
        {icon || NOTE_ICON[tone] || "info"}
      </span>
      <div>{children}</div>
    </div>
  );
}

/** Masks a client API key for display; the copy button still copies the full value. */
export function maskSecret(value: string): string {
  const trimmed = value.trim();
  if (trimmed.length <= 10) return trimmed ? "•".repeat(trimmed.length) : "";
  return `${trimmed.slice(0, 7)}••••${trimmed.slice(-4)}`;
}
