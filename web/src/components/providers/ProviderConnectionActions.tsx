import { Button } from "../ui";
import type { ConnectionMethod, ConnectionProfile } from "./connectionMethods";

type Props = {
  profile: ConnectionProfile;
  onMethod: (method: ConnectionMethod) => void;
  busy?: boolean;
  size?: "sm" | "md";
};

/** Provider-specific connection action buttons (9router-style). */
export function ProviderConnectionActions({ profile, onMethod, busy, size = "sm" }: Props) {
  const primary = profile.methods.filter((method) => method.kind !== "import_9router" && method.kind !== "import_cliproxy");
  const secondary = profile.methods.filter((method) => method.kind === "import_9router" || method.kind === "import_cliproxy");

  return (
    <div className="provider-connection-actions">
      {profile.notice ? <p className="provider-connection-notice">{profile.notice}</p> : null}
      <div className="provider-connection-buttons">
        {primary.map((method) => (
          <Button
            key={`${method.kind}-${method.label}`}
            size={size}
            variant={method.kind === "oauth" ? "primary" : "secondary"}
            icon={iconForMethod(method.kind)}
            loading={busy && method.kind === "oauth"}
            disabled={!method.available || busy}
            title={method.unavailableReason || method.description}
            onClick={() => onMethod(method)}
          >
            {method.label}
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
      return "lock_person";
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
    case "import_9router":
      return "cloud_download";
    default: {
      const _exhaustive: never = kind;
      return _exhaustive;
    }
  }
}
