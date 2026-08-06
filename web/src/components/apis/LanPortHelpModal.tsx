import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
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
      subtitle={`Allow inbound TCP ${port} so other devices on your network can reach tproxy.`}
      size="lg"
    >
      <div className="lan-port-help">
        <div className="usage-segmented lan-port-help-tabs">
          {LAN_PORT_OS_TABS.map((tab) => (
            <button
              key={tab.id}
              type="button"
              className={osTab === tab.id ? "active" : ""}
              onClick={() => setOsTab(tab.id)}
            >
              {tab.label}
            </button>
          ))}
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
          Ensure <code>server.host</code> is set to <code>0.0.0.0</code> in <code>config.yaml</code> and restart
          tproxy after opening the port.
        </p>
      </div>
    </Modal>
  );
}
