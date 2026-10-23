import { useMemo, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { Segmented } from "../page-kit";
import { Button, Modal } from "../ui";
import {
  LAN_PORT_OS_TABS,
  lanPortCommandSections,
  type LanPortOsTab,
} from "./lanPortCommands";

type Props = {
  open: boolean;
  port: number;
  onClose: () => void;
};

export function LanPortHelpModal({ open, port, onClose }: Props) {
  const { t } = useTranslation();
  const [osTab, setOsTab] = useState<LanPortOsTab>("windows");
  const [copiedKey, setCopiedKey] = useState<string | null>(null);

  const sections = useMemo(() => lanPortCommandSections(port, osTab), [port, osTab]);

  const copyCommand = async (key: string, command: string) => {
    try {
      await navigator.clipboard.writeText(command);
      setCopiedKey(key);
      window.setTimeout(() => setCopiedKey(null), 2000);
    } catch {
      /* clipboard may be unavailable */
    }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("apis.lan.openPort")}
      subtitle={t("apis.lanPortHelpSubtitle", { port })}
      icon="lan"
      size="lg"
    >
      <div className="lan-port-help">
        <div className="lan-port-help-tabs">
          <Segmented
            label={t("apis.lan.osLabel")}
            value={osTab}
            onChange={setOsTab}
            items={LAN_PORT_OS_TABS.map((tab) => ({ value: tab.id, label: tab.label }))}
          />
        </div>

        <div className="lan-port-help-sections">
          {sections.map((section, index) => {
            const copyId = `${osTab}_${index}`;
            return (
              <div key={copyId} className="lan-port-help-section">
                <div className="lan-port-help-section-head">
                  <span>{section.title}</span>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="btn-icon-only"
                    icon={copiedKey === copyId ? "check" : "content_copy"}
                    aria-label={copiedKey === copyId ? t("common.copied") : t("apis.lan.copyCommand")}
                    title={copiedKey === copyId ? t("common.copied") : t("apis.lan.copyCommand")}
                    onClick={() => void copyCommand(copyId, section.command)}
                  />
                </div>
                <pre className="lan-port-help-code">{section.command}</pre>
                {section.note ? <p className="lan-port-help-note">{section.note}</p> : null}
              </div>
            );
          })}
        </div>

        <p className="lan-port-help-footer">
          <Trans i18nKey="apis.lan.footer" components={{ code: <code /> }} />
        </p>
      </div>
    </Modal>
  );
}
