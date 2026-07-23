import { useCallback, useEffect, useState } from "react";
import { Button, Field, Input, Modal } from "../ui";
import { autoImportCursorTokens, importCursorTokens } from "./api";

type Props = {
  open: boolean;
  secret: string;
  providerId: string;
  onClose: () => void;
  onComplete?: () => void;
  onError?: (message: string) => void;
};

export function CursorImportModal({ open, secret, providerId, onClose, onComplete, onError }: Props) {
  const [accessToken, setAccessToken] = useState("");
  const [machineId, setMachineId] = useState("");
  const [label, setLabel] = useState("Cursor IDE");
  const [detecting, setDetecting] = useState(false);
  const [importing, setImporting] = useState(false);
  const [autoDetected, setAutoDetected] = useState(false);
  const [windowsManual, setWindowsManual] = useState(false);
  const [errorMsg, setErrorMsg] = useState("");

  const runAutoDetect = useCallback(async () => {
    setDetecting(true);
    setErrorMsg("");
    setAutoDetected(false);
    setWindowsManual(false);
    try {
      const result = await autoImportCursorTokens(secret);
      if (result.found && result.access_token && result.machine_id) {
        setAccessToken(result.access_token);
        setMachineId(result.machine_id);
        setAutoDetected(true);
      } else if (result.windows_manual) {
        setWindowsManual(true);
      } else {
        setErrorMsg(result.error || "Could not auto-detect Cursor tokens");
      }
    } catch (error) {
      setErrorMsg(error instanceof Error ? error.message : "Auto-detect failed");
    } finally {
      setDetecting(false);
    }
  }, [secret]);

  useEffect(() => {
    if (!open) return;
    setAccessToken("");
    setMachineId("");
    setLabel("Cursor IDE");
    setAutoDetected(false);
    setWindowsManual(false);
    setErrorMsg("");
    void runAutoDetect();
  }, [open, runAutoDetect]);

  const handleImport = async () => {
    if (!accessToken.trim() || !machineId.trim()) {
      setErrorMsg("Access token and machine ID are required");
      return;
    }
    setImporting(true);
    setErrorMsg("");
    try {
      await importCursorTokens(secret, {
        provider_id: providerId,
        label: label.trim() || "Cursor IDE",
        access_token: accessToken.trim(),
        machine_id: machineId.trim(),
      });
      onComplete?.();
      onClose();
    } catch (error) {
      const message = error instanceof Error ? error.message : "Import failed";
      setErrorMsg(message);
      onError?.(message);
    } finally {
      setImporting(false);
    }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Connect Cursor IDE"
      subtitle="Read from Cursor's local database or paste tokens manually"
      icon="edit"
      size="md"
      footer={
        detecting ? null : (
          <>
            <Button variant="ghost" size="sm" onClick={onClose} disabled={importing}>
              Cancel
            </Button>
            <Button
              variant="primary"
              size="sm"
              icon="upload"
              loading={importing}
              disabled={!accessToken.trim() || !machineId.trim()}
              onClick={() => void handleImport()}
            >
              {importing ? "Importing…" : "Import token"}
            </Button>
          </>
        )
      }
    >
      {detecting ? (
        <div className="cursor-import-detecting" style={{ textAlign: "center", padding: "24px 0" }}>
          <span className="material-symbols-outlined animate-spin">progress_activity</span>
          <p className="cursor-import-detecting-title">Auto-detecting tokens…</p>
          <p className="cli-tool-hint">Reading from Cursor IDE database</p>
        </div>
      ) : (
        <>
          {autoDetected ? (
            <div className="cli-tool-note cli-tool-note-info">
              <span className="material-symbols-outlined">check_circle</span>
              <p>Tokens auto-detected from Cursor IDE.</p>
            </div>
          ) : null}
          {windowsManual ? (
            <div className="cli-tool-note cli-tool-note-warning">
              <span className="material-symbols-outlined">info</span>
              <div>
                <p>Could not read the Cursor database automatically.</p>
                <p className="cli-tool-hint">
                  Open Cursor IDE at least once, then click Retry. If it still fails, paste your tokens manually below.
                </p>
                <Button variant="outline" size="sm" icon="refresh" onClick={() => void runAutoDetect()}>
                  Retry
                </Button>
              </div>
            </div>
          ) : null}
          {!autoDetected && !windowsManual && errorMsg ? (
            <div className="cli-tool-note cli-tool-note-warning">
              <span className="material-symbols-outlined">warning</span>
              <p>{errorMsg}</p>
            </div>
          ) : null}
          {!autoDetected && !windowsManual && !errorMsg ? (
            <div className="cli-tool-note cli-tool-note-info">
              <span className="material-symbols-outlined">info</span>
              <p>Cursor IDE not detected. Paste your tokens manually or retry auto-detect.</p>
            </div>
          ) : null}
          <div className="settings-form-grid">
            <Field label="Label">
              <Input value={label} onChange={(event) => setLabel(event.target.value)} />
            </Field>
            <Field label="Access token">
              <textarea
                className="input"
                rows={3}
                value={accessToken}
                onChange={(event) => setAccessToken(event.target.value)}
                placeholder="cursorAuth/accessToken"
              />
            </Field>
            <Field label="Machine ID">
              <Input
                value={machineId}
                onChange={(event) => setMachineId(event.target.value)}
                placeholder="storage.serviceMachineId"
              />
            </Field>
          </div>
          {!windowsManual ? (
            <Button variant="outline" size="sm" icon="refresh" disabled={detecting} onClick={() => void runAutoDetect()}>
              Retry auto-detect
            </Button>
          ) : null}
          <p className="cli-tool-hint">
            Open Cursor IDE and sign in first. Tokens are read from <code>state.vscdb</code> in Cursor&apos;s globalStorage folder.
          </p>
        </>
      )}
    </Modal>
  );
}
