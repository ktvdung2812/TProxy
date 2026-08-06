import { useTranslation } from "react-i18next";
import { Input } from "../ui";

type Props = {
  label: string;
  url: string;
  copyId: string;
  copied: string | null;
  onCopy: (text: string, id: string) => void;
  highlight?: boolean;
  actions?: React.ReactNode;
  onHelpClick?: () => void;
};

export function EndpointRow({ label, url, copyId, copied, onCopy, highlight = false, actions, onHelpClick }: Props) {
  const { t } = useTranslation();
  return (
    <div className="endpoint-row">
      <span className={highlight ? "endpoint-row-badge active" : "endpoint-row-badge"}>{label}</span>
      <Input value={url} readOnly className="endpoint-row-input" />
      <button
        type="button"
        className="endpoint-row-copy"
        onClick={() => onCopy(url, copyId)}
        aria-label={`Copy ${label}`}
      >
        <span className="material-symbols-outlined">{copied === copyId ? "check" : "content_copy"}</span>
      </button>
      {onHelpClick ? (
        <button
          type="button"
          className="endpoint-row-help"
          onClick={onHelpClick}
          aria-label={`${label} firewall help`}
          title={t("apis.lan.howToOpen")}
        >
          <span className="material-symbols-outlined">help</span>
        </button>
      ) : null}
      {actions}
    </div>
  );
}
