import { useTranslation } from "react-i18next";
import { cn, Select } from "../ui";
import type { CliBaseUrlKind } from "../../lib/cliBaseUrl";
import { CopyField } from "./CopyBits";

type Props = {
  kind: CliBaseUrlKind;
  baseUrl: string;
  lanUrl: string;
  tunnelUrl: string;
  tailscaleUrl?: string;
  allowLan: boolean;
  lanIPs: string[];
  selectedLanIP: string;
  onKindChange: (kind: CliBaseUrlKind) => void;
  onLanIPChange: (ip: string) => void;
};

/** Localhost / LAN / Tunnel / Tailscale switch plus the resulting base URL with a copy button. */
export function CliBaseUrlPicker({
  kind,
  baseUrl,
  lanUrl,
  tunnelUrl,
  tailscaleUrl = "",
  allowLan,
  lanIPs,
  selectedLanIP,
  onKindChange,
  onLanIPChange,
}: Props) {
  const { t } = useTranslation();
  const lanAvailable = allowLan && lanIPs.length > 0 && Boolean(lanUrl);
  const options: Array<{ kind: CliBaseUrlKind; icon: string; label: string; available: boolean; title: string }> = [
    { kind: "local", icon: "computer", label: t("cliTools.localhost"), available: true, title: t("cliTools.endpointHint.local") },
    { kind: "lan", icon: "lan", label: t("cliTools.lan"), available: lanAvailable, title: lanAvailable ? lanUrl : t("cliTools.enableLanAccess") },
    { kind: "tunnel", icon: "public", label: t("cliTools.tunnel"), available: Boolean(tunnelUrl), title: tunnelUrl || t("cliTools.configurePublicUrl") },
    {
      kind: "tailscale",
      icon: "vpn_lock",
      label: t("cliTools.tailscale"),
      available: Boolean(tailscaleUrl),
      title: tailscaleUrl || t("cliTools.enableTailscaleHint"),
    },
  ];

  return (
    <div className="ct-base-url">
      <div className="ct-seg" role="group" aria-label={t("cliTools.steps.baseUrl")}>
        {options.map((option) => (
          <button
            key={option.kind}
            type="button"
            className={cn(kind === option.kind && "is-active")}
            aria-pressed={kind === option.kind}
            disabled={!option.available}
            title={option.title}
            onClick={() => onKindChange(option.kind)}
          >
            <span className="material-symbols-outlined" aria-hidden>{option.icon}</span>
            <span>{option.label}</span>
          </button>
        ))}
      </div>

      {kind === "lan" && lanIPs.length > 1 ? (
        <Select value={selectedLanIP || lanIPs[0]} onChange={(event) => onLanIPChange(event.target.value)} aria-label={t("cliTools.lanAddress")}>
          {lanIPs.map((ip) => (
            <option key={ip} value={ip}>
              {ip}
            </option>
          ))}
        </Select>
      ) : null}

      <CopyField value={baseUrl} copyLabel={t("cliTools.copyBaseUrl")} />

      <p className="ct-hint">
        {kind === "lan" && !lanAvailable
          ? t("cliTools.enableLanHint")
          : kind === "tunnel" && !tunnelUrl
            ? t("cliTools.setPublicUrlHint")
            : kind === "tailscale" && !tailscaleUrl
              ? t("cliTools.enableTailscaleHint")
              : t(`cliTools.endpointHint.${kind}`)}
      </p>
    </div>
  );
}
