import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { StatusPill, Tag } from "../page-kit";
import type { AdminSettings } from "./api";
import { PanelSkeleton, SettingRow, SettingsPanel } from "./SettingsParts";
import { RETENTION_KEYS, humanizeDuration } from "./utils";

const RETENTION_LABEL: Record<(typeof RETENTION_KEYS)[number], string> = {
  usage_events: "settings.retention.usageEvents",
  audit_events: "settings.retention.auditEvents",
  media_jobs: "settings.retention.mediaJobs",
  oauth_sessions: "settings.retention.oauthSessions",
  cleanup_interval: "settings.retention.cleanupInterval",
};

export function FeaturesPanel({ loading, settings }: { loading: boolean; settings: AdminSettings | null }) {
  const { t } = useTranslation();
  const capture = Boolean(settings?.payload_capture);
  const rtk = settings?.token_saver?.rtk_enabled !== false;
  return (
    <SettingsPanel
      id="features"
      icon="tune"
      title={t("settings.page.featuresTitle")}
      subtitle={t("settings.page.featuresSubtitle")}
      readOnly
    >
      {loading ? (
        <PanelSkeleton rows={3} />
      ) : (
        <>
          <SettingRow label={t("settings.page.bindAddress")} hint={t("settings.page.bindAddressHint")}>
            <code className="st-value">{`${settings?.server_host || "127.0.0.1"}:${settings?.server_port || 28120}`}</code>
          </SettingRow>
          <SettingRow label={t("settings.payloadCapture")} hint={t("settings.page.payloadCaptureHint")}>
            <StatusPill tone={capture ? "warning" : "muted"}>
              {capture ? t("common.enabled") : t("common.disabled")}
            </StatusPill>
          </SettingRow>
          <SettingRow label={t("settings.tokenSaverRTK")} hint={t("settings.page.tokenSaverHint")}>
            <div className="st-inline">
              <StatusPill tone={rtk ? "success" : "muted"}>{rtk ? t("common.enabled") : t("common.disabled")}</StatusPill>
              <Link className="cp-link-btn" to="/token-saver">{t("settings.configure")}</Link>
            </div>
          </SettingRow>
        </>
      )}
    </SettingsPanel>
  );
}

export function RetentionPanel({ loading, settings }: { loading: boolean; settings: AdminSettings | null }) {
  const { t } = useTranslation();
  return (
    <SettingsPanel
      id="retention"
      icon="schedule"
      title={t("settings.dataRetention")}
      subtitle={t("settings.page.retentionSubtitle")}
      readOnly
      meta={<Tag tone="muted">{t("settings.configYaml")}</Tag>}
    >
      {loading ? (
        <PanelSkeleton rows={4} />
      ) : (
        <dl className="st-kv">
          {RETENTION_KEYS.map((key) => {
            const value = settings?.retention?.[key];
            const human = humanizeDuration(value);
            return (
              <div key={key}>
                <dt>{t(RETENTION_LABEL[key])}</dt>
                <dd>
                  <code>{value || "—"}</code>
                  {human ? (
                    <span>
                      {human.hours
                        ? t("settings.page.daysHours", { count: human.days, hours: human.hours })
                        : t("settings.page.days", { count: human.days })}
                    </span>
                  ) : null}
                </dd>
              </div>
            );
          })}
        </dl>
      )}
    </SettingsPanel>
  );
}
