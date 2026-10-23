import { Trans, useTranslation } from "react-i18next";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";
import { IconAction, PageNote, StatusPill } from "../page-kit";
import { Toggle } from "../ui";
import type { AdminSettings } from "./api";
import { PanelSkeleton, SettingRow, SettingsPanel } from "./SettingsParts";
import { isLoopbackHost, looksLikeHttpUrl } from "./utils";

type Props = {
  loading: boolean;
  dirty: boolean;
  settings: AdminSettings | null;
  allowLan: boolean;
  onAllowLanChange: (value: boolean) => void;
  publicBaseUrl: string;
  onPublicBaseUrlChange: (value: string) => void;
};

export function AccessPanel({
  loading,
  dirty,
  settings,
  allowLan,
  onAllowLanChange,
  publicBaseUrl,
  onPublicBaseUrlChange,
}: Props) {
  const { t } = useTranslation();
  const { copied, copy } = useCopyToClipboard();
  const port = settings?.server_port || 28120;
  const savedLan = Boolean(settings?.allow_lan_management);
  const loopback = isLoopbackHost(settings?.server_host);
  const lanUrls = savedLan ? (settings?.lan_ips || []).map((ip) => `http://${ip}:${port}/dashboard/`) : [];
  const trimmedUrl = publicBaseUrl.trim();
  const urlWarning = trimmedUrl && !looksLikeHttpUrl(trimmedUrl) ? t("settings.page.publicUrlInvalid") : undefined;

  return (
    <SettingsPanel
      id="access"
      icon="lan"
      title={t("settings.page.accessTitle")}
      subtitle={t("settings.page.accessSubtitle")}
      dirty={dirty}
    >
      {loading ? (
        <PanelSkeleton rows={3} />
      ) : (
        <>
          <SettingRow label={t("settings.allowLanAccess")} hint={t("settings.page.lanHint", { port })}>
            <div className="st-inline">
              <Toggle
                checked={allowLan}
                onChange={(event) => onAllowLanChange(event.target.checked)}
                aria-label={t("settings.allowLanAccess")}
              />
              <StatusPill tone={allowLan ? "success" : "muted"}>
                {allowLan ? t("settings.page.lanOn") : t("settings.page.lanOff")}
              </StatusPill>
            </div>
            {allowLan && loopback ? (
              <PageNote icon="warning">
                <Trans
                  i18nKey="settings.page.lanLoopbackNote"
                  values={{ host: settings?.server_host || "127.0.0.1" }}
                  components={{ code: <code /> }}
                />
              </PageNote>
            ) : null}
            {lanUrls.length > 0 ? (
              <ul className="st-url-list" aria-label={t("settings.page.lanUrls")}>
                {lanUrls.map((url) => (
                  <li key={url}>
                    <code>{url}</code>
                    <IconAction
                      icon={copied === url ? "check" : "content_copy"}
                      label={copied === url ? t("common.copied") : t("common.copy")}
                      onClick={() => copy(url, url)}
                    />
                  </li>
                ))}
              </ul>
            ) : null}
          </SettingRow>

          <SettingRow
            label={t("settings.page.publicBaseUrl")}
            hint={t("settings.page.publicBaseUrlHint")}
            htmlFor="st-public-url"
            error={urlWarning}
          >
            <input
              id="st-public-url"
              className="input st-input-mono"
              type="url"
              inputMode="url"
              spellCheck={false}
              value={publicBaseUrl}
              placeholder="https://your-tunnel.example.com"
              aria-invalid={Boolean(urlWarning)}
              onChange={(event) => onPublicBaseUrlChange(event.target.value)}
            />
          </SettingRow>

          <SettingRow label={t("settings.remoteManagement")} hint={t("settings.page.remoteHint")}>
            <StatusPill tone={settings?.allow_remote_management ? "warning" : "success"}>
              {settings?.allow_remote_management ? t("settings.allowed") : t("settings.localOnly")}
            </StatusPill>
          </SettingRow>
        </>
      )}
    </SettingsPanel>
  );
}
