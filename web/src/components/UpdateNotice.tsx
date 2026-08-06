import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, ConfirmDialog, Modal } from "./ui";
import { fetchVersionInfo, type VersionInfo } from "../lib/version";

type Props = {
  secret: string;
  collapsed?: boolean;
};

export function UpdateNotice({ secret, collapsed = false }: Props) {
  const { t } = useTranslation();
  const [info, setInfo] = useState<VersionInfo | null>(null);
  const [showConfirm, setShowConfirm] = useState(false);
  const [showPanel, setShowPanel] = useState(false);
  const [copied, setCopied] = useState<"npm" | "source" | null>(null);

  useEffect(() => {
    if (!secret) return;
    let cancelled = false;
    void fetchVersionInfo(secret).then((data) => {
      if (!cancelled && data) {
        setInfo(data);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [secret]);

  const copyCommand = async (command: string, kind: "npm" | "source") => {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(kind);
      window.setTimeout(() => setCopied(null), 2000);
    } catch {
      setCopied(null);
    }
  };

  if (!info) {
    return null;
  }

  if (collapsed) {
    if (!info.has_update) {
      return null;
    }
    return (
      <>
        <button
          type="button"
          className="sidebar-update-dot"
          title={t("updates.newVersionTitle", { version: info.latest_version })}
          onClick={() => setShowPanel(true)}
          aria-label={t("updates.newVersionTitle", { version: info.latest_version })}
        />
        <UpdatePanelModal
          open={showPanel}
          info={info}
          copied={copied}
          onClose={() => setShowPanel(false)}
          onCopy={copyCommand}
        />
      </>
    );
  }

  return (
    <>
      <VersionBlock info={info} copied={copied} onOpen={() => setShowConfirm(true)} onCopy={copyCommand} />

      <ConfirmDialog
        open={showConfirm}
        onClose={() => setShowConfirm(false)}
        onConfirm={() => {
          setShowConfirm(false);
          setShowPanel(true);
        }}
        title={t("updates.updateTproxy")}
        message={t("updates.showCommands", { version: info.latest_version || "" })}
        confirmText={t("updates.showCommandsBtn")}
        variant="primary"
      />

      <UpdatePanelModal
        open={showPanel}
        info={info}
        copied={copied}
        onClose={() => setShowPanel(false)}
        onCopy={copyCommand}
      />
    </>
  );
}

function VersionBlock({
  info,
  copied,
  onOpen,
  onCopy,
}: {
  info: VersionInfo;
  copied: "npm" | "source" | null;
  onOpen: () => void;
  onCopy: (command: string, kind: "npm" | "source") => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="sidebar-version-block">
      <span className="sidebar-version-label">v{info.current_version}</span>
      {info.has_update ? (
        <div className="sidebar-update-banner">
          <span className="sidebar-update-title">{t("updates.newVersionBanner", { version: info.latest_version })}</span>
          <div className="sidebar-update-actions">
            <button type="button" className="sidebar-update-btn" onClick={onOpen}>
              {t("updates.updateNow")}
            </button>
            <button
              type="button"
              className="sidebar-update-cmd"
              onClick={() => void onCopy(info.install_command, "npm")}
              title={t("updates.copyCommand")}
            >
              <code>{copied === "npm" ? t("updates.copiedExclaim") : info.install_command}</code>
            </button>
          </div>
        </div>
      ) : null}
    </div>
  );
}

function UpdatePanelModal({
  open,
  info,
  copied,
  onClose,
  onCopy,
}: {
  open: boolean;
  info: VersionInfo;
  copied: "npm" | "source" | null;
  onClose: () => void;
  onCopy: (command: string, kind: "npm" | "source") => void;
}) {
  const { t } = useTranslation();
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={info.latest_version ? t("updates.updateToVersion", { version: info.latest_version }) : t("updates.updateTproxy")}
      subtitle={t("updates.currentVersion", { version: info.current_version })}
      icon="system_update_alt"
      size="md"
      footer={
        <>
          <Button variant="ghost" size="sm" onClick={onClose}>
            {t("common.close")}
          </Button>
          <Button
            variant="secondary"
            size="sm"
            icon="open_in_new"
            onClick={() => window.open(info.release_url, "_blank", "noopener,noreferrer")}
          >
            {t("updates.releaseNotes")}
          </Button>
        </>
      }
    >
      <div className="update-panel">
        <p className="update-panel-lead">
          {t("updates.newerReleaseLead")}
        </p>
        <UpdateCommandSection
          title={t("updates.npmGlobal")}
          command={info.install_command}
          copied={copied === "npm"}
          onCopy={() => void onCopy(info.install_command, "npm")}
        />
        <UpdateCommandSection
          title={t("updates.gitSource")}
          command={info.source_update_command}
          copied={copied === "source"}
          onCopy={() => void onCopy(info.source_update_command, "source")}
        />
      </div>
    </Modal>
  );
}

function UpdateCommandSection({
  title,
  command,
  copied,
  onCopy,
}: {
  title: string;
  command: string;
  copied: boolean;
  onCopy: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="update-panel-section">
      <div className="update-panel-section-head">
        <span className="update-panel-section-title">{title}</span>
        <Button variant="outline" size="sm" icon="content_copy" onClick={onCopy}>
          {copied ? t("common.copied") : t("common.copy")}
        </Button>
      </div>
      <pre className="update-panel-code">{command}</pre>
    </div>
  );
}
