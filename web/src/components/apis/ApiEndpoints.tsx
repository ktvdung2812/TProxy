import { useTranslation } from "react-i18next";
import { SectionHead, StatusPill } from "../page-kit";
import { EndpointRow } from "./EndpointRow";
import { SecurityWarning } from "./SecurityWarning";
import { STATE_TONE, type RemoteAccessSummary } from "./TunnelSection";
import { buildHostBaseUrl } from "./utils";

type Props = {
  baseUrl: string;
  serverPort: number;
  /** LAN rows only exist when LAN management is allowed in settings. */
  lan: { allowed: boolean; ips: string[]; restartRequired: boolean };
  remote: RemoteAccessSummary | null;
  /** Remote host (not localhost) with no keys: anyone who reaches the gateway can use it. */
  exposedWithoutKeys: boolean;
  copied: string | null;
  onCopy: (text: string, id: string) => void;
  onLanHelp: () => void;
  onRemoteSetup: () => void;
};

/**
 * Every base URL a client can use, in one copyable list: local first, then
 * LAN addresses, then the public URLs of running tunnels.
 */
export function ApiEndpoints({ baseUrl, serverPort, lan, remote, exposedWithoutKeys, copied, onCopy, onLanHelp, onRemoteSetup }: Props) {
  const { t } = useTranslation();

  const remoteRows = remote
    ? ([
        ["cloudflare", "tunnel_url_row", t("apis.tunnel.tunnel")],
        ["tailscale", "ts_url_row", t("apis.tunnel.tailscale")],
      ] as const).map(([method, copyId, label]) => {
        const item = remote[method];
        const tone = STATE_TONE[item.state];
        return (
          <EndpointRow
            key={method}
            label={label}
            tone={item.state === "online" ? "success" : "neutral"}
            url={item.url}
            placeholder={
              item.state === "off" ? (
                <button type="button" className="cp-link-btn" onClick={onRemoteSetup}>
                  {t("apis.endpoints.remoteOff")}
                </button>
              ) : (
                t(`apis.tunnel.state.${item.state}`)
              )
            }
            status={
              <StatusPill tone={tone} loading={item.state === "checking" || item.state === "busy" || item.state === "reconnecting"}>
                {t(`apis.tunnel.state.${item.state}`)}
              </StatusPill>
            }
            copyId={copyId}
            copied={copied}
            onCopy={onCopy}
          />
        );
      })
    : null;

  return (
    <section className="ap-section" aria-labelledby="ap-endpoints-title">
      <SectionHead
        leading={<span className="material-symbols-outlined cp-section-icon" aria-hidden>api</span>}
        title={<span id="ap-endpoints-title">{t("apis.endpoints.title")}</span>}
        subtitle={t("apis.endpoints.subtitle")}
      />
      {exposedWithoutKeys ? <SecurityWarning tone="danger" message={t("apis.noKeysRemoteWarning")} /> : null}
      {lan.allowed && lan.restartRequired ? (
        <SecurityWarning
          message={t("apis.lanRestartWarning")}
          action={<button type="button" className="cp-link-btn" onClick={onLanHelp}>{t("apis.lan.howToOpen")}</button>}
        />
      ) : null}
      <div className="cp-list ap-endpoint-list">
        <EndpointRow
          label={t("apis.local")}
          url={baseUrl}
          hint={t("apis.endpoints.localHint")}
          status={<StatusPill tone="success">{t("apis.endpoints.ready")}</StatusPill>}
          copyId="local_url"
          copied={copied}
          onCopy={onCopy}
        />
        {lan.allowed ? (
          lan.ips.length > 0 ? (
            lan.ips.map((ip) => (
              <EndpointRow
                key={ip}
                label={t("apis.lan.label")}
                tone="info"
                url={buildHostBaseUrl(ip, serverPort)}
                hint={t("apis.endpoints.lanHint", { port: serverPort })}
                status={
                  lan.restartRequired ? (
                    <StatusPill tone="warning">{t("apis.endpoints.restartNeeded")}</StatusPill>
                  ) : (
                    <StatusPill tone="info">{t("apis.endpoints.network")}</StatusPill>
                  )
                }
                copyId={`lan_url_${ip}`}
                copied={copied}
                onCopy={onCopy}
                onHelpClick={onLanHelp}
              />
            ))
          ) : (
            <EndpointRow
              label={t("apis.lan.label")}
              tone="neutral"
              placeholder={t("apis.noLanIp")}
              copyId="lan_url_empty"
              copied={copied}
              onCopy={onCopy}
              onHelpClick={onLanHelp}
            />
          )
        ) : null}
        {remoteRows}
      </div>
    </section>
  );
}
