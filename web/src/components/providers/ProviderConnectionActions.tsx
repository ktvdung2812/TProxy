import { Button, cn } from "../ui";
import { useTranslation } from "react-i18next";
import { ActionMenu, PageNote } from "../page-kit";
import type { ConnectionMethod, ConnectionProfile } from "./connectionMethods";
import { methodDescription, methodIcon, methodShortText, methodText, profileNotice } from "./connectionText";

type Props = {
  profile: ConnectionProfile;
  onMethod: (method: ConnectionMethod) => void;
  busy?: boolean;
  size?: "sm" | "md";
  /** empty = first-time connect (every method spelled out); footer = provider already has accounts */
  placement?: "empty" | "footer";
};

/** Split methods the way 9router does: real sign-ins first, file imports aside. */
function splitMethods(methods: ConnectionMethod[]) {
  const cursorPrimary =
    methods.some((method) => method.kind === "import_cursor") &&
    !methods.some(
      (method) =>
        method.kind === "oauth" ||
        method.kind === "api_key" ||
        method.kind === "cookie" ||
        method.kind === "service_account" ||
        method.kind === "none",
    );

  const primary = methods.filter((method) => {
    if (method.kind === "import_data") return false;
    if (method.kind === "import_cursor" && !cursorPrimary) return false;
    return true;
  });
  const secondary = methods.filter(
    (method) =>
      method.kind === "import_data" ||
      (method.kind === "import_cursor" && !cursorPrimary),
  );
  return { primary, secondary };
}

/**
 * Provider-specific connection actions. On an empty provider every method is
 * spelled out with its description; once accounts exist the first method is
 * the toolbar's primary button and the rest live in its menu.
 */
export function ProviderConnectionActions({
  profile,
  onMethod,
  busy,
  size = "sm",
  placement = "empty",
}: Props) {
  const { t } = useTranslation();
  const { primary, secondary } = splitMethods(profile.methods);

  if (placement === "footer") {
    const [first, ...rest] = primary.length > 0 ? primary : secondary;
    const others = primary.length > 0 ? [...rest, ...secondary] : rest;
    if (!first) return null;
    return (
      <div className="pv-connect-compact">
        <Button
          size={size}
          variant="primary"
          icon={first.kind === "oauth" ? "person_add" : methodIcon(first.kind)}
          loading={busy && (first.kind === "oauth" || first.kind === "import_cursor")}
          disabled={!first.available || busy}
          title={first.unavailableReason || methodDescription(first, t)}
          onClick={() => onMethod(first)}
        >
          {methodShortText(first, t)}
        </Button>
        {others.length > 0 ? (
          <ActionMenu
            label={t("providers.method.moreWays")}
            icon="expand_more"
            disabled={busy}
            items={others.map((method) => ({
              key: `${method.kind}-${method.label}`,
              icon: methodIcon(method.kind),
              label: methodText(method, t),
              hint: method.unavailableReason || methodDescription(method, t),
              disabled: !method.available,
              onSelect: () => onMethod(method),
            }))}
          />
        ) : null}
      </div>
    );
  }

  const notice = profileNotice(profile, t);
  return (
    <div className="pv-connect">
      {notice ? <PageNote icon="info">{notice}</PageNote> : null}
      <div className={cn("pv-methods", size === "md" && "is-large")}>
        {primary.map((method, index) => (
          <button
            key={`${method.kind}-${method.label}`}
            type="button"
            className={cn("pv-method", index === 0 && "is-recommended")}
            disabled={!method.available || busy}
            title={method.unavailableReason}
            onClick={() => onMethod(method)}
          >
            <span className={cn("material-symbols-outlined", busy && index === 0 && "animate-spin")} aria-hidden>
              {busy && index === 0 ? "progress_activity" : methodIcon(method.kind)}
            </span>
            <span className="pv-method-copy">
              <span className="pv-method-title">
                <strong>{methodText(method, t)}</strong>
                {index === 0 ? <span className="pv-method-badge">{t("providers.method.recommended")}</span> : null}
              </span>
              {methodDescription(method, t) ? <small>{methodDescription(method, t)}</small> : null}
            </span>
            <span className="material-symbols-outlined pv-method-go" aria-hidden>arrow_forward</span>
          </button>
        ))}
      </div>
      {secondary.length > 0 ? (
        <div className="pv-connect-secondary">
          {secondary.map((method) => (
            <button
              key={`${method.kind}-${method.label}`}
              type="button"
              className="cp-tool-btn"
              disabled={!method.available || busy}
              title={methodDescription(method, t)}
              onClick={() => onMethod(method)}
            >
              <span className="material-symbols-outlined" aria-hidden>{methodIcon(method.kind)}</span>
              <span>{methodText(method, t)}</span>
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}
