import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { IconAction, Tag, type Tone } from "../page-kit";
import { cn } from "../ui";

type Props = {
  label: string;
  /** Tone of the label tag and the row stripe. */
  tone?: Tone;
  /** Copyable URL; omit to show `placeholder` text instead. */
  url?: string;
  placeholder?: ReactNode;
  status?: ReactNode;
  hint?: ReactNode;
  copyId: string;
  copied: string | null;
  onCopy: (text: string, id: string) => void;
  onHelpClick?: () => void;
  actions?: ReactNode;
};

/** One base URL: the whole URL is a copy button, so copying is a single click. */
export function EndpointRow({ label, tone = "primary", url, placeholder, status, hint, copyId, copied, onCopy, onHelpClick, actions }: Props) {
  const { t } = useTranslation();
  const isCopied = copied === copyId;
  return (
    <div className={cn("cp-list-row ap-endpoint-row", `cp-tone-${tone}`)}>
      <span className="cp-cell"><Tag tone={tone}>{label}</Tag></span>
      <span className="cp-cell cp-cell-main">
        {url ? (
          <button
            type="button"
            className="ap-url"
            onClick={() => onCopy(url, copyId)}
            title={t("apis.copyEndpoint", { label })}
          >
            <span className="ap-url-text">{url}</span>
            <span className={cn("ap-url-copied", isCopied && "is-visible")} aria-live="polite">
              {isCopied ? t("common.copied") : ""}
            </span>
          </button>
        ) : (
          <span className="ap-url-placeholder">{placeholder}</span>
        )}
        {hint ? <small>{hint}</small> : null}
      </span>
      <span className="cp-cell ap-endpoint-status">{status}</span>
      <span className="cp-cell cp-cell-actions">
        {actions}
        {onHelpClick ? <IconAction icon="help" label={t("apis.lan.howToOpen")} onClick={onHelpClick} /> : null}
        {url ? (
          <IconAction
            icon={isCopied ? "check" : "content_copy"}
            label={isCopied ? t("common.copied") : t("apis.copyEndpoint", { label })}
            onClick={() => onCopy(url, copyId)}
          />
        ) : null}
      </span>
    </div>
  );
}
