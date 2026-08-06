import { useTranslation } from "react-i18next";
import { Button, Select } from "../ui";
import type { CliBaseUrlKind } from "../../lib/cliBaseUrl";

type Props = {
  kind: CliBaseUrlKind;
  baseUrl: string;
  lanUrl: string;
  tunnelUrl: string;
  allowLan: boolean;
  lanIPs: string[];
  selectedLanIP: string;
  copied?: boolean;
  onKindChange: (kind: CliBaseUrlKind) => void;
  onLanIPChange: (ip: string) => void;
  onCopy: () => void;
};

export function CliBaseUrlPicker({
  kind,
  baseUrl,
  lanUrl,
  tunnelUrl,
  allowLan,
  lanIPs,
  selectedLanIP,
  copied,
  onKindChange,
  onLanIPChange,
  onCopy,
}: Props) {
  const { t } = useTranslation();
  const tunnelAvailable = Boolean(tunnelUrl);
  const lanAvailable = allowLan && lanIPs.length > 0 && Boolean(lanUrl);

  return (
    <div className="cli-base-url-picker">
      <div className="usage-segmented cli-base-url-tabs">
        <button type="button" className={kind === "local" ? "active" : ""} onClick={() => onKindChange("local")}>
          {t("cliTools.localhost")}
        </button>
        <button
          type="button"
          className={kind === "lan" ? "active" : ""}
          disabled={!lanAvailable}
          title={lanAvailable ? lanUrl : t("cliTools.enableLanAccess")}
          onClick={() => onKindChange("lan")}
        >
          {t("cliTools.lan")}
        </button>
        <button
          type="button"
          className={kind === "tunnel" ? "active" : ""}
          disabled={!tunnelAvailable}
          title={tunnelAvailable ? tunnelUrl : t("cliTools.configurePublicUrl")}
          onClick={() => onKindChange("tunnel")}
        >
          {t("cliTools.tunnel")}
        </button>
      </div>

      {kind === "lan" && lanIPs.length > 1 ? (
        <div className="cli-base-url-lan-select">
          <Select value={selectedLanIP || lanIPs[0]} onChange={(event) => onLanIPChange(event.target.value)}>
            {lanIPs.map((ip) => (
              <option key={ip} value={ip}>
                {ip}
              </option>
            ))}
          </Select>
        </div>
      ) : null}

      <div className="cli-tool-kv">
        <code>{baseUrl}</code>
        <Button
          variant="outline"
          size="sm"
          className="btn-icon-only"
          icon={copied ? "check" : "content_copy"}
          aria-label={copied ? t("common.copied") : t("common.copy")}
          title={copied ? t("common.copied") : t("common.copy")}
          onClick={onCopy}
        />
      </div>

      {kind === "lan" && !lanAvailable ? (
        <p className="cli-tool-hint">{t("cliTools.enableLanHint")}</p>
      ) : null}
      {kind === "tunnel" && !tunnelAvailable ? (
        <p className="cli-tool-hint">{t("cliTools.setPublicUrlHint")}</p>
      ) : null}
    </div>
  );
}
