import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import type { CLITool } from "../../cli-tools/constants";
import { Button } from "../ui";
import { LinkButton } from "../page-kit";
import type { CLIToolStatus } from "./api";
import { CodeBlock, CopyField, Note } from "./CopyBits";
import { INSTALL_GUIDES } from "./installGuides";
import { useToolCopy } from "./toolCopy";
import type { ConfigState } from "./toolState";

export type ApplyMessage = { type: "success" | "error"; text: string };

type Props = {
  tool: CLITool;
  status: CLIToolStatus | null;
  statusLoading: boolean;
  configState: ConfigState | null;
  configPath: string;
  /** Auto-apply only works when the dashboard itself runs on localhost. */
  canAutoApply: boolean;
  applying: boolean;
  /** Why Apply is disabled, if it is. */
  blockedReason: string;
  message: ApplyMessage | null;
  hasManualConfig: boolean;
  onApply: () => void;
  onOpenManual: () => void;
};

/** Step 4 for tools tproxy can configure itself: detection, install help and the Apply button. */
export function CLIToolApplyPanel({
  tool,
  status,
  statusLoading,
  configState,
  configPath,
  canAutoApply,
  applying,
  blockedReason,
  message,
  hasManualConfig,
  onApply,
  onOpenManual,
}: Props) {
  const { t } = useTranslation();
  const toolCopy = useToolCopy();
  const installGuide = INSTALL_GUIDES[tool.id];
  const notDetected = Boolean(status && !status.installed);
  const [showInstall, setShowInstall] = useState(false);

  return (
    <div className="ct-apply">
      {statusLoading && !status ? (
        <p className="ct-hint">{t("cliTools.checkingInstallation")}</p>
      ) : notDetected ? (
        <Note tone="warning">
          <p>{t("cliTools.apply.notDetected", { name: tool.name })}</p>
          <div className="ct-note-actions">
            {installGuide ? (
              <LinkButton onClick={() => setShowInstall((prev) => !prev)} aria-expanded={showInstall}>
                {showInstall ? t("cliTools.hideInstallGuide") : t("cliTools.installGuide")}
              </LinkButton>
            ) : null}
            {hasManualConfig ? <LinkButton onClick={onOpenManual}>{t("cliTools.apply.useManual")}</LinkButton> : null}
          </div>
        </Note>
      ) : null}

      {showInstall && notDetected && installGuide ? (
        <div className="ct-install">
          {installGuide.steps.map((step, index) => (
            <p key={step}>{toolCopy.install(tool.id, index, step)}</p>
          ))}
          {installGuide.command ? <CodeBlock language="shell" code={installGuide.command} /> : null}
          {installGuide.docsUrl ? (
            <a href={installGuide.docsUrl} target="_blank" rel="noreferrer" className="ct-link">
              {t("cliTools.apply.installDocs")}
              <span className="material-symbols-outlined" aria-hidden>open_in_new</span>
            </a>
          ) : null}
        </div>
      ) : null}

      {configState === "other" && status?.endpoint ? (
        <Note tone="warning">
          <p>{t("cliTools.statusOtherHint")}</p>
          <code>{status.endpoint}</code>
        </Note>
      ) : null}

      {configPath ? <CopyField label={t("cliTools.apply.writesTo")} value={configPath} copyLabel={t("cliTools.menu.copyConfigPath")} /> : null}

      {canAutoApply ? (
        <div className="ct-apply-actions">
          <Button variant="primary" size="sm" icon="bolt" loading={applying} disabled={applying || Boolean(blockedReason)} onClick={onApply}>
            {applying ? t("cliTools.applying") : configState === "connected" ? t("cliTools.apply.reapply") : t("cliTools.applyToLocal")}
          </Button>
          {hasManualConfig ? (
            <Button variant="secondary" size="sm" icon="content_copy" onClick={onOpenManual}>
              {t("cliTools.manualConfig")}
            </Button>
          ) : null}
          {blockedReason ? <span className="ct-hint">{blockedReason}</span> : null}
        </div>
      ) : (
        <>
          <Note tone="info">
            <p>{t("cliTools.autoApplyHint")}</p>
            <p>
              {t("cliTools.apply.remoteSteps", { name: tool.name })} <Link to="/apis">{t("nav.apis")}</Link>
            </p>
          </Note>
          {hasManualConfig ? (
            <div className="ct-apply-actions">
              <Button variant="primary" size="sm" icon="content_copy" onClick={onOpenManual}>
                {t("cliTools.manualConfig")}
              </Button>
            </div>
          ) : null}
        </>
      )}

      {message ? (
        <Note tone={message.type === "success" ? "success" : "danger"}>
          <p>{message.text}</p>
        </Note>
      ) : null}
    </div>
  );
}
