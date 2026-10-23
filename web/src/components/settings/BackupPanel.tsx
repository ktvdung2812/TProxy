import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, ConfirmDialog } from "../ui";
import { exportConfig, importConfig, reloadConfig } from "./api";
import { SettingRow, SettingsPanel } from "./SettingsParts";
import { downloadBlob } from "./utils";

type Props = {
  secret: string;
  /** Unsaved edits elsewhere on the page are replaced by the reloaded values. */
  hasUnsavedChanges: boolean;
  onError: (message: string) => void;
  onNotice?: (message: string) => void;
  onMutated?: () => void;
  onReloadSettings: () => Promise<void>;
};

export function BackupPanel({ secret, hasUnsavedChanges, onError, onNotice, onMutated, onReloadSettings }: Props) {
  const { t } = useTranslation();
  const importRef = useRef<HTMLInputElement>(null);
  const [exporting, setExporting] = useState<"json" | "yaml" | null>(null);
  const [importing, setImporting] = useState(false);
  const [reloading, setReloading] = useState(false);
  const [pendingImport, setPendingImport] = useState<File | null>(null);
  const [confirmReload, setConfirmReload] = useState(false);
  const busy = exporting !== null || importing || reloading;

  const handleExport = async (format: "json" | "yaml") => {
    setExporting(format);
    try {
      const { blob, filename } = await exportConfig(secret, format);
      downloadBlob(blob, filename);
      onNotice?.(t("settings.page.exported", { file: filename }));
    } catch (error) {
      onError(error instanceof Error ? error.message : t("settings.exportFailed"));
    } finally {
      setExporting(null);
    }
  };

  const handleImport = async (file: File) => {
    setImporting(true);
    try {
      await importConfig(secret, file);
      onNotice?.(t("settings.page.imported", { file: file.name }));
      onMutated?.();
      await onReloadSettings();
    } catch (error) {
      onError(error instanceof Error ? error.message : t("settings.importFailed"));
    } finally {
      setImporting(false);
      if (importRef.current) importRef.current.value = "";
    }
  };

  const handleReload = async () => {
    setReloading(true);
    try {
      const result = await reloadConfig(secret);
      onNotice?.(result.config_path ? t("settings.page.reloadedFrom", { path: result.config_path }) : t("settings.configReloaded"));
      onMutated?.();
      await onReloadSettings();
    } catch (error) {
      onError(error instanceof Error ? error.message : t("settings.reloadFailed"));
    } finally {
      setReloading(false);
    }
  };

  const unsavedNote = hasUnsavedChanges ? ` ${t("settings.page.unsavedLost")}` : "";

  return (
    <SettingsPanel
      id="backup"
      icon="backup"
      title={t("settings.page.backupTitle")}
      subtitle={t("settings.page.backupSubtitle")}
    >
      <SettingRow label={t("settings.page.exportLabel")} hint={t("settings.page.exportHint")}>
        <div className="st-actions">
          <Button variant="outline" size="sm" icon="download" loading={exporting === "json"} disabled={busy} onClick={() => void handleExport("json")}>
            {exporting === "json" ? t("settings.exporting") : t("settings.exportJSON")}
          </Button>
          <Button variant="outline" size="sm" icon="download" loading={exporting === "yaml"} disabled={busy} onClick={() => void handleExport("yaml")}>
            {exporting === "yaml" ? t("settings.exporting") : t("settings.exportYAML")}
          </Button>
        </div>
      </SettingRow>

      <SettingRow label={t("settings.page.importLabel")} hint={t("settings.page.importHint")}>
        <div className="st-actions">
          <Button variant="outline" size="sm" icon="upload" loading={importing} disabled={busy} onClick={() => importRef.current?.click()}>
            {importing ? t("settings.importing") : t("settings.importFile")}
          </Button>
        </div>
        <input
          ref={importRef}
          type="file"
          accept=".json,.yaml,.yml,application/json,application/yaml"
          hidden
          onChange={(event) => {
            const file = event.target.files?.[0];
            if (file) setPendingImport(file);
          }}
        />
      </SettingRow>

      <SettingRow label={t("settings.page.reloadLabel")} hint={t("settings.page.reloadHint")}>
        <div className="st-actions">
          <Button variant="outline" size="sm" icon="restart_alt" loading={reloading} disabled={busy} onClick={() => setConfirmReload(true)}>
            {reloading ? t("settings.reloading") : t("settings.reloadConfig")}
          </Button>
        </div>
      </SettingRow>

      <ConfirmDialog
        open={pendingImport !== null}
        title={t("settings.page.importConfirmTitle")}
        message={`${t("settings.page.importConfirmMessage", { file: pendingImport?.name || "" })}${unsavedNote}`}
        confirmText={t("settings.page.importConfirm")}
        cancelText={t("common.cancel")}
        variant="danger"
        onClose={() => {
          setPendingImport(null);
          if (importRef.current) importRef.current.value = "";
        }}
        onConfirm={() => {
          const file = pendingImport;
          setPendingImport(null);
          if (file) void handleImport(file);
        }}
      />
      <ConfirmDialog
        open={confirmReload}
        title={t("settings.page.reloadConfirmTitle")}
        message={`${t("settings.page.reloadConfirmMessage")}${unsavedNote}`}
        confirmText={t("settings.reloadConfig")}
        cancelText={t("common.cancel")}
        onClose={() => setConfirmReload(false)}
        onConfirm={() => {
          setConfirmReload(false);
          void handleReload();
        }}
      />
    </SettingsPanel>
  );
}
