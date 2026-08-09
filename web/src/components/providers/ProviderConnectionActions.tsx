import { Button } from "../ui";
import type { ConnectionMethod, ConnectionProfile } from "./connectionMethods";

type Props = {
  profile: ConnectionProfile;
  onMethod: (method: ConnectionMethod) => void;
  busy?: boolean;
  size?: "sm" | "md";
  /** empty = first-time connect (prominent); footer = provider already has accounts */
  placement?: "empty" | "footer";
  /** Hide backup/token-file import actions on provider detail pages. */
  hideImports?: boolean;
};

function methodLabel(method: ConnectionMethod, placement: "empty" | "footer"): string {
  if (placement === "footer" && method.kind === "oauth") {
    return "Add account";
  }
  return method.label;
}

function methodVariant(
  method: ConnectionMethod,
  placement: "empty" | "footer",
): "primary" | "secondary" {
  if (placement === "footer") {
    return "secondary";
  }
  if (method.kind === "oauth" || method.kind === "import_cursor") {
    return "primary";
  }
  return "secondary";
}

/** Provider-specific connection action buttons (9router-style). */
export function ProviderConnectionActions({
  profile,
  onMethod,
  busy,
  size = "sm",
  placement = "empty",
  hideImports = false,
}: Props) {
  const methods = hideImports
    ? profile.methods.filter((method) => method.kind !== "import_9router" && method.kind !== "import_cliproxy")
    : profile.methods;
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
    if (method.kind === "import_9router" || method.kind === "import_cliproxy") return false;
    if (method.kind === "import_cursor" && !cursorPrimary) return false;
    return true;
  });
  const secondary = methods.filter(
    (method) =>
      method.kind === "import_9router" ||
      method.kind === "import_cliproxy" ||
      (method.kind === "import_cursor" && !cursorPrimary),
  );

  return (
    <div className="provider-connection-actions">
      {profile.notice ? <p className="provider-connection-notice">{profile.notice}</p> : null}
      <div className="provider-connection-buttons">
        {primary.map((method) => (
          <Button
            key={`${method.kind}-${method.label}`}
            size={size}
            variant={methodVariant(method, placement)}
            icon={iconForMethod(method.kind)}
            loading={busy && (method.kind === "oauth" || method.kind === "import_cursor")}
            disabled={!method.available || busy}
            title={method.unavailableReason || method.description}
            onClick={() => onMethod(method)}
          >
            {methodLabel(method, placement)}
          </Button>
        ))}
      </div>
      {secondary.length > 0 ? (
        <div className="provider-connection-secondary">
          {secondary.map((method) => (
            <button
              key={`${method.kind}-${method.label}`}
              type="button"
              className="provider-connection-link"
              disabled={!method.available || busy}
              title={method.description}
              onClick={() => onMethod(method)}
            >
              <span className="material-symbols-outlined">{iconForMethod(method.kind)}</span>
              {method.label}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function iconForMethod(kind: ConnectionMethod["kind"]): string {
  switch (kind) {
    case "oauth":
      return "login";
    case "api_key":
      return "key";
    case "cookie":
      return "cookie";
    case "service_account":
      return "badge";
    case "none":
      return "lock_open";
    case "import_cliproxy":
      return "upload";
    case "import_cursor":
      return "edit";
    case "connect_kiro":
      return "shield";
    case "import_9router":
      return "cloud_download";
    default: {
      const _exhaustive: never = kind;
      return _exhaustive;
    }
  }
}
